package evolution

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"seed/kernel/ids"
	"seed/kernel/memory"
	"seed/kernel/migrate"
	"seed/kernel/sandbox"
	"seed/kernel/tools"
)

// Previewing: before a generation goes live, its owner can try it. The
// candidate runs in its evolution's sandbox against a copy of the live data
// (changes made while trying it are thrown away), and the evolution waits at
// Ready for the owner's decision: apply it, ask for changes (back to
// mutating, with the owner's words), or discard it.

// Decision is what the owner decided after previewing.
type Decision struct {
	Action   string // apply, changes, discard
	Feedback string // what to change (changes)
}

var errDiscarded = errors.New("discarded by my owner after previewing")

// maxPreviewCopy is the largest live database copied for a preview; bigger
// ones are previewed with fresh data (migrations only).
const maxPreviewCopy = 512 << 20

type previewRun struct {
	sb      sandbox.Sandbox
	db      string
	ch      chan Decision
	decided bool
}

// wantsPreview reports whether e waits for its owner at Ready.
func (o *Orchestrator) wantsPreview(ctx context.Context, e *memory.Evolution) bool {
	if e.Kind != "evolve" || (e.Preview != nil && e.Preview.Skip) || o.PreviewOn == nil {
		return false
	}
	return o.PreviewOn(ctx)
}

// RequestWithoutPreview records an evolution that goes live without waiting
// for its owner (e.g. a fix I start on my own).
func (o *Orchestrator) RequestWithoutPreview(ctx context.Context, conv, intent string) (*memory.Evolution, error) {
	e, err := o.Request(ctx, conv, intent)
	if err != nil {
		return nil, err
	}
	e.Preview = &memory.Preview{Skip: true}
	return e, o.save(ctx, e)
}

// previewAndWait starts the candidate for its owner and waits for a decision.
func (o *Orchestrator) previewAndWait(ctx context.Context, e *memory.Evolution, ws *workspace) (Decision, error) {
	o.init()
	if e.Preview == nil {
		e.Preview = &memory.Preview{}
	}
	e.Preview.State, e.Preview.Note = "starting", ""
	_ = o.save(ctx, e)
	// Nothing of the evolution runs during a preview: closing its sandbox
	// ends anything left running there (it has open network, and must never
	// sit beside the preview of live data). A new one is made if my owner
	// asks for changes.
	if ws.sb != nil {
		_ = ws.sb.Close(ctx)
		ws.sb, ws.env.Sandbox = nil, nil
	}
	run := &previewRun{ch: make(chan Decision, 1)}
	data, err := o.startPreview(ctx, e, ws, run)
	e.Preview.Data = data
	if err != nil {
		e.Preview.State, e.Preview.Note = "failed", "I couldn't start the preview: "+firstLine(err.Error())
		slog.Warn("preview", "evolution", e.ID, "err", err)
	} else {
		e.Preview.State = "ready"
	}
	_ = o.save(ctx, e)
	o.mu.Lock()
	o.previews[e.ID] = run
	o.mu.Unlock()
	defer o.stopPreview(context.WithoutCancel(ctx), e.ID)
	if err == nil {
		o.event(ctx, e, "note", "Ready to try before it goes live", nil)
		o.say(ctx, e, fmt.Sprintf("**%s** is ready to try before it goes live. **Try it** on its card, then **Apply** it, ask me for changes, or discard it.", titleOr(e)))
	} else {
		o.say(ctx, e, fmt.Sprintf("**%s** passed its checks, but I couldn't start a preview (%s). You can still apply it or discard it from its card.", titleOr(e), firstLine(err.Error())))
	}
	select {
	case <-ctx.Done():
		return Decision{}, ctx.Err()
	case d := <-run.ch:
		e.Preview.State = "done"
		_ = o.save(ctx, e)
		if d.Action == "changes" {
			o.stopPreview(context.WithoutCancel(ctx), e.ID) // before the evolution works again
			sb, err := o.evolutionSandbox(ctx, ws)
			if err != nil {
				return d, fmt.Errorf("a new sandbox for the changes: %w", err)
			}
			ws.sb, ws.env.Sandbox = sb, sb
		}
		return d, nil
	}
}

