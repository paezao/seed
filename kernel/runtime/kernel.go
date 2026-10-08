// Package runtime boots a Seed: it connects memory, sandboxing, the model and
// the evolution engine, keeps the live organism running, and serves the
// control plane.
package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"seed/kernel/config"
	"seed/kernel/events"
	"seed/kernel/evolution"
	"seed/kernel/git"
	"seed/kernel/infra"
	"seed/kernel/knowledge"
	"seed/kernel/memory"
	"seed/kernel/models"
	"seed/kernel/permissions"
	"seed/kernel/pg"
	"seed/kernel/sandbox"
)

// ErrRestart asks the launcher to rebuild and restart the kernel (after an
// evolution changed kernel code).
var ErrRestart = errors.New("kernel restart requested")

// RestartExitCode is the process exit code that signals ErrRestart.
const RestartExitCode = 75

type Kernel struct {
	Cfg   *config.Config
	Store *memory.Store
	Bus   *events.Bus
	Repo  *git.Repo
	Mind  *models.Switchable
	// Secrets passed by the owner at start (memory only).
	Secrets      *Secrets
	Driver       sandbox.Driver
	SandboxImage string
	Admin        infra.Admin
	// PG is my private PostgreSQL (nil when an external server is configured).
	PG        *pg.Server
	Policy    *permissions.Policy
	Approvals *evolution.Approvals
	Orch      *evolution.Orchestrator
	Organism  *Organism
	Chat      *Chat
	Logs      *LogBuffer

	restart chan struct{}
	// Token authorizes control-plane API calls. It is embedded only in the
	// control plane page and written to .seed/control-token for the CLI, so
	// organism code (same origin) cannot drive the kernel.
	Token string
}

