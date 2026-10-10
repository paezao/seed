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
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"seed/kernel/config"
	"seed/kernel/runtime"
	"seed/kernel/template"
)

const usage = `seed — software that grows itself

Usage:
  seed new <name> -e OPENROUTER_API_KEY   plant a Seed in ./<name>, start it, open it
  seed run                 run the Seed in the current directory
  seed status              show what this Seed is right now
  seed evolve "<intent>"   ask this Seed to evolve and follow its progress
  seed generations         list this Seed's generations
  seed rollback <n>        return to generation n
  seed login [--print]     sign a browser in to this Seed's control plane
  seed plant --into DIR    plant a new Seed into an empty DIR (deploy images do this on first boot)
  seed stop                stop the Seed in the current directory
  seed upgrade [--force]   give this Seed the kernel of this seed CLI (as a new generation)

Flags for run: -e KEY[=VALUE] (secrets, repeatable), --env-file <file>, --dir <path>,
               --addr <host:port>, --open, --detach, --native

Secrets (your model API key, tokens) are passed at every start and never stored by the Seed.
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
	case "stop":
		err = cmdStop(ctx, args)
	case "image":
		err = cmdImage(ctx, args)
	case "kernel-rollback":
		err = cmdKernelRollback(ctx, args)
	case "release":
		err = cmdRelease(args)
	case "plant":
		err = cmdPlant(ctx, args)
	case "login":
		err = cmdLogin(ctx, args)
	case "upgrade":
		err = cmdUpgrade(ctx, args)
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
	secrets := map[string]string{}
	fs.Var(envFlag{secrets}, "e", "pass a secret to the first run: KEY or KEY=VALUE; repeatable")
	envFile := fs.String("env-file", "", "pass secrets from a KEY=VALUE file")
	// Allow flags before or after the name (flags that take a value keep it).
	takesValue := map[string]bool{"e": true, "addr": true, "env-file": true}
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			positional = append(positional, a)
			continue
		}
		flags = append(flags, a)
		name := strings.TrimLeft(a, "-")
		if takesValue[name] && !strings.Contains(a, "=") && i+1 < len(args) {
			i++
			flags = append(flags, args[i])
		}
	}
	if err := fs.Parse(append(flags, positional...)); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return errors.New("usage: seed new <name> [-e KEY]... [--env-file file] [--no-run] [--addr host:port]")
	}
	name := fs.Arg(0)
	dir, err := filepath.Abs(name)
	if err != nil {
		return err
	}
	if err := checkSeedHome(dir); err != nil {
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
	if *envFile != "" {
		runArgs = append(runArgs, "--env-file", *envFile)
	}
	for k, v := range secrets { // in-process: values never reach a command line
		runArgs = append(runArgs, "-e", k+"="+v)
	}
	return cmdRun(ctx, runArgs)
}

// openWhenUp waits for the kernel to listen, then signs my owner's browser
// in with a one-time link.
func openWhenUp(ctx context.Context, root string, addrOf func() string) {
	for i := 0; i < 1200; i++ {
		select {
		case <-ctx.Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
		addr := addrOf()
		if addr == "" {
			continue
		}
		// Retries until the kernel has written this start's token.
		link, err := loginLink(ctx, addr, root)
		if err != nil {
			continue
		}
		_, port, _ := net.SplitHostPort(addr)
		fmt.Fprintf(os.Stderr, "\n  Talk to me at http://localhost:%s/_seed/ (signing your browser in)\n\n", port)
		openBrowser(link)
		return
	}
}

// loginLink asks the running kernel for a one-time sign-in link.
func loginLink(ctx context.Context, addr, root string) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://"+addr+"/_seed/api/login-links", strings.NewReader("{}"))
	req.Header.Set("content-type", "application/json")
	req.Header.Set("X-Seed-Token", readSeedFile(root, ".seed/control-token"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out struct{ Path string }
	if err := decodeResp(resp, &out); err != nil {
		return "", err
	}
	// Only ever open a sign-in link on this Seed (the response is handed to
	// the system's URL opener).
	if !regexp.MustCompile(`^/_seed/login\?code=[0-9a-f]{32}$`).MatchString(out.Path) {
		return "", errors.New("unexpected sign-in link")
	}
	_, port, _ := net.SplitHostPort(addr)
	return "http://localhost:" + port + out.Path, nil
}

func openBrowser(link string) {
	opener, args := browserCommand(link)
	if err := exec.Command(opener, args...).Start(); err != nil {
		fmt.Fprintf(os.Stderr, "  Couldn't open a browser (%v). Open this sign-in link (one use, 15 minutes):\n  %s\n", err, link)
	}
}

// cmdLogin signs a browser in to the running Seed.
func cmdLogin(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	dir := fs.String("dir", ".", "Seed directory")
	print := fs.Bool("print", false, "print the link instead of opening a browser")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*dir)
	if err != nil {
		return err
	}
	addr, err := runningAddr(ctx, cfg)
	if err != nil {
		return err
	}
	link, err := loginLink(ctx, addr, cfg.Root)
	if err != nil {
		return err
	}
	if *print {
		fmt.Println(link)
		fmt.Fprintln(os.Stderr, "One use, valid for 15 minutes.")
		return nil
	}
	fmt.Fprintln(os.Stderr, "Signing your browser in…")
	openBrowser(link)
	return nil
}

type runFlags struct {
	dir, addr            string
	open, native, detach bool
	// secrets are KEY=VALUE pairs passed with -e / --env-file.
	secrets map[string]string
}

func (f runFlags) secretNames() []string {
	names := make([]string, 0, len(f.secrets))
	for k := range f.secrets {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

func (f runFlags) secretEnv() []string {
	var env []string
	for _, k := range f.secretNames() {
		env = append(env, k+"="+f.secrets[k])
	}
	return env
}

// envFlag collects -e KEY (value from the current environment) and -e KEY=VALUE.
type envFlag struct{ m map[string]string }

func (e envFlag) String() string { return "" }
func (e envFlag) Set(v string) error {
	k, val, hasVal := strings.Cut(v, "=")
	k = strings.TrimSpace(k)
	if !envName.MatchString(k) {
		return fmt.Errorf("invalid variable name %q", k)
	}
	if !hasVal {
		var ok bool
		if val, ok = os.LookupEnv(k); !ok {
			return fmt.Errorf("-e %s: %s is not set in your environment", k, k)
		}
	}
	e.m[k] = val
	return nil
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// readEnvFile parses KEY=VALUE lines (# comments, optional quotes).
func readEnvFile(path string, into map[string]string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	for i, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok || !envName.MatchString(strings.TrimSpace(k)) {
			return fmt.Errorf("%s:%d: expected KEY=VALUE", path, i+1)
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		}
		into[strings.TrimSpace(k)] = v
	}
	return nil
}

func parseRunFlags(name string, args []string) (runFlags, error) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	var f runFlags
	fs.StringVar(&f.dir, "dir", ".", "Seed directory")
	fs.StringVar(&f.addr, "addr", "", "listen address (default from seed.yaml; a busy port moves to the next free one)")
	fs.BoolVar(&f.open, "open", false, "open the control plane in your browser once I'm up")
	fs.BoolVar(&f.native, "native", false, "run directly on this machine instead of in my container (kernel development)")
	fs.BoolVar(&f.detach, "detach", false, "start in the background and return")
	f.secrets = map[string]string{}
	fs.Var(envFlag{f.secrets}, "e", "pass a secret: KEY (value from your environment) or KEY=VALUE; repeatable")
	var envFile string
	fs.StringVar(&envFile, "env-file", "", "pass secrets from a KEY=VALUE file (keep it outside the Seed)")
	if err := fs.Parse(args); err != nil {
		return f, err
	}
	if envFile != "" {
		if err := readEnvFile(envFile, f.secrets); err != nil {
			return f, err
		}
	}
	abs, err := filepath.Abs(f.dir)
	f.dir = abs
	return f, err
}

// cmdRun builds the Seed's own kernel from its own source and runs it. When
// the kernel exits asking for a restart (an evolution changed it), it is
// rebuilt and started again: the Seed literally runs the latest version of itself.
// runNative builds and runs the kernel directly on this machine (kernel
// development; needs bubblewrap and PostgreSQL server binaries installed).
func runNative(ctx context.Context, f runFlags) error {
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
		child.Env = append(os.Environ(), f.secretEnv()...)
		child.Env = append(child.Env, runtime.SecretsEnv+"="+strings.Join(f.secretNames(), ","))
		if f.addr != "" {
			child.Env = append(child.Env, "SEED_ADDR="+f.addr)
		}
		if p, err := runtime.NativeAddrPath(f.dir); err == nil {
			_ = os.Remove(p)
		}
		if err := child.Start(); err != nil {
			return err
		}
		if f.open {
			go openWhenUp(ctx, f.dir, func() string {
				a, _ := nativeAddr(f.dir)
				return a
			})
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
	addr, err := runningAddr(context.Background(), cfg)
	if err != nil {
		return "", cfg, err
	}
	return "http://" + addr + "/_seed/api", cfg, nil
}

// runningAddr is where the running Seed listens. In a container: the port
// the engine published. Natively (--native): the address the kernel wrote to
// my cache. Never an address from the Seed's folder (the Seed controls it),
// and never a guessed port (another program may be listening on it).
func runningAddr(ctx context.Context, cfg *config.Config) (string, error) {
	if a, err := publishedAddr(ctx, containerName(ctx, cfg)); err == nil {
		return a, nil
	}
	if a, ok := nativeAddr(cfg.Root); ok {
		return a, nil
	}
	return "", errors.New("I'm not running: start me with `seed run -e OPENROUTER_API_KEY`")
}

// controlToken is the running kernel's API token (written to .seed/).
func controlToken() string {
	root, _ := filepath.Abs(".")
	return readSeedFile(root, ".seed/control-token")
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
	previewHinted := false
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
					Preview           *struct {
						State string `json:"state"`
					} `json:"preview"`
				}
				if json.Unmarshal(data, &ev) == nil && ev.ID == id {
					if ev.Status == "ready" && ev.Preview != nil && ev.Preview.State == "ready" && !previewHinted {
						previewHinted = true
						fmt.Printf("  ⏸ ready to try before it goes live: open the control plane to try it, then apply it, ask for changes or discard it\n")
					}
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
	base, _, err := baseURL()
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
		return fmt.Errorf("start me to list my generations: %w", err)
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

// nativeAddr reads where a natively running kernel listens, from the owner's
// cache directory (never from the Seed's folder), loopback only.
func nativeAddr(root string) (string, bool) {
	p, err := runtime.NativeAddrPath(root)
	if err != nil {
		return "", false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return "", false
	}
	return loopbackAddr(strings.TrimSpace(string(b)))
}

// cmdPlant plants a new Seed into dir if it holds none (a deploy image runs
// it on first boot, against an empty volume that may hold lost+found).
func cmdPlant(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("plant", flag.ContinueOnError)
	into := fs.String("into", "/seed", "directory to plant into")
	name := fs.String("name", "seed", "the new Seed's name")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(*into, "seed.yaml")); err == nil {
		return nil // already a Seed
	}
	entries, err := os.ReadDir(*into)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.Name() != "lost+found" {
			return fmt.Errorf("%s is not empty and holds no Seed (found %s): refusing to plant over it", *into, e.Name())
		}
	}
	// Plant beside, on the same volume, then move into place.
	stage := filepath.Join(*into, ".planting")
	_ = os.RemoveAll(stage)
	fmt.Fprintf(os.Stderr, "planting a new Seed named %s…\n", *name)
	if err := template.Create(ctx, stage, *name); err != nil {
		return err
	}
	planted, err := os.ReadDir(stage)
	if err != nil {
		return err
	}
	for _, e := range planted {
		if err := os.Rename(filepath.Join(stage, e.Name()), filepath.Join(*into, e.Name())); err != nil {
			return err
		}
	}
	return os.Remove(stage)
}

// modelKeys are the names my kernel reads a model key from.
var modelKeys = []string{"OPENROUTER_API_KEY", "ANTHROPIC_API_KEY", "OPENAI_API_KEY"}

// keyHint warns when no model key is passed under a name I read, but one
// seems to be passed under another (e.g. -e MY_OPENROUTER_KEY).
func keyHint(names []string) string {
	for _, n := range names {
		for _, k := range modelKeys {
			if n == k {
				return ""
			}
		}
	}
	for _, n := range names {
		for _, k := range modelKeys {
			provider := strings.SplitN(k, "_", 2)[0] // OPENROUTER, ANTHROPIC, OPENAI
			if strings.Contains(n, provider) {
				return fmt.Sprintf("note: I read my model key from %s, not %s. Pass it as: -e %s=$%s", k, n, k, n)
			}
		}
	}
	if len(names) == 0 {
		return "note: no model key passed; I'll start without a brain. Pass one with -e OPENROUTER_API_KEY"
	}
	return ""
}
