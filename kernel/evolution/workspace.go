package evolution

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"seed/kernel/git"
	"seed/kernel/ids"
	"seed/kernel/memory"
	"seed/kernel/migrate"
	"seed/kernel/sandbox"
	"seed/kernel/tools"
)

// workspace is one evolution's isolated environment: a git worktree on its
// own branch, a scratch database and a sandbox container.
type workspace struct {
	o        *Orchestrator
	e        *memory.Evolution
	dir      string
	repo     *git.Repo
	dbName   string
	sb       sandbox.Sandbox
	env      *tools.Env
	approver *approvalRecorder
}

func (o *Orchestrator) prepareWorkspace(ctx context.Context, e *memory.Evolution) (*workspace, error) {
	short := strings.ToLower(ids.Short(e.ID, 8))
	ws := &workspace{o: o, e: e, dir: filepath.Join(o.workspacesDir(), e.ID), dbName: o.Cfg.DBName("evo_" + short)}
	if err := ensureDir(o.workspacesDir()); err != nil {
		return nil, err
	}
	e.Branch = "seed/" + e.ID
	e.Worktree = ws.dir
	if err := o.Repo.AddWorktree(ctx, ws.dir, e.Branch, e.BaseCommit); err != nil {
		return nil, err
	}
	ws.repo = o.Repo.WithDir(ws.dir)
	_ = o.save(ctx, e)
	o.event(ctx, e, "note", "Created workspace on branch "+e.Branch, map[string]string{"worktree": ws.dir, "base": e.BaseCommit})

	if err := o.Admin.EnsureDatabase(ctx, ws.dbName, o.DB.Role); err != nil {
		ws.close(ctx, false)
		return nil, fmt.Errorf("scratch database: %w", err)
	}
	dbHost := ""
	if o.Cfg.Sandbox.Driver == "docker" {
		dbHost = o.Cfg.Sandbox.DBHost
	}
	sandboxDB := o.Admin.DatabaseURL(ws.dbName, o.DB.Role, o.DB.Password, dbHost)
	hostDB := o.Admin.DatabaseURL(ws.dbName, o.DB.Role, o.DB.Password, "")

	var writable []string
	for _, p := range o.Policy.Evolvable {
		writable = append(writable, strings.Trim(p, "/"))
	}
	env := map[string]string{"DATABASE_URL": sandboxDB, "SEED_ENV": "evolution"}
	sb, err := o.Driver.Create(ctx, sandbox.Spec{
		Name: o.Cfg.ContainerName("evo-" + short), Root: ws.dir,
		Writable: writable, Hidden: []string{".seed"},
		Port: o.Cfg.Organism.Port, Env: env,
		Labels: map[string]string{"seed.name": o.Cfg.Name, "seed.evolution": e.ID},
	})
	if err != nil {
		ws.close(ctx, false)
		return nil, fmt.Errorf("sandbox: %w", err)
	}
	ws.sb = sb
	timeout := time.Duration(o.Cfg.Evolution.CommandTimeoutSeconds) * time.Second
	ws.env = &tools.Env{
		Sandbox: sb, Root: ws.dir, DBURL: hostDB, SandboxEnv: map[string]string{"DATABASE_URL": sandboxDB},
		MigrationsDir: o.Cfg.Organism.Migrations, RunCommand: o.Cfg.Organism.Run, HealthPath: o.Cfg.Organism.Health,
		CommandTimeout: 5 * time.Minute, MaxCommandTimeout: timeout,
	}
	ws.approver = o.evolutionApprover(e)
	o.event(ctx, e, "note", "Started sandbox and scratch database", map[string]string{"database": ws.dbName, "isolated": fmt.Sprint(o.Driver.Isolated())})
	return ws, nil
}

func (ws *workspace) migrate(ctx context.Context) (migrate.Result, error) {
	return migrate.ApplyRepo(ctx, ws.env.DBURL, tools.OrganismMigrationsTable, ws.dir, ws.o.Cfg.Organism.Migrations)
}

// resetDB recreates the scratch database empty.
func (ws *workspace) resetDB(ctx context.Context) error {
	if err := ws.o.Admin.DropDatabase(ctx, ws.dbName); err != nil {
		return err
	}
	return ws.o.Admin.EnsureDatabase(ctx, ws.dbName, ws.o.DB.Role)
}

// close tears the workspace down. On success the worktree and branch are
// removed (the commit lives on main); on failure the worktree is kept so the
// owner (or a future evolution) can inspect what was attempted.
func (ws *workspace) close(ctx context.Context, success bool) {
	if ws.sb != nil {
		if err := ws.sb.Close(ctx); err != nil {
			slog.Warn("close sandbox", "err", err)
		}
	}
	if err := ws.o.Admin.DropDatabase(ctx, ws.dbName); err != nil {
		slog.Warn("drop scratch database", "db", ws.dbName, "err", err)
	}
	if success {
		if err := ws.o.Repo.RemoveWorktree(ctx, ws.dir); err != nil {
			slog.Warn("remove worktree", "err", err)
		}
		_ = ws.o.Repo.DeleteBranch(ctx, ws.e.Branch)
	}
}

// cleanupScratch removes leftovers of an interrupted evolution (not the worktree).
func (o *Orchestrator) cleanupScratch(ctx context.Context, e *memory.Evolution, removeWorktree bool) {
	short := strings.ToLower(ids.Short(e.ID, 8))
	_ = o.Admin.DropDatabase(ctx, o.Cfg.DBName("evo_"+short))
	if o.Cfg.Sandbox.Driver == "docker" {
		_, _ = dockerRm(ctx, o.Cfg.ContainerName("evo-"+short))
	}
	if removeWorktree && e.Worktree != "" {
		_ = o.Repo.RemoveWorktree(ctx, e.Worktree)
	}
}
