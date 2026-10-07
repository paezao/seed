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
	"seed/kernel/sandbox"
)

// ErrRestart asks the launcher to rebuild and restart the kernel (after an
// evolution changed kernel code).
var ErrRestart = errors.New("kernel restart requested")

// RestartExitCode is the process exit code that signals ErrRestart.
const RestartExitCode = 75

type Kernel struct {
	Cfg          *config.Config
	Store        *memory.Store
	Bus          *events.Bus
	Repo         *git.Repo
	Model        models.Model
	ModelInfo    models.Info
	Driver       sandbox.Driver
	SandboxImage string
	Admin        infra.Admin
	Policy       *permissions.Policy
	Approvals    *evolution.Approvals
	Orch         *evolution.Orchestrator
	Organism     *Organism
	Chat         *Chat
	Logs         *LogBuffer

	restart chan struct{}
}

// Boot assembles a kernel for the Seed at root.
func Boot(ctx context.Context, root string, logs *LogBuffer) (*Kernel, error) {
	cfg, err := config.Load(root)
	if err != nil {
		return nil, err
	}
	k := &Kernel{Cfg: cfg, Bus: events.NewBus(), Logs: logs, restart: make(chan struct{}, 1)}
	k.Repo = git.Open(cfg.Root)
	if _, err := k.Repo.Head(ctx); err != nil {
		return nil, fmt.Errorf("%s is not a git repository with history (create Seeds with `seed new`): %w", cfg.Root, err)
	}
	if roots, err := k.Repo.Run(ctx, "rev-list", "--max-parents=0", "HEAD"); err == nil && len(roots) >= 8 {
		lines := strings.Split(roots, "\n")
		cfg.Instance = lines[len(lines)-1][:8]
	}
	k.Policy = permissions.NewPolicy(cfg.Permissions.Safe, cfg.Permissions.Review, cfg.Permissions.Dangerous, cfg.Kernel.Protected)

	// Infrastructure
	k.Admin = infra.Admin{URL: cfg.Database.URL}
	if cfg.Sandbox.Driver == "docker" {
		if !infra.DockerAvailable(ctx) {
			return nil, errors.New("docker is not available; Seeds run generated code in Docker sandboxes (or set sandbox.driver: local, without isolation)")
		}
		if err := infra.EnsurePostgres(ctx, cfg.Sandbox.Network, k.Admin); err != nil {
			return nil, fmt.Errorf("postgres: %w", err)
		}
		if err := infra.EnsureNetwork(ctx, cfg.Sandbox.Network); err != nil {
			return nil, err
		}
		ref, err := infra.EnsureImage(ctx, cfg.Sandbox.Image, filepath.Join(cfg.Root, "Dockerfile"))
		if err != nil {
			return nil, fmt.Errorf("sandbox image: %w", err)
		}
		k.SandboxImage = ref
		cache, _ := os.UserCacheDir()
		k.Driver = &sandbox.DockerDriver{Image: ref, Network: cfg.Sandbox.Network, Memory: cfg.Sandbox.Memory,
			CPUs: cfg.Sandbox.CPUs, CacheDir: filepath.Join(cache, "seed")}
	} else {
		slog.Warn("sandbox.driver is local: generated code runs on the host WITHOUT isolation")
		if err := k.Admin.Ping(ctx); err != nil {
			return nil, fmt.Errorf("postgres at %s: %w", redact(cfg.Database.URL), err)
		}
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

	// Mind
	k.Model, k.ModelInfo = models.New(models.Settings{
		Provider: cfg.Model.Provider, Name: cfg.Model.Name, BaseURL: cfg.Model.BaseURL,
		APIKeyEnv: cfg.Model.APIKeyEnv, MaxTokens: cfg.Model.MaxTokens,
	})
	if !k.ModelInfo.Configured {
		slog.Warn("no model API key configured; I can boot but cannot think", "provider", k.ModelInfo.Provider)
	}

	k.Organism = &Organism{Cfg: cfg, Driver: k.Driver, Admin: k.Admin, Role: role, Pass: pass, Repo: k.Repo, Bus: k.Bus}
	k.Approvals = &evolution.Approvals{Store: k.Store, Bus: k.Bus}
	k.Orch = &evolution.Orchestrator{
		Cfg: cfg, Store: k.Store, Bus: k.Bus, Repo: k.Repo, Model: k.Model, Driver: k.Driver,
		Admin: k.Admin, Policy: k.Policy, Approvals: k.Approvals, Live: k.Organism,
		DB:              evolution.DBCreds{Role: role, Password: pass},
		OnKernelChanged: k.requestRestart,
	}
	k.Chat = &Chat{Root: cfg.Root, Store: k.Store, Bus: k.Bus, Model: k.Model, Orch: k.Orch, Repo: k.Repo, Policy: k.Policy}
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

	ln, err := net.Listen("tcp", k.Cfg.Server.Addr)
	if err != nil {
		return err
	}
	srv := &http.Server{Handler: k.Handler(), ReadHeaderTimeout: 10 * time.Second}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	slog.Info(knowledge.Name(k.Cfg.Root)+" is alive", "slug", k.Cfg.Name, "control_plane", "http://"+k.Cfg.Server.Addr+"/_seed/", "model", k.ModelInfo.Provider+"/"+k.ModelInfo.Name)

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
