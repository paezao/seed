// Command seed creates and runs Seeds.
//
//	seed new <name>        create a new Seed (generation 1)
//	seed run               build this Seed's own kernel and run it
//	seed status            what this Seed is right now
//	seed evolve "<intent>" ask this Seed to evolve, and follow along
//	seed generations       list generations
//	seed rollback <n>      return to generation n (as a new generation)
//	seed infra up          start the shared PostgreSQL and sandbox network
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"seed/kernel/config"
	"seed/kernel/git"
	"seed/kernel/infra"
	"seed/kernel/runtime"
	"seed/kernel/template"
)

const usage = `seed — software that grows itself

Usage:
  seed new <name>          create a new Seed in ./<name>
  seed run                 run the Seed in the current directory
  seed status              show what this Seed is right now
  seed evolve "<intent>"   ask this Seed to evolve and follow its progress
  seed generations         list this Seed's generations
  seed rollback <n>        return to generation n
  seed infra up|down       start/stop shared PostgreSQL (docker)

Flags for run/serve: --dir <path>, --addr <host:port>
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "new":
		err = cmdNew(ctx, args)
	case "run":
		err = cmdRun(ctx, args)
	case "serve":
		err = cmdServe(ctx, args)
	case "status":
		err = cmdStatus(ctx, args)
	case "evolve":
		err = cmdEvolve(ctx, args)
	case "generations", "gens":
		err = cmdGenerations(ctx, args)
	case "rollback":
		err = cmdRollback(ctx, args)
	case "infra":
		err = cmdInfra(ctx, args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		var exit exitCode
		if errors.As(err, &exit) {
			os.Exit(int(exit))
		}
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

type exitCode int

func (e exitCode) Error() string { return fmt.Sprintf("exit %d", int(e)) }

func cmdNew(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("new", flag.ContinueOnError)
	noRun := fs.Bool("no-run", false, "only create the Seed; don't start it")
	addr := fs.String("addr", "", "listen address")
	// Allow flags before or after the name.
	var rest []string
	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			rest = append([]string{a}, rest...)
		} else {
			rest = append(rest, a)
		}
	}
	if err := fs.Parse(rest); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: seed new <name> [--no-run] [--addr host:port]")
	}
	name := fs.Arg(0)
	dir, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	if err := template.Create(ctx, dir, filepath.Base(name)); err != nil {
		return err
	}
	if *noRun {
		fmt.Printf(`🌱 Seed created.

Name: %s
Generation: 1

Run:

  cd %s
  seed run --open