// Boot assembles a kernel for the Seed at root.
func Boot(ctx context.Context, root string, logs *LogBuffer) (*Kernel, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	k := &Kernel{Cfg: cfg, Bus: events.NewBus(), Logs: logs, restart: make(chan struct{}, 1), Token: infra.RandomSecret(24),
		Secrets: LoadSecrets()}
	if err := WriteControlToken(cfg.Root, k.Token); err != nil {
		return nil, err
	}
	k.Repo = git.Open(cfg.Root)
	if _, err := k.Repo.Head(ctx); err != nil {
		return nil, fmt.Errorf("%s is not a git repository with history (create Seeds with `seed new`): %w", cfg.Root, err)
	}
	if roots, err := k.Repo.Run(ctx, "rev-list", "--max-parents=0", "HEAD"); err == nil && len(roots) >= 8 {
		lines := strings.Split(roots, "\n")
		cfg.Instance = lines[len(lines)-1][:8]
	}
	k.Policy = permissions.NewPolicy(cfg.Permissions.Safe, cfg.Permissions.Review, cfg.Permissions.Dangerous, cfg.Kernel.Evolvable)

	// Body: a private PostgreSQL inside my own folder (unless an external
	// server is configured) and a sandbox driver for generated code.
	stateDir := filepath.Join(cfg.Root, ".seed")
	if cfg.Database.URL == "" {
		k.PG, err = pg.Start(ctx, stateDir)
		if err != nil {
			return nil, fmt.Errorf("my database: %w", err)
		}
		cfg.Database.URL = k.PG.AdminURL()
	}
	k.Admin = infra.Admin{URL: cfg.Database.URL}
	if err := k.Admin.Ping(ctx); err != nil {
		return nil, fmt.Errorf("postgres at %s: %w", redact(cfg.Database.URL), err)
	}
	switch cfg.Sandbox.Driver {
	case "bwrap":
		if !sandbox.BwrapAvailable() {
			return nil, errors.New("bubblewrap is not available; run me in my container (`seed run`), or set sandbox.driver: local (no isolation)")
		}
		d := &sandbox.BwrapDriver{CacheDir: filepath.Join(stateDir, "cache"), Hide: []string{cfg.Root}}
		if k.PG != nil {
			d.SocketDir = k.PG.SocketDir
		}
		k.Driver = d
	default:
		slog.Warn("sandbox.driver is local: generated code runs WITHOUT isolation")
		k.Driver = sandbox.LocalDriver{}
	}

	// Memory
	kernelDB := cfg.DBName("seed")
	if err := k.Admin.EnsureDatabase(ctx, kernelDB, ""); err != nil {
		return nil, fmt.Errorf("kernel database: %w", err)
	}
	k.Store, err = memory.Open(ctx, k.Admin.DatabaseURL(kernelDB, "", "", ""))
	if err != nil {
		return nil, fmt.Errorf("opening memory: %w", err)
	}
	if err := k.bootstrapGenerations(ctx); err != nil {
		return nil, err
	}

	// The organism's own database identity (least privilege: it cannot see kernel memory).
	role := cfg.DBName("organism")
	pass, err := k.Store.Setting(ctx, "organism_db_password")
	if err != nil {
		pass = infra.RandomSecret(16)
		if err := k.Store.SetSetting(ctx, "organism_db_password", pass); err != nil {
			return nil, err
		}
	}
	if err := k.Admin.EnsureRole(ctx, role, pass); err != nil {
		return nil, fmt.Errorf("organism role: %w", err)
	}
	if err := k.Admin.EnsureDatabase(ctx, cfg.DBName("app"), role); err != nil {
		return nil, fmt.Errorf("organism database: %w", err)
	}
	// Evolutions get their own role: it owns only scratch databases and cannot
	// connect to the live one, so an experiment can never touch real data.
	evoRole := cfg.DBName("evolver")
	evoPass, err := k.Store.Setting(ctx, "evolution_db_password")
	if err != nil {
		evoPass = infra.RandomSecret(16)
		if err := k.Store.SetSetting(ctx, "evolution_db_password", evoPass); err != nil {
			return nil, err
		}
	}
	if err := k.Admin.EnsureRole(ctx, evoRole, evoPass); err != nil {
		return nil, fmt.Errorf("evolution role: %w", err)
	}
	// A read-only role for answering questions about live data: it can read
	// the live database and nothing else, whatever SQL it is given.
	readRole := cfg.DBName("reader")
	readPass, err := k.Store.Setting(ctx, "reader_db_password")
	if err != nil {
		readPass = infra.RandomSecret(16)
		if err := k.Store.SetSetting(ctx, "reader_db_password", readPass); err != nil {
			return nil, err
		}
	}
	if err := k.Admin.EnsureReader(ctx, readRole, readPass, cfg.DBName("app")); err != nil {
		return nil, fmt.Errorf("reader role: %w", err)
	}

	// Mind: the model this Seed thinks with, chosen by its owner.
	k.Mind = models.NewSwitchable()
	k.loadMind(ctx)
	if info := k.Mind.Info(); info.Configured {
		slog.Info("I can think", "provider", info.Provider, "model", info.Name)
	} else {
		slog.Warn("I have no brain: start me with `seed run -e OPENROUTER_API_KEY` (or lend me a key in my control plane)")
	}

	k.Organism = &Organism{Cfg: cfg, Driver: k.Driver, Admin: k.Admin, Role: role, Pass: pass, Repo: k.Repo, Bus: k.Bus,
		ReaderRole: readRole, ReaderPass: readPass}
	k.Approvals = &evolution.Approvals{Store: k.Store, Bus: k.Bus}
	k.Orch = &evolution.Orchestrator{
		Cfg: cfg, Store: k.Store, Bus: k.Bus, Repo: k.Repo, Model: k.Mind, Driver: k.Driver,
		Admin: k.Admin, Policy: k.Policy, Approvals: k.Approvals, Live: k.Organism,
		DB:              evolution.DBCreds{Role: evoRole, Password: evoPass},
		OnKernelChanged: k.requestRestart,
	}
	k.Chat = &Chat{Root: cfg.Root, Store: k.Store, Bus: k.Bus, Model: k.Mind, Orch: k.Orch, Repo: k.Repo, Policy: k.Policy,
		Live: k.Organism, Approvals: k.Approvals}
	return k, nil
}

func (k *Kernel) requestRestart() {
	select {
	case k.restart <- struct{}{}:
	default:
	}
}

var genTrailer = regexp.MustCompile(`^(?:\d+\s*->\s*)?(\d+)$`)

