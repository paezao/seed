// Package runtime boots a Seed: it connects memory, sandboxing, the model and
// the evolution engine, keeps the live organism running, and serves the
// control plane.
package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"seed/kernel/tools"
	"seed/kernel/update"
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
	// Token authorizes the CLI's control-plane API calls. It is written to
	// .seed/control-token (hidden from sandboxes) and never sent to a browser.
	Token string
	// Owner holds the browsers my owner signed in with (see owner.go).
	Owner *Owner
	// Egress is the live organism's only way out.
	Egress *Egress
	// Routines runs what I do on a schedule.
	Routines *Scheduler
	// Updates brings me new kernels from signed releases.
	Updates *KernelUpdates
	// cookieName is unique per Seed: browsers share cookies across ports, so
	// Seeds on the same machine must not overwrite each other's sessions.
	cookieName string
	// trustedProxies is how many proxies in front of me append to
	// X-Forwarded-For (SEED_TRUSTED_PROXIES; 0 = trust none).
	trustedProxies int
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
	var dbSocketDir string
	if k.PG != nil {
		dbSocketDir = k.PG.SocketDir
	} else {
		// An external server (DATABASE_URL): check I can manage it, and give
		// my sandboxes a socket to it (the live organism has no network).
		if err := k.Admin.CheckPrivileges(ctx); err != nil {
			return nil, err
		}
		if cfg.Sandbox.Driver == "bwrap" {
			dir, err := os.MkdirTemp("", "seed-db-") // my /tmp: sandboxes get their own
			if err != nil {
				return nil, err
			}
			bridge, err := pg.StartBridge(dir, cfg.Database.URL)
			if err != nil {
				return nil, fmt.Errorf("database bridge: %w", err)
			}
			dbSocketDir = bridge.SocketDir
		}
		slog.Info("using an external PostgreSQL", "server", redact(cfg.Database.URL))
	}
	switch cfg.Sandbox.Driver {
	case "bwrap":
		if !sandbox.BwrapAvailable() {
			return nil, errors.New("bubblewrap is not available; run me in my container (`seed run`), or set sandbox.driver: local (no isolation)")
		}
		d := &sandbox.BwrapDriver{CacheDir: filepath.Join(stateDir, "cache"), Hide: []string{cfg.Root}, SocketDir: dbSocketDir}
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
	if k.Owner, err = NewOwner(ctx, k.Store); err != nil {
		return nil, fmt.Errorf("owner sessions: %w", err)
	}
	if err := k.Owner.SetPassword(k.Secrets.Get("SEED_OWNER_USER"), k.Secrets.Get("SEED_OWNER_PASSWORD")); err != nil {
		slog.Warn("password sign-in is off: " + err.Error())
	}
	instance, err := k.Store.Setting(ctx, "instance_id")
	if err != nil {
		instance = randomHex(4)
		if err := k.Store.SetSetting(ctx, "instance_id", instance); err != nil {
			return nil, err
		}
	}
	k.cookieName = "seed_owner_" + instance
	if v := os.Getenv("SEED_TRUSTED_PROXIES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 && n <= 5 {
			k.trustedProxies = n
		} else {
			slog.Warn("SEED_TRUSTED_PROXIES must be a number of proxies (0-5); trusting none")
		}
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
	if err := k.Admin.EnsureReader(ctx, readRole, readPass, cfg.DBName("app"), role); err != nil {
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
	// The organism's only way out (see egress.go), on a socket in my own
	// /tmp (sandboxes get a private /tmp, so only the organism's bind sees it).
	if k.Egress, err = NewEgress(ctx, k.Store); err != nil {
		return nil, fmt.Errorf("outbound access: %w", err)
	}
	egressDir, err := os.MkdirTemp("", "seed-egress-")
	if err != nil {
		return nil, err
	}
	egressSock := filepath.Join(egressDir, "egress.sock")
	egressL, err := net.Listen("unix", egressSock)
	if err != nil {
		return nil, fmt.Errorf("outbound proxy: %w", err)
	}
	_ = os.Chmod(egressSock, 0o600)
	go func() {
		if err := k.Egress.Serve(egressL); err != nil {
			slog.Error("outbound proxy stopped", "err", err)
		}
	}()
	k.Organism.Egress, k.Organism.EgressSocket, k.Organism.Secrets = k.Egress, egressSock, k.Secrets
	k.Egress.OnSecretsChanged = func() {
		go func() {
			if err := k.Organism.Recreate(context.Background()); err != nil {
				slog.Warn("restarting the organism with its new secrets failed", "err", err)
			}
		}()
	}
	k.Approvals = &evolution.Approvals{Store: k.Store, Bus: k.Bus}
	k.Orch = &evolution.Orchestrator{
		Cfg: cfg, Store: k.Store, Bus: k.Bus, Repo: k.Repo, Model: k.Mind, Driver: k.Driver,
		Admin: k.Admin, Policy: k.Policy, Approvals: k.Approvals, Live: k.Organism,
		DB:              evolution.DBCreds{Role: evoRole, Password: evoPass},
		OnKernelChanged: k.requestRestart,
		ExtraTools:      func() []*tools.Tool { return []*tools.Tool{k.Egress.RequestTool()} },
		ExtraContext: func(ctx context.Context) string {
			return k.Egress.Describe(k.Secrets.Names()) + k.Routines.Describe(ctx)
		},
	}
	k.Chat = &Chat{Root: cfg.Root, Store: k.Store, Bus: k.Bus, Model: k.Mind, Orch: k.Orch, Repo: k.Repo, Policy: k.Policy,
		Live: k.Organism, Approvals: k.Approvals, Egress: k.Egress}
	k.Routines = &Scheduler{Root: cfg.Root, Store: k.Store, Bus: k.Bus, Agent: k.Chat, Jobs: k.Organism}
	k.Chat.Routines = k.Routines
	k.Organism.JobToken = randomHex(24)
	k.Updates = &KernelUpdates{Root: cfg.Root, Store: k.Store, Bus: k.Bus, Restart: k.requestRestart,
		Busy: func() bool { return k.Orch.Active(context.Background()) != nil }}
	if os.Getenv("SEED_UPDATES") != "off" {
		source := update.DefaultSource
		if v := firstNonEmpty(os.Getenv("SEED_RELEASES_URL"), k.Secrets.Get("SEED_RELEASES_URL")); v != "" {
			source = v // a mirror: releases must still be signed by a key I carry
		}
		k.Updates.Client = &update.Client{Source: source, Keys: update.TrustedKeys}
	}
	k.Organism.AfterDeploy = func() { k.Routines.SyncJobs(context.Background()) }
	return k, nil
}

func (k *Kernel) requestRestart() {
	select {
	case k.restart <- struct{}{}:
	default:
	}
}

var genTrailer = regexp.MustCompile(`^(?:\d+\s*->\s*)?(\d+)$`)

// bootstrapGenerations brings the generation table up to date with git
// history: Git is the source of truth. It rebuilds the table for a fresh
// Seed (or a lost database), and records generations committed while I was
// stopped (e.g. by `seed upgrade`).
func (k *Kernel) bootstrapGenerations(ctx context.Context) error {
	known := 0
	if g, err := k.Store.CurrentGeneration(ctx); err == nil {
		known = g.Number
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
		found++
		n, _ := strconv.Atoi(m[1])
		if n <= known {
			continue
		}
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
		known = n
	}
	if found == 0 && known == 0 {
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
	go k.Routines.Start(ctx)
	go k.Updates.Start(ctx)
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
	// Running natively (--native), tell the CLI where I listen. In a
	// container the CLI asks the engine instead, so nothing is written.
	if os.Getenv("SEED_IN_CONTAINER") != "1" {
		if p, err := NativeAddrPath(k.Cfg.Root); err == nil {
			_ = os.MkdirAll(filepath.Dir(p), 0o700)
			_ = os.WriteFile(p, []byte(k.Cfg.Server.Addr), 0o600)
		}
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
	p, err := url.Parse(u)
	if err != nil || p.Scheme == "" {
		return "(database URL)"
	}
	if p.User != nil {
		p.User = url.User(p.User.Username())
	}
	q := p.Query()
	if q.Has("password") {
		q.Set("password", "***")
		p.RawQuery = q.Encode()
	}
	return p.String()
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

// NativeAddrPath is where a natively running kernel records its address for
// the CLI: in the owner's cache directory, keyed by the Seed's path, never in
// the Seed's folder (which the Seed controls, so it could point the CLI at
// another local service).
func NativeAddrPath(root string) (string, error) {
	dir, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(root))
	return filepath.Join(dir, "seed", "native", hex.EncodeToString(sum[:8])+".addr"), nil
}

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