`, filepath.Base(name), name)
		return nil
	}
	fmt.Printf("🌱 Seed created: %s (generation 1). Starting it…\n\n", filepath.Base(name))
	runArgs := []string{"--dir", dir, "--open"}
	if *addr != "" {
		runArgs = append(runArgs, "--addr", *addr)
	}
	return cmdRun(ctx, runArgs)
}

// openWhenUp waits for the kernel to listen, then opens the control plane.
func openWhenUp(ctx context.Context, dir string) {
	for i := 0; i < 600; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		b, err := os.ReadFile(runtime.AddrPath(dir))
		if err != nil {
			continue
		}
		addr := strings.TrimSpace(string(b))
		_, port, _ := net.SplitHostPort(addr)
		url := "http://localhost:" + port + "/_seed/"
		resp, err := http.Get("http://" + addr + "/_seed/api/identity/logo")
		if err != nil {
			continue
		}
		resp.Body.Close()
		fmt.Fprintf(os.Stderr, "\n  Talk to me at %s\n\n", url)
		opener := "xdg-open"
		if goruntime.GOOS == "darwin" {
			opener = "open"
		}
		if err := exec.Command(opener, url).Start(); err != nil {
			fmt.Fprintf(os.Stderr, "  (couldn't open a browser: %v)\n", err)
		}
		return
	}
}

type runFlags struct {
	dir, addr string
	open      bool
}

func parseRunFlags(name string, args []string) (runFlags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var f runFlags
	fs.StringVar(&f.dir, "dir", ".", "Seed directory")
	fs.StringVar(&f.addr, "addr", "", "listen address (default from seed.yaml; a busy port moves to the next free one)")
	fs.BoolVar(&f.open, "open", false, "open the control plane in your browser once I'm up")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	abs, err := filepath.Abs(f.dir)
	f.dir = abs
	return f, err
}

// cmdRun builds the Seed's own kernel from its own source and runs it. When
// the kernel exits asking for a restart (an evolution changed it), it is
// rebuilt and started again: the Seed literally runs the latest version of itself.
func cmdRun(ctx context.Context, args []string) error {
	f, err := parseRunFlags("run", args)
	if err != nil {
		return err
	}
	if _, err := config.Load(f.dir); err != nil {
		return err
	}
	bin := filepath.Join(f.dir, ".seed", "bin", "seed")
	for {
		fmt.Fprintln(os.Stderr, "building my kernel…")
		build := exec.CommandContext(ctx, "go", "build", "-o", bin, "./cmd/seed")
		build.Dir = f.dir
		build.Stdout, build.Stderr = os.Stderr, os.Stderr
		// Build exactly the kernel described by go.mod/go.sum: no workspace
		// files or vendor directories can redirect what gets compiled.
		build.Env = append(os.Environ(), "GOWORK=off", "GOFLAGS=-mod=readonly")
		if err := build.Run(); err != nil {
			return fmt.Errorf("building kernel: %w", err)
		}
		child := exec.Command(bin, "serve", "--dir", f.dir)
		child.Stdout, child.Stderr, child.Stdin = os.Stdout, os.Stderr, os.Stdin
		child.Env = os.Environ()
		if f.addr != "" {
			child.Env = append(child.Env, "SEED_ADDR="+f.addr)
		}
		_ = os.Remove(runtime.AddrPath(f.dir))
		if err := child.Start(); err != nil {
			return err
		}
		if f.open {
			go openWhenUp(ctx, f.dir)
			f.open = false // only the first start, not kernel restarts
		}
		done := make(chan error, 1)
		go func() { done <- child.Wait() }()
		select {
		case <-ctx.Done():
			_ = child.Process.Signal(syscall.SIGTERM)
			<-done
			return nil
		case err := <-done:
			var ee *exec.ExitError
			if errors.As(err, &ee) && ee.ExitCode() == runtime.RestartExitCode {
				fmt.Fprintln(os.Stderr, "my kernel changed; restarting into it…")
				continue
			}
			if err != nil {
				return err
			}
			return nil
		}
	}
}

func cmdServe(ctx context.Context, args []string) error {
	f, err := parseRunFlags("serve", args)
	if err != nil {
		return err
	}
	if f.addr != "" {
		os.Setenv("SEED_ADDR", f.addr)
	}
	logs := runtime.NewLogBuffer(5000, os.Stderr)
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelInfo})))
	k, err := runtime.Boot(ctx, f.dir, logs)
	if err != nil {
		return err
	}
	err = k.Serve(ctx)
	if errors.Is(err, runtime.ErrRestart) {
		return exitCode(runtime.RestartExitCode)
	}
	return err
}

// ---- client commands talk to the running kernel

func baseURL() (string, *config.Config, error) {
	cfg, err := config.Load(".")
	if err != nil {
		return "", nil, err
	}
	addr := cfg.Server.Addr
	if b, err := os.ReadFile(runtime.AddrPath(cfg.Root)); err == nil {
		addr = strings.TrimSpace(string(b))
	}
	return "http://" + addr + "/_seed/api", cfg, nil
}

// controlToken is the running kernel's API token (written to .seed/).
func controlToken() string {
	b, _ := os.ReadFile(runtime.ControlTokenPath("."))
	return strings.TrimSpace(string(b))
}

func getJSON(ctx context.Context, url string, v any) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set("X-Seed-Token", controlToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("is the Seed running? (%w)", err)
	}
	defer resp.Body.Close()
	return decodeResp(resp, v)
}

func postJSON(ctx context.Context, url string, body, v any) error {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(b))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("X-Seed-Token", controlToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("is the Seed running? (%w)", err)
	}
	defer resp.Body.Close()
	return decodeResp(resp, v)
}

func decodeResp(resp *http.Response, v any) error {
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		var e struct{ Error string }
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.Unmarshal(b, v)
}

func cmdStatus(ctx context.Context, args []string) error {
	base, _, err := baseURL()
	if err != nil {
		return err
	}
	var s struct {
		Name       string
		Purpose    string
		Identity   struct{ Name, Tagline string }
		Generation *struct {
			Number int
			Title  string
			Commit string
		}
		Organism        map[string]string
		Model           struct{ Provider, Name string }
		ActiveEvolution *struct{ ID, Title, Intent, Status string } `json:"active_evolution"`
	}
	if err := getJSON(ctx, base+"/status", &s); err != nil {
		return err
	}
	fmt.Printf("%s (%s)\n", s.Identity.Name, s.Name)
	if s.Identity.Tagline != "" {
		fmt.Println(s.Identity.Tagline)
	}
	if s.Purpose == "" {
		fmt.Println("Purpose: none yet")
	} else {
		fmt.Println("Purpose:", s.Purpose)
	}
	if s.Generation != nil {
		fmt.Printf("Generation %d: %s (%s)\n", s.Generation.Number, s.Generation.Title, shortHash(s.Generation.Commit))
	}
	fmt.Println("Body:", s.Organism["state"])
	fmt.Printf("Mind: %s/%s\n", s.Model.Provider, s.Model.Name)
	if e := s.ActiveEvolution; e != nil {
		fmt.Printf("Evolving: %s [%s]\n", firstNonEmpty(e.Title, e.Intent), e.Status)
	}
	return nil
}

func cmdEvolve(ctx context.Context, args []string) error {
	base, _, err := baseURL()
	if err != nil {
		return err
	}
	intent := strings.Join(args, " ")
	if intent == "" {
		fmt.Print("What should I become? > ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		intent = strings.TrimSpace(line)
	}
	if intent == "" {
		return errors.New("no intent given")
	}
	var e struct{ ID string }
	if err := postJSON(ctx, base+"/evolutions", map[string]string{"intent": intent}, &e); err != nil {
		return err
	}
	fmt.Println("evolution", e.ID)
	return follow(ctx, base, e.ID)
}

// follow streams an evolution's progress until it ends.
func follow(ctx context.Context, base, id string) error {
	req, _ := http.NewRequestWithContext(ctx, "GET", base+"/events", nil)
	req.Header.Set("X-Seed-Token", controlToken())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	var event string
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			event = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := []byte(strings.TrimPrefix(line, "data: "))
			switch event {
			case "evolution_event":
				var ev struct {
					EvolutionID string `json:"evolution_id"`
					Kind        string
					Summary     string
				}
				if json.Unmarshal(data, &ev) == nil && ev.EvolutionID == id && ev.Kind != "agent_text" {
					prefix := "  "
					if ev.Kind == "phase" {
						prefix = "▸ "
					}
					fmt.Println(prefix + ev.Summary)
				}
			case "evolution":
				var ev struct {
					ID, Status, Error string
					NewGeneration     *int   `json:"new_generation"`
					Commit            string `json:"commit"`
				}
				if json.Unmarshal(data, &ev) == nil && ev.ID == id {
					switch ev.Status {
					case "complete":
						fmt.Printf("\nI am now generation %d (%s).\n", *ev.NewGeneration, shortHash(ev.Commit))
						return nil
					case "failed", "cancelled", "rolled_back":
						fmt.Printf("\n%s: %s\n", ev.Status, ev.Error)
						return exitCode(1)
					}
				}
			case "approval":
				var a struct{ ID, Action, Status string }
				if json.Unmarshal(data, &a) == nil && a.Status == "pending" {
					fmt.Printf("  ⏸ waiting for approval in the control plane: %s\n", a.Action)
				}
			}
		}
	}
	return sc.Err()
}

func cmdGenerations(ctx context.Context, args []string) error {
	base, cfg, err := baseURL()
	if err != nil {
		return err
	}
	var gens []struct {
		Number      int
		Title       string
		Commit      string
		Current     bool
		TestsPassed *int      `json:"tests_passed"`
		CreatedAt   time.Time `json:"created_at"`
	}
	if err := getJSON(ctx, base+"/generations", &gens); err != nil {
		// Not running: Git is the source of truth.
		commits, gerr := git.Open(cfg.Root).Log(ctx, "HEAD", 200)
		if gerr != nil {
			return err
		}
		for _, c := range commits {
			if g := git.Trailers(c.Body)["Generation"]; g != "" {
				fmt.Printf("%-12s %s  %s\n", "gen "+g, shortHash(c.Hash), c.Subject)
			}
		}
		return nil
	}
	for _, g := range gens {
		mark := " "
		if g.Current {
			mark = "*"
		}
		tests := ""
		if g.TestsPassed != nil {
			tests = fmt.Sprintf("  ✓ %d tests", *g.TestsPassed)
		}
		fmt.Printf("%s generation %-3d %s  %s%s\n", mark, g.Number, shortHash(g.Commit), g.Title, tests)
	}
	return nil
}

func cmdRollback(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: seed rollback <generation>")
	}
	base, _, err := baseURL()
	if err != nil {
		return err
	}
	var e struct{ ID string }
	if err := postJSON(ctx, base+"/generations/"+args[0]+"/rollback", nil, &e); err != nil {
		return err
	}
	return follow(ctx, base, e.ID)
}

func cmdInfra(ctx context.Context, args []string) error {
	if len(args) != 1 {
		return errors.New("usage: seed infra up|down")
	}
	d := config.Defaults()
	managed, err := infra.ManagedAdminURL()
	if err != nil {
		return err
	}
	admin := infra.Admin{URL: managed}
	if v := os.Getenv("SEED_DATABASE_URL"); v != "" {
		admin.URL = v
	}
	switch args[0] {
	case "up":
		if err := infra.EnsureNetwork(ctx, d.Sandbox.Network); err != nil {
			return err
		}
		if err := infra.EnsurePostgres(ctx, d.Sandbox.Network, admin); err != nil {
			return err
		}
		fmt.Println("PostgreSQL ready on 127.0.0.1:" + infra.PostgresHostPort)
	case "down":
		_, err := infra.Docker(ctx, "stop", infra.PostgresContainer)
		return err
	default:
		return errors.New("usage: seed infra up|down")
	}
	return nil
}

func shortHash(h string) string {
	if len(h) > 10 {
		return h[:10]
	}
	return h
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}