// startPreview runs the candidate on a copy of the live data, under the
// same rules as my live organism: its own database role (a fresh password,
// given only to the preview process, so no evolution sandbox can reach the
// copy), a private network whose only way out is the egress proxy, and no
// secrets. It returns what data the preview has ("copy" or "fresh").
func (o *Orchestrator) startPreview(ctx context.Context, e *memory.Evolution, ws *workspace, run *previewRun) (string, error) {
	role, pass := o.Cfg.DBName("preview"), randomPassword()
	if err := o.Admin.EnsureRole(ctx, role, pass); err != nil {
		return "", err
	}
	run.db = ws.dbName + "_pv"
	_ = o.Admin.DropDatabase(ctx, run.db)
	if err := o.Admin.EnsureDatabase(ctx, run.db, role); err != nil {
		return "", err
	}
	hostURL := o.Admin.DatabaseURL(run.db, role, pass, "")
	data := "fresh"
	if o.Live != nil {
		if err := copyDatabase(ctx, o.Live.DatabaseURL(), hostURL); err == nil {
			data = "copy"
		} else {
			slog.Info("previewing with fresh data", "evolution", e.ID, "why", err)
			// Start over clean: a partial copy is worse than none.
			_ = o.Admin.DropDatabase(ctx, run.db)
			if err := o.Admin.EnsureDatabase(ctx, run.db, role); err != nil {
				return "", err
			}
		}
	}
	if _, err := migrate.ApplyRepo(ctx, hostURL, tools.OrganismMigrationsTable, ws.dir, o.Cfg.Organism.Migrations); err != nil {
		return data, fmt.Errorf("migrating the preview's data: %w", err)
	}
	bwrap := o.Cfg.Sandbox.Driver == "bwrap"
	dbHost := ""
	if o.Cfg.Sandbox.Driver != "local" {
		dbHost = o.Cfg.Sandbox.DBHost
	}
	spec := sandbox.Spec{
		Name: o.Cfg.ContainerName("preview-" + strings.ToLower(ids.Short(e.ID, 8))), Root: ws.dir,
		// Read-only: what the preview writes (from the live data's copy) must
		// never land in the workspace the evolution works in. Its own /tmp
		// is private and thrown away.
		Writable: nil, Hidden: []string{".seed"}, Port: o.Cfg.Organism.Port,
		PrivateNetwork: bwrap,
		Env:            map[string]string{"SEED_ENV": "preview"},
		Labels:         map[string]string{"seed.name": o.Cfg.Name, "seed.role": "preview"},
	}
	if bwrap {
		spec.Egress = o.EgressSocket
	}
	sb, err := o.Driver.Create(ctx, spec)
	if err != nil {
		return data, err
	}
	run.sb = sb
	// The preview's database password and way out go to its process only.
	procEnv := map[string]string{"DATABASE_URL": o.Admin.DatabaseURL(run.db, role, pass, dbHost)}
	if bwrap && o.EgressSocket != "" && o.PreviewEnv != nil {
		for k, v := range o.PreviewEnv() {
			procEnv[k] = v
		}
	}
	env := &tools.Env{Sandbox: sb, Root: ws.dir, RunCommand: o.Cfg.Organism.Run, HealthPath: o.Cfg.Organism.Health, SandboxEnv: procEnv}
	if _, err := tools.StartApp(ctx, env, 90*time.Second); err != nil {
		return data, err
	}
	return data, nil
}

func randomPassword() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// copyDatabase copies the live database into the preview one (pg_dump into
// psql, owned by the preview's role). Large databases aren't copied.
func copyDatabase(ctx context.Context, from, to string) error {
	if _, err := exec.LookPath("pg_dump"); err != nil {
		return errors.New("pg_dump isn't available")
	}
	var size int64
	sizeOut, err := exec.CommandContext(ctx, "psql", "-XAtq", from, "-c", "SELECT pg_database_size(current_database())").Output()
	if err == nil {
		fmt.Sscan(strings.TrimSpace(string(sizeOut)), &size)
	}
	if size > maxPreviewCopy {
		return fmt.Errorf("the live data is too large to copy (%d MB)", size>>20)
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	dump := exec.CommandContext(ctx, "pg_dump", "--no-owner", "--no-privileges", "--no-comments", from)
	load := exec.CommandContext(ctx, "psql", "-Xq", "-v", "ON_ERROR_STOP=1", to)
	pipe, err := dump.StdoutPipe()
	if err != nil {
		return err
	}
	load.Stdin = pipe
	var dumpErr, loadErr strings.Builder
	dump.Stderr, load.Stderr = &dumpErr, &loadErr
	load.Stdout = io.Discard
	if err := dump.Start(); err != nil {
		return err
	}
	if err := load.Run(); err != nil {
		_ = dump.Process.Kill()
		_ = dump.Wait()
		return fmt.Errorf("loading the copy: %v: %s", err, firstLine(loadErr.String()))
	}
	if err := dump.Wait(); err != nil {
		return fmt.Errorf("copying: %v: %s", err, firstLine(dumpErr.String()))
	}
	return nil
}

func (o *Orchestrator) stopPreview(ctx context.Context, id string) {
	o.mu.Lock()
	run := o.previews[id]
	delete(o.previews, id)
	o.mu.Unlock()
	if run == nil {
		return
	}
	if run.sb != nil {
		_ = run.sb.Close(ctx)
	}
	if run.db != "" {
		_ = o.Admin.DropDatabase(ctx, run.db)
	}
}

// PreviewSandbox is where an evolution's preview runs, if it's being previewed.
func (o *Orchestrator) PreviewSandbox(id string) (sandbox.Sandbox, bool) {
	o.init()
	o.mu.Lock()
	defer o.mu.Unlock()
	run := o.previews[id]
	if run == nil || run.sb == nil || run.decided {
		return nil, false
	}
	return run.sb, true
}

// Decide delivers the owner's decision about a previewed evolution.
func (o *Orchestrator) Decide(id string, d Decision) error {
	o.init()
	d.Feedback = strings.TrimSpace(d.Feedback)
	switch d.Action {
	case "apply", "discard":
	case "changes":
		if d.Feedback == "" {
			return errors.New("say what to change")
		}
		if len(d.Feedback) > 4000 {
			return errors.New("keep it under 4000 characters")
		}
	default:
		return errors.New("action must be apply, changes or discard")
	}
	o.mu.Lock()
	run := o.previews[id]
	if run == nil || run.decided {
		o.mu.Unlock()
		return fmt.Errorf("evolution %s isn't waiting for you", id)
	}
	run.decided = true
	o.mu.Unlock()
	run.ch <- d // buffered: never blocks
	return nil
}