// bootstrapGenerations rebuilds the generation table from git history when
// memory is empty (a fresh Seed, or a lost database): Git is the source of truth.
func (k *Kernel) bootstrapGenerations(ctx context.Context) error {
	if _, err := k.Store.CurrentGeneration(ctx); err == nil {
		return nil
	}
	commits, err := k.Repo.Log(ctx, "HEAD", 1000)
	if err != nil {
		return err
	}
	found := 0
	for i := len(commits) - 1; i >= 0; i-- {
		c := commits[i]
		t := gitTrailers(c.Body)
		m := genTrailer.FindStringSubmatch(t["Generation"])
		if m == nil {
			continue
		}
		n, _ := strconv.Atoi(m[1])
		parent := ""
		if i+1 < len(commits) {
			parent = commits[i+1].Hash
		}
		title := c.Subject
		if _, rest, ok := strings.Cut(title, ": "); ok {
			title = rest
		}
		if err := k.Store.AddGeneration(ctx, &memory.Generation{Number: n, Title: upperFirst(title), Commit: c.Hash,
			ParentCommit: parent, EvolutionID: t["Evolution"]}); err != nil {
			return err
		}
		found++
	}
	if found == 0 {
		head, _ := k.Repo.Head(ctx)
		return k.Store.AddGeneration(ctx, &memory.Generation{Number: 1, Title: "Initial seed", Commit: head})
	}
	return nil
}

func gitTrailers(body string) map[string]string { return git.Trailers(body) }

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// Serve runs the kernel until ctx ends or a restart is requested.
func (k *Kernel) Serve(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := k.Orch.Recover(ctx); err != nil {
		slog.Error("recovering evolutions", "err", err)
	}
	go k.Orch.Run(ctx)
	go func() {
		if err := k.Organism.EnsureRunning(ctx); err != nil {
			slog.Error("starting organism", "err", err)
		}
	}()

	ln, err := listen(k.Cfg.Server.Addr, os.Getenv("SEED_ADDR") == "")
	if err != nil {
		return err
	}
	k.Cfg.Server.Addr = ln.Addr().String()
	if err := os.WriteFile(AddrPath(k.Cfg.Root), []byte(k.Cfg.Server.Addr), 0o600); err != nil {
		return err
	}
	srv := &http.Server{Handler: k.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	slog.Info(knowledge.Name(k.Cfg.Root)+" is alive", "slug", k.Cfg.Name, "control_plane", "http://"+k.Cfg.Server.Addr+"/_seed/", "model", k.Mind.Info().Provider+"/"+k.Mind.Info().Name)

	var result error
	select {
	case <-ctx.Done():
	case err := <-errc:
		result = err
	case <-k.restart:
		result = ErrRestart
	}
	shutdownCtx, c2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer c2()
	_ = srv.Shutdown(shutdownCtx)
	cancel()
	if !errors.Is(result, ErrRestart) {
		k.Organism.Stop(shutdownCtx)
	}
	k.Store.Close()
	k.PG.Stop()
	return result
}

func redact(u string) string {
	if i := strings.Index(u, "@"); i >= 0 {
		if j := strings.Index(u, "://"); j >= 0 && j < i {
			return u[:j+3] + "***" + u[i:]
		}
	}
	return u
}

// ControlTokenPath is where the CLI finds the running kernel's token. .seed/
// is hidden from every sandbox.
func ControlTokenPath(root string) string { return filepath.Join(root, ".seed", "control-token") }

func WriteControlToken(root, token string) error {
	p := ControlTokenPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(token), 0o600)
}

// AddrPath records where the running kernel listens (for the CLI).
func AddrPath(root string) string { return filepath.Join(root, ".seed", "addr") }

// listen binds addr. Unless the address was given explicitly, a busy port
// moves on to the next free one, so several Seeds can run side by side.
func listen(addr string, flexible bool) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil || !flexible {
		return ln, err
	}
	host, portStr, splitErr := net.SplitHostPort(addr)
	port, convErr := strconv.Atoi(portStr)
	if splitErr != nil || convErr != nil {
		return nil, err
	}
	for p := port + 1; p <= port+50; p++ {
		if ln, err2 := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p))); err2 == nil {
			slog.Info("port busy; using the next free one", "wanted", addr, "port", p)
			return ln, nil
		}
	}
	return nil, err
}
