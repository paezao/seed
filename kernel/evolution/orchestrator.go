// Package evolution turns intent into a new generation.
//
//	requested → planning → planned → mutating → building → testing → running
//	  → observing → reflecting → ready → applying → complete
//
// Every evolution happens in an isolated git worktree and Docker sandbox with
// its own scratch database. The live Seed is only touched in the final
// "applying" step, by fast-forwarding main to a verified commit. If the new
// generation fails to come up, the previous one is restored.
package evolution

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"seed/kernel/agent"
	"seed/kernel/config"
	"seed/kernel/events"
	"seed/kernel/git"
	"seed/kernel/infra"
	"seed/kernel/knowledge"
	"seed/kernel/memory"
	"seed/kernel/models"
	"seed/kernel/permissions"
	"seed/kernel/sandbox"
	"seed/kernel/skills"
	"seed/kernel/tools"
)

// Live is the running generation of the Seed.
type Live interface {
	// Deploy builds the organism from the main checkout, migrates the live
	// database and restarts it, returning an error if it does not come up healthy.
	Deploy(ctx context.Context) error
	// DatabaseURL is the host-side URL of the live organism database.
	DatabaseURL() string
}

// DBCreds are the organism's PostgreSQL credentials.
type DBCreds struct {
	Role, Password string
}

type Orchestrator struct {
	Cfg       *config.Config
	Store     *memory.Store
	Bus       *events.Bus
	Repo      *git.Repo
	Model     models.Model
	Driver    sandbox.Driver
	Admin     infra.Admin
	Policy    *permissions.Policy
	Approvals *Approvals
	Live      Live
	DB        DBCreds
	// OnKernelChanged is called after applying a generation that changed
	// kernel files (the running kernel binary is now stale).
	OnKernelChanged func()

	mu      sync.Mutex
	wake    chan struct{}
	cancels map[string]context.CancelFunc
	running string
}

func (o *Orchestrator) init() {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.wake == nil {
		o.wake = make(chan struct{}, 1)
		o.cancels = map[string]context.CancelFunc{}
	}
}

// Request records a new evolution and wakes the worker.
func (o *Orchestrator) Request(ctx context.Context, conv, intent string) (*memory.Evolution, error) {
	o.init()
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return nil, errors.New("intent is empty")
	}
	base := 0
	if g, err := o.Store.CurrentGeneration(ctx); err == nil {
		base = g.Number
	}
	e := &memory.Evolution{ConversationID: conv, Intent: intent, BaseGeneration: base, Kind: "evolve"}
	if err := o.Store.CreateEvolution(ctx, e); err != nil {
		return nil, err
	}
	o.Bus.Publish("evolution", e)
	o.kick()
	return e, nil
}

// RequestRollback records a rollback to generation n.
func (o *Orchestrator) RequestRollback(ctx context.Context, conv string, n int) (*memory.Evolution, error) {
	o.init()
	target, err := o.Store.Generation(ctx, n)
	if err != nil {
		return nil, fmt.Errorf("generation %d: %w", n, err)
	}
	cur, err := o.Store.CurrentGeneration(ctx)
	if err != nil {
		return nil, err
	}
	if cur.Number == n {
		return nil, fmt.Errorf("generation %d is already current", n)
	}
	e := &memory.Evolution{ConversationID: conv, Kind: "rollback", BaseGeneration: cur.Number, TargetGeneration: &n,
		Intent: fmt.Sprintf("Roll back to generation %d (%s)", n, target.Title), Title: fmt.Sprintf("Roll back to generation %d", n)}
	if err := o.Store.CreateEvolution(ctx, e); err != nil {
		return nil, err
	}
	o.Bus.Publish("evolution", e)
	o.kick()
	return e, nil
}

// Cancel stops a queued or running evolution.
func (o *Orchestrator) Cancel(ctx context.Context, id string) (*memory.Evolution, error) {
	o.init()
	e, err := o.Store.Evolution(ctx, id)
	if err != nil {
		return nil, err
	}
	if e.Status.Terminal() {
		return e, nil
	}
	o.mu.Lock()
	cancel := o.cancels[id]
	o.mu.Unlock()
	if cancel != nil {
		cancel()
		return e, nil // the worker records the cancellation
	}
	if e.Status == memory.Applying {
		return nil, errors.New("cannot cancel while applying")
	}
	_ = e.Transition(memory.Cancelled)
	now := time.Now()
	e.CompletedAt = &now
	return e, o.save(ctx, e)
}

// Active returns the evolution currently being worked on, if any.
func (o *Orchestrator) Active(ctx context.Context) *memory.Evolution {
	o.mu.Lock()
	id := o.running
	o.mu.Unlock()
	if id == "" {
		return nil
	}
	e, err := o.Store.Evolution(ctx, id)
	if err != nil {
		return nil
	}
	return e
}

func (o *Orchestrator) kick() {
	select {
	case o.wake <- struct{}{}:
	default:
	}
}

// Recover reconciles evolutions interrupted by a kernel restart. An
// evolution that was applying is checked against git: if main already points
// at its commit, the generation is completed; otherwise it failed. Anything
// else in progress is marked failed (its worktree is preserved for inspection).
func (o *Orchestrator) Recover(ctx context.Context) error {
	o.init()
	_ = o.Store.ExpireApprovals(ctx)
	active, err := o.Store.EvolutionsByStatus(ctx, memory.ActiveStates()...)
	if err != nil {
		return err
	}
	head, _ := o.Repo.Head(ctx)
	for _, e := range active {
		if e.Status == memory.Requested {
			continue // never started; will run normally
		}
		was := e.Status
		if was == memory.Applying && e.Commit != "" && e.Commit == head {
			if err := o.recordGeneration(ctx, e); err == nil {
				_ = e.Transition(memory.Complete)
				now := time.Now()
				e.CompletedAt = &now
				_ = o.save(ctx, e)
				o.event(ctx, e, "note", "Recovered after restart: the generation had been applied", nil)
				continue
			}
		}
		e.Status = memory.Failed
		e.Error = fmt.Sprintf("interrupted by a kernel restart while %s", was)
		now := time.Now()
		e.CompletedAt = &now
		_ = o.save(ctx, e)
		o.event(ctx, e, "error", e.Error, map[string]string{"worktree": e.Worktree})
		o.say(ctx, e, fmt.Sprintf("I was restarted in the middle of evolving (%s) and could not finish **%s**. Its workspace is preserved; ask me again to retry.", was, titleOr(e)))
		o.cleanupScratch(context.WithoutCancel(ctx), e, false)
	}
	return nil
}

// Run processes requested evolutions one at a time until ctx ends.
func (o *Orchestrator) Run(ctx context.Context) {
	o.init()
	o.kick()
	for {
		select {
		case <-ctx.Done():
			return
		case <-o.wake:
		}
		for ctx.Err() == nil {
			queued, err := o.Store.EvolutionsByStatus(ctx, memory.Requested)
			if err != nil {
				slog.Error("evolution queue", "err", err)
				break
			}
			if len(queued) == 0 {
				break
			}
			o.process(ctx, queued[0])
		}
	}
}

func (o *Orchestrator) process(parent context.Context, e *memory.Evolution) {
	ctx, cancel := context.WithCancel(parent)
	o.mu.Lock()
	o.cancels[e.ID] = cancel
	o.running = e.ID
	o.mu.Unlock()
	defer func() {
		cancel()
		o.mu.Lock()
		delete(o.cancels, e.ID)
		o.running = ""
		o.mu.Unlock()
	}()

	var err error
	if e.Kind == "rollback" {
		err = o.rollback(ctx, e)
	} else {
		err = o.evolve(ctx, e)
	}
	bg := context.WithoutCancel(ctx)
	now := time.Now()
	switch {
	case err == nil:
		return
	case errors.Is(err, context.Canceled) && parent.Err() == nil:
		e.Status = memory.Cancelled
		e.Error = "cancelled by owner"
		e.CompletedAt = &now
		_ = o.save(bg, e)
		o.event(bg, e, "note", "Cancelled", nil)
		o.say(bg, e, fmt.Sprintf("Cancelled **%s**. Nothing about me changed.", titleOr(e)))
	case parent.Err() != nil:
		// Kernel shutting down; Recover will reconcile on next start.
	default:
		if e.Status != memory.RolledBack {
			e.Status = memory.Failed
		}
		e.Error = err.Error()
		e.CompletedAt = &now
		_ = o.save(bg, e)
		o.event(bg, e, "error", firstLine(err.Error()), map[string]string{"error": err.Error()})
		if e.Status == memory.RolledBack {
			o.say(bg, e, fmt.Sprintf("**%s** passed verification but did not come up healthy as the live me, so I rolled back to generation %d.\n\n```\n%s\n```", titleOr(e), e.BaseGeneration, tools.Truncate(err.Error(), 1500)))
		} else {
			o.say(bg, e, fmt.Sprintf("I couldn't complete **%s**. Nothing about the live me changed.\n\n```\n%s\n```", titleOr(e), tools.Truncate(err.Error(), 1500)))
		}
	}
}

// transition moves e to a new state, persists and broadcasts it.
func (o *Orchestrator) transition(ctx context.Context, e *memory.Evolution, to memory.Status) error {
	if err := e.Transition(to); err != nil {
		return err
	}
	if err := o.save(ctx, e); err != nil {
		return err
	}
	o.event(ctx, e, "phase", phaseSummary(to, e), map[string]any{"status": to, "attempt": e.Attempts})
	return nil
}

func phaseSummary(s memory.Status, e *memory.Evolution) string {
	switch s {
	case memory.Planning:
		return "Planning"
	case memory.Planned:
		return "Plan ready"
	case memory.Mutating:
		if e.Attempts > 1 {
			return fmt.Sprintf("Repairing (attempt %d)", e.Attempts)
		}
		return "Evolving"
	case memory.Building:
		return "Building"
	case memory.Testing:
		return "Testing"
	case memory.Running:
		return "Launching"
	case memory.Observing:
		return "Observing"
	case memory.Reflecting:
		return "Reflecting"
	case memory.Ready:
		return "Ready to apply"
	case memory.Applying:
		return "Applying new generation"
	case memory.Complete:
		return "Complete"
	}
	return string(s)
}

func (o *Orchestrator) save(ctx context.Context, e *memory.Evolution) error {
	err := o.Store.SaveEvolution(ctx, e)
	o.Bus.Publish("evolution", e)
	return err
}

func (o *Orchestrator) event(ctx context.Context, e *memory.Evolution, kind, summary string, data any) {
	ev, err := o.Store.AddEvent(ctx, e.ID, kind, summary, data)
	if err != nil {
		slog.Warn("record evolution event", "err", err)
		return
	}
	o.Bus.Publish("evolution_event", ev)
}

func (o *Orchestrator) say(ctx context.Context, e *memory.Evolution, content string) {
	conv := e.ConversationID
	if conv == "" {
		conv = memory.DefaultConversation
	}
	m, err := o.Store.AddReport(ctx, conv, content, e.ID)
	if err == nil {
		o.Bus.Publish("message", m)
	}
}

func (o *Orchestrator) addCheck(ctx context.Context, e *memory.Evolution, c memory.Check) {
	e.Checks = append(e.Checks, c)
	_ = o.save(ctx, e)
	mark := "✓"
	if !c.OK {
		mark = "✗"
	}
	o.event(ctx, e, "check", mark+" "+c.Detail, c)
}

// ---- the evolution itself

func (o *Orchestrator) evolve(ctx context.Context, e *memory.Evolution) error {
	if err := o.transition(ctx, e, memory.Planning); err != nil {
		return err
	}
	base, err := o.Repo.Head(ctx)
	if err != nil {
		return err
	}
	e.BaseCommit = base
	if g, err := o.Store.CurrentGeneration(ctx); err == nil {
		e.BaseGeneration = g.Number
	}

	plan, err := o.plan(ctx, e)
	if err != nil {
		return fmt.Errorf("planning: %w", err)
	}
	e.Plan, e.Title = plan, plan.Title
	if err := o.transition(ctx, e, memory.Planned); err != nil {
		return err
	}
	o.event(ctx, e, "plan", "Plan: "+plan.Title, plan)
	if o.Cfg.Permissions.RequirePlanApproval {
		if err := o.ask(ctx, e, "carry out plan: "+plan.Title, permissions.Review, plan.Summary); err != nil {
			return err
		}
	}

	ws, err := o.prepareWorkspace(ctx, e)
	if err != nil {
		return fmt.Errorf("preparing workspace: %w", err)
	}
	success := false
	defer func() { ws.close(context.WithoutCancel(ctx), success) }()

	transcript := []models.Message{{Role: models.User, Content: o.mutationBrief(ctx, e, ws)}}
	maxAttempts := max(1, o.Cfg.Evolution.MaxRepairAttempts)
	var finish *finishInput
	for attempt := 1; ; attempt++ {
		e.Attempts = attempt
		if err := o.transition(ctx, e, memory.Mutating); err != nil {
			return err
		}
		transcript, finish, err = o.mutate(ctx, e, ws, transcript)
		if err != nil {
			return fmt.Errorf("evolving: %w", err)
		}
		e.Summary = finish.Summary
		_ = o.save(ctx, e)
		feedback, err := o.verify(ctx, e, ws, finish)
		if err != nil {
			return err
		}
		if feedback == "" {
			break
		}
		if attempt >= maxAttempts {
			return fmt.Errorf("verification still failing after %d attempts:\n%s", attempt, tools.Truncate(feedback, 3000))
		}
		transcript = append(transcript, models.Message{Role: models.User, Content: fmt.Sprintf(
			"The kernel's independent verification failed (attempt %d of %d):\n\n%s\n\n"+
				"Diagnose the root cause before changing anything. If a check you declared in finish has the wrong expectation "+
				"(for example the code correctly returns 404 for a missing resource but the check expected 204), fix the check, not the code. "+
				"Verify with your tools, then call finish again with corrected checks.",
			attempt, maxAttempts, feedback)})
	}

	if err := o.transition(ctx, e, memory.Reflecting); err != nil {
		return err
	}
	refl, err := o.reflect(ctx, e, ws)
	if err != nil {
		return fmt.Errorf("reflecting: %w", err)
	}
	e.Reflection = refl
	_ = o.save(ctx, e)
	o.event(ctx, e, "note", "Reflection: "+firstLine(refl.Summary), refl)

	commit, kernelChanged, err := o.commit(ctx, e, ws)
	if err != nil {
		return err
	}
	e.Commit = commit
	if err := o.transition(ctx, e, memory.Ready); err != nil {
		return err
	}
	if o.Cfg.Permissions.RequireApplyApproval {
		if err := o.ask(ctx, e, "apply generation: "+e.Title, permissions.Review, "commit "+short(commit)); err != nil {
			return err
		}
	}
	if err := o.apply(ctx, e); err != nil {
		return err
	}
	success = true
	if kernelChanged && o.OnKernelChanged != nil {
		o.say(ctx, e, "This generation changed my kernel. I'm restarting to load it.")
		o.OnKernelChanged()
	}
	return nil
}

// ask requests an owner decision, parking the evolution in needs_input.
func (o *Orchestrator) ask(ctx context.Context, e *memory.Evolution, action string, level permissions.Level, detail string) error {
	ok, err := o.evolutionApprover(e).Approve(ctx, permissions.Request{EvolutionID: e.ID, Action: action, Level: level, Detail: detail})
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("the owner declined to %s", action)
	}
	return nil
}

// evolutionApprover wraps the approval queue so that waiting is visible as
// the needs_input state, and remembers grants for the evolution.
func (o *Orchestrator) evolutionApprover(e *memory.Evolution) *approvalRecorder {
	return &approvalRecorder{o: o, e: e}
}

type approvalRecorder struct {
	o       *Orchestrator
	e       *memory.Evolution
	granted []string
	grants  map[string]bool
}

func (a *approvalRecorder) Approve(ctx context.Context, req permissions.Request) (bool, error) {
	if a.grants == nil {
		a.grants = map[string]bool{}
	}
	if a.grants[req.Action] {
		return true, nil
	}
	if a.o.Approvals == nil {
		return false, nil
	}
	prev := a.e.Status
	_ = a.o.transition(ctx, a.e, memory.NeedsInput)
	a.o.event(ctx, a.e, "approval", "Waiting for approval: "+req.Action, req)
	ok, err := a.o.Approvals.Approve(ctx, req)
	if a.e.Status == memory.NeedsInput && ctx.Err() == nil {
		_ = a.o.transition(context.WithoutCancel(ctx), a.e, prev)
	}
	if err != nil {
		return false, err
	}
	verdict := "denied"
	if ok {
		verdict = "approved"
		a.grants[req.Action] = true
		a.granted = append(a.granted, req.Action)
	}
	a.o.event(ctx, a.e, "approval", fmt.Sprintf("Owner %s: %s", verdict, req.Action), nil)
	return ok, nil
}

// ---- planning

type planInput struct {
	Title        string            `json:"title"`
	Scope        string            `json:"scope"`
	Summary      string            `json:"summary"`
	Steps        []memory.PlanStep `json:"steps"`
	Capabilities []string          `json:"capabilities"`
	Risks        []string          `json:"risks"`
}

func (o *Orchestrator) plan(ctx context.Context, e *memory.Evolution) (*memory.Plan, error) {
	ws := &tools.Workspace{Root: o.Cfg.Root, Policy: o.Policy}
	var plan *memory.Plan
	submit := &tools.Tool{
		Name:        "submit_plan",
		Description: "Submit the evolution plan. Call once, when your plan is complete.",
		Schema: tools.Schema(tools.Props{
			"title":   tools.Str("short imperative title, e.g. 'Become a todo application' or 'Add task priorities'"),
			"scope":   tools.Str("one-word lowercase scope for the commit, e.g. 'tasks'"),
			"summary": tools.Str("what I will be after this evolution, 1-3 sentences"),
			"steps": map[string]any{"type": "array", "description": "ordered steps", "items": map[string]any{
				"type": "object", "properties": map[string]any{"title": tools.Str("step"), "detail": tools.Str("optional detail")}, "required": []string{"title"}}},
			"capabilities": tools.StrList("capabilities I will have afterwards"),
			"risks":        tools.StrList("risks and assumptions"),
		}, "title", "scope", "summary", "steps"),
		Classify: tools.Fixed(permissions.Safe, "submit plan"),
		Terminal: true,
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var p planInput
			if err := json.Unmarshal(in, &p); err != nil {
				return "", err
			}
			if strings.TrimSpace(p.Title) == "" || len(p.Steps) == 0 {
				return "", errors.New("a plan needs a title and at least one step")
			}
			p.Scope = scopeRe.ReplaceAllString(strings.ToLower(p.Scope), "")
			if p.Scope == "" {
				p.Scope = "organism"
			}
			plan = &memory.Plan{Title: p.Title, Scope: p.Scope, Summary: p.Summary, Steps: p.Steps, Capabilities: p.Capabilities, Risks: p.Risks}
			return "plan recorded", nil
		},
	}
	reg := tools.NewRegistry(o.Policy, nil)
	reg.EvolutionID = e.ID
	reg.Add(tools.ReadOnlyFileTools(ws)...).Add(tools.SkillTools(skills.Library{Root: o.Cfg.Root})...).
		Add(tools.GitTools(o.Repo, "HEAD")[2]).Add(submit)
	a := o.newAgent(ctx, e, "plan", reg, 40)
	msg := fmt.Sprintf("Owner's intent:\n\n> %s\n\n%s", strings.ReplaceAll(e.Intent, "\n", "\n> "), o.SelfContext(ctx, o.Cfg.Root))
	if _, err := a.Run(ctx, []models.Message{{Role: models.User, Content: msg}}); err != nil {
		return nil, err
	}
	if plan == nil {
		return nil, errors.New("no plan was submitted")
	}
	return plan, nil
}

var scopeRe = regexp.MustCompile(`[^a-z0-9-]`)

// ---- mutation

type httpCheck struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	Body           string `json:"body,omitempty"`
	ExpectStatus   int    `json:"expect_status"`
	ExpectContains string `json:"expect_contains,omitempty"`
}

type finishInput struct {
	Summary string      `json:"summary"`
	Checks  []httpCheck `json:"checks"`
}

func (o *Orchestrator) mutate(ctx context.Context, e *memory.Evolution, ws *workspace, transcript []models.Message) ([]models.Message, *finishInput, error) {
	var finish *finishInput
	finishTool := &tools.Tool{
		Name: "finish",
		Description: "Declare the evolution implemented and verified. Provide a summary and HTTP checks that prove it works; " +
			"the kernel will independently rebuild, migrate a fresh database, run tests, start the organism and run these checks.",
		Schema: tools.Schema(tools.Props{
			"summary": tools.Str("what you changed, 1-4 sentences"),
			"checks": map[string]any{"type": "array", "description": "HTTP checks run in order against a fresh instance", "items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"method":          tools.Enum("method", "GET", "POST", "PUT", "PATCH", "DELETE"),
					"path":            tools.Str("path, e.g. /api/tasks"),
					"body":            tools.Str("JSON request body"),
					"expect_status":   tools.Int("expected HTTP status"),
					"expect_contains": tools.Str("substring the response body must contain"),
				},
				"required": []string{"method", "path", "expect_status"},
			}},
		}, "summary", "checks"),
		Classify: tools.Fixed(permissions.Safe, "finish evolution"),
		Terminal: true,
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var f finishInput
			if err := json.Unmarshal(in, &f); err != nil {
				return "", err
			}
			if strings.TrimSpace(f.Summary) == "" {
				return "", errors.New("summary is required")
			}
			changes, _ := ws.repo.WorkingChanges(ctx)
			if len(changes) == 0 {
				return "", errors.New("you have not changed anything yet")
			}
			finish = &f
			return "handing over to verification", nil
		},
	}
	reg := o.workspaceRegistry(e, ws)
	reg.Add(finishTool)
	a := o.newAgent(ctx, e, "mutate", reg, o.Cfg.Evolution.MaxAgentTurns)
	out, err := a.Run(ctx, transcript)
	if out != nil {
		transcript = out.Messages
	}
	if err != nil {
		return transcript, nil, err
	}
	if finish == nil {
		return transcript, nil, errors.New("evolution ended without finish")
	}
	return transcript, finish, nil
}

func (o *Orchestrator) workspaceRegistry(e *memory.Evolution, ws *workspace) *tools.Registry {
	// One recorder per workspace so grants (and the record of approved kernel
	// changes) survive repair attempts.
	reg := tools.NewRegistry(o.Policy, ws.approver)
	reg.EvolutionID = e.ID
	fsws := &tools.Workspace{Root: ws.dir, Policy: o.Policy}
	reg.Add(tools.FileTools(fsws)...).
		Add(tools.ExecTools(ws.env)...).
		Add(tools.DBTools(ws.env)...).
		Add(tools.GitTools(ws.repo, "HEAD")...).
		Add(tools.SkillTools(skills.Library{Root: ws.dir})...)
	return reg
}

func (o *Orchestrator) mutationBrief(ctx context.Context, e *memory.Evolution, ws *workspace) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "Owner's intent:\n\n> %s\n\n", strings.ReplaceAll(e.Intent, "\n", "\n> "))
	if e.Plan != nil {
		fmt.Fprintf(&sb, "## Your plan: %s\n%s\n\n", e.Plan.Title, e.Plan.Summary)
		for i, s := range e.Plan.Steps {
			fmt.Fprintf(&sb, "%d. %s", i+1, s.Title)
			if s.Detail != "" {
				fmt.Fprintf(&sb, " — %s", s.Detail)
			}
			sb.WriteString("\n")
		}
		if len(e.Plan.Risks) > 0 {
			sb.WriteString("\nAssumptions/risks: " + strings.Join(e.Plan.Risks, "; ") + "\n")
		}
		sb.WriteString("\n")
	}
	sb.WriteString(o.SelfContext(ctx, ws.dir))
	fmt.Fprintf(&sb, "\n## Commands\n- build: `%s`\n- test: `%s`\n- run: `%s` (listens on $PORT; health: %s)\n",
		o.Cfg.Organism.Build, o.Cfg.Organism.Test, o.Cfg.Organism.Run, o.Cfg.Organism.Health)
	return sb.String()
}

// SelfContext describes the Seed to itself: knowledge, skills, history,
// live schema and the organism's files.
func (o *Orchestrator) SelfContext(ctx context.Context, root string) string {
	var sb strings.Builder
	sb.WriteString("# What I know about myself\n\n")
	sb.WriteString(knowledge.Brief(root))
	sb.WriteString("\n## My skills (read with read_skill)\n")
	sb.WriteString(skills.Library{Root: root}.Index())
	sb.WriteString("\n## My generations\n")
	if gens, err := o.Store.Generations(ctx); err == nil {
		for i, g := range gens {
			if i >= 15 {
				fmt.Fprintf(&sb, "… %d earlier generations\n", len(gens)-i)
				break
			}
			fmt.Fprintf(&sb, "- generation %d (%s): %s\n", g.Number, short(g.Commit), g.Title)
		}
	}
	if o.Live != nil {
		sb.WriteString("\n## My live database schema\n")
		if schema, err := tools.DescribeSchema(ctx, o.Live.DatabaseURL()); err == nil {
			sb.WriteString("```\n" + schema + "\n```\n")
		} else {
			sb.WriteString("(unavailable: " + err.Error() + ")\n")
		}
	}
	sb.WriteString("\n## My organism's files\n```\n" + tools.Tree(root, "organism", 300) + "\n```\n")
	return sb.String()
}

// ---- verification

var (
	goPassRe     = regexp.MustCompile(`(?m)^\s*--- PASS: `)
	goFailRe     = regexp.MustCompile(`(?m)^\s*--- FAIL: `)
	vitestPassRe = regexp.MustCompile(`Tests\s+(?:\d+ failed \| )?(\d+) passed`)
	vitestFailRe = regexp.MustCompile(`Tests\s+(\d+) failed`)
)

// CountTests extracts pass/fail counts from go test -v and vitest output.
func CountTests(out string) (passed, failed int) {
	passed = len(goPassRe.FindAllString(out, -1))
	failed = len(goFailRe.FindAllString(out, -1))
	for _, m := range vitestPassRe.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(m[1])
		passed += n
	}
	for _, m := range vitestFailRe.FindAllStringSubmatch(out, -1) {
		n, _ := strconv.Atoi(m[1])
		failed += n
	}
	return passed, failed
}

// verify independently checks the workspace. It returns "" on success or
// feedback for the builder on failure.
func (o *Orchestrator) verify(ctx context.Context, e *memory.Evolution, ws *workspace, f *finishInput) (string, error) {
	attempt := e.Attempts
	timeout := time.Duration(o.Cfg.Evolution.CommandTimeoutSeconds) * time.Second
	fail := func(check memory.Check, output string) string {
		o.addCheck(ctx, e, check)
		return fmt.Sprintf("%s failed: %s\n\n```\n%s\n```", check.Name, check.Detail, tools.Truncate(output, 6000))
	}

	// build
	if err := o.transition(ctx, e, memory.Building); err != nil {
		return "", err
	}
	res, err := ws.sb.Exec(ctx, o.Cfg.Organism.Build, timeout, ws.env.SandboxEnv)
	if err != nil {
		return "", err
	}
	if !res.OK() {
		return fail(memory.Check{Name: "build", Detail: "Build failed", Attempt: attempt}, res.Output), nil
	}
	o.addCheck(ctx, e, memory.Check{Name: "build", OK: true, Detail: fmt.Sprintf("Build successful (%.0fs)", res.Duration.Seconds()), Attempt: attempt})

	// fresh database + migrations + tests
	if err := o.transition(ctx, e, memory.Testing); err != nil {
		return "", err
	}
	_ = ws.sb.Stop(ctx, "organism")
	if err := ws.resetDB(ctx); err != nil {
		return "", err
	}
	mres, err := ws.migrate(ctx)
	if err != nil {
		return fail(memory.Check{Name: "migrate", Detail: "Migrations failed on a fresh database", Attempt: attempt}, err.Error()), nil
	}
	o.addCheck(ctx, e, memory.Check{Name: "migrate", OK: true, Detail: "Migrations: " + mres.String(), Attempt: attempt})

	res, err = ws.sb.Exec(ctx, o.Cfg.Organism.Test, timeout, ws.env.SandboxEnv)
	if err != nil {
		return "", err
	}
	passed, failed := CountTests(res.Output)
	if !res.OK() {
		detail := "Tests failed"
		if failed > 0 {
			detail = fmt.Sprintf("%d test(s) failed", failed)
		}
		return fail(memory.Check{Name: "test", Detail: detail, Attempt: attempt, TestsPassed: &passed, TestsFailed: &failed}, res.Output), nil
	}
	o.addCheck(ctx, e, memory.Check{Name: "test", OK: true, Detail: fmt.Sprintf("%d tests passed", passed), Attempt: attempt, TestsPassed: &passed, TestsFailed: &failed})

	// launch against a clean database so checks see exactly what a fresh install sees
	if err := o.transition(ctx, e, memory.Running); err != nil {
		return "", err
	}
	if err := ws.resetDB(ctx); err != nil {
		return "", err
	}
	if _, err := ws.migrate(ctx); err != nil {
		return "", err
	}
	if _, err := tools.StartApp(ctx, ws.env, 60*time.Second); err != nil {
		return fail(memory.Check{Name: "launch", Detail: "Failed to start", Attempt: attempt}, err.Error()), nil
	}
	o.addCheck(ctx, e, memory.Check{Name: "launch", OK: true, Detail: "Launched", Attempt: attempt})
	defer ws.sb.Stop(context.WithoutCancel(ctx), "organism")

	// observe
	if err := o.transition(ctx, e, memory.Observing); err != nil {
		return "", err
	}
	checks := []httpCheck{
		{Method: "GET", Path: o.Cfg.Organism.Health, ExpectStatus: 200},
		{Method: "GET", Path: "/", ExpectStatus: 200},
	}
	for _, c := range f.Checks {
		if c.Body == "" && c.ExpectContains == "" && c.Method == "GET" && (c.Path == "/" || c.Path == o.Cfg.Organism.Health) {
			continue // already a built-in check
		}
		checks = append(checks, c)
	}
	for i, c := range checks {
		name := "http:" + c.Method + " " + c.Path
		if i == 0 {
			name = "health"
		}
		status, _, body, err := tools.HTTP(ctx, ws.sb, c.Method, c.Path, c.Body, nil)
		switch {
		case err != nil:
			return fail(memory.Check{Name: name, Detail: fmt.Sprintf("%s %s: %v", c.Method, c.Path, err), Attempt: attempt}, ws.sb.Logs(ctx, "organism", 50)), nil
		case c.ExpectStatus != 0 && status != c.ExpectStatus:
			return fail(memory.Check{Name: name, Detail: fmt.Sprintf("%s %s returned %d, expected %d", c.Method, c.Path, status, c.ExpectStatus), Attempt: attempt},
				"response body:\n"+tools.Truncate(body, 2000)+"\n\nlogs:\n"+ws.sb.Logs(ctx, "organism", 40)), nil
		case c.ExpectContains != "" && !strings.Contains(body, c.ExpectContains):
			return fail(memory.Check{Name: name, Detail: fmt.Sprintf("%s %s did not contain %q", c.Method, c.Path, c.ExpectContains), Attempt: attempt},
				"response body:\n"+tools.Truncate(body, 2000)), nil
		}
		detail := fmt.Sprintf("%s %s → %d", c.Method, c.Path, status)
		if i == 0 {
			detail = "Health check successful"
		}
		o.addCheck(ctx, e, memory.Check{Name: name, OK: true, Detail: detail, Attempt: attempt})
	}
	return "", nil
}

// ---- reflection

func (o *Orchestrator) reflect(ctx context.Context, e *memory.Evolution, ws *workspace) (*memory.Reflection, error) {
	var refl *memory.Reflection
	submit := &tools.Tool{
		Name:        "submit_reflection",
		Description: "Submit your reflection once knowledge/ and skills/ are updated.",
		Schema: tools.Schema(tools.Props{
			"summary":        tools.Str("what changed and what I am now, 1-3 sentences"),
			"goal_satisfied": tools.Bool("did the evolution fully satisfy the owner's intent"),
			"gaps":           tools.StrList("if not fully satisfied: each thing that falls short and why (required then), e.g. 'Filtering is server-side only; the UI has no filter control because …'"),
			"decisions":      tools.StrList("decisions recorded (paths or one-liners)"),
			"learnings":      tools.StrList("what future evolutions should know"),
			"skills":         tools.StrList("skills created or improved"),
			"debt":           tools.StrList("technical debt or constraints introduced"),
		}, "summary", "goal_satisfied"),
		Classify: tools.Fixed(permissions.Safe, "submit reflection"),
		Terminal: true,
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var r memory.Reflection
			if err := json.Unmarshal(in, &r); err != nil {
				return "", err
			}
			if !r.GoalSatisfied && len(r.Gaps) == 0 {
				return "", errors.New("goal_satisfied is false: list the gaps (what falls short of the owner's intent, and why)")
			}
			if err := knowledge.ValidateSelf(ws.dir); err != nil {
				return "", fmt.Errorf("fix knowledge/self.yaml first: %w", err)
			}
			for _, s := range (skills.Library{Root: ws.dir}).List() {
				if strings.HasPrefix(s.Description, "(invalid skill") {
					return "", fmt.Errorf("skill %s is invalid: %s", s.Name, s.Description)
				}
			}
			refl = &r
			return "reflection recorded", nil
		},
	}
	fsws := &tools.Workspace{Root: ws.dir, Policy: o.Policy, WriteFilter: func(rel string) error {
		if strings.HasPrefix(rel, "knowledge/") || strings.HasPrefix(rel, "skills/") {
			return nil
		}
		return errors.New("during reflection I can only write knowledge/ and skills/")
	}}
	reg := tools.NewRegistry(o.Policy, nil)
	reg.EvolutionID = e.ID
	reg.Add(tools.FileTools(fsws)...).Add(tools.GitTools(ws.repo, "HEAD")...).Add(tools.SkillTools(skills.Library{Root: ws.dir})...).Add(submit)
	a := o.newAgent(ctx, e, "reflect", reg, 40)
	var checks []string
	for _, c := range e.Checks {
		if c.Attempt == e.Attempts {
			checks = append(checks, c.Detail)
		}
	}
	msg := fmt.Sprintf("Owner's intent:\n\n> %s\n\nPlan: %s\n\nWhat the builder reported: %s\n\nVerification (attempt %d): %s\n\n%s",
		e.Intent, e.Plan.Title, e.Summary, e.Attempts, strings.Join(checks, "; "), knowledge.Brief(ws.dir)+"\n## Skills\n"+skills.Library{Root: ws.dir}.Index())
	if _, err := a.Run(ctx, []models.Message{{Role: models.User, Content: msg}}); err != nil {
		return nil, err
	}
	if refl == nil {
		return nil, errors.New("no reflection submitted")
	}
	return refl, nil
}

// ---- commit and apply

func (o *Orchestrator) commit(ctx context.Context, e *memory.Evolution, ws *workspace) (string, bool, error) {
	changes, err := ws.repo.WorkingChanges(ctx)
	if err != nil {
		return "", false, err
	}
	protected := o.Policy.ProtectedIn(changes)
	var unapproved []string
	for _, p := range protected {
		ok := false
		for _, g := range ws.approver.granted {
			if strings.Contains(g, " "+p+" ") || strings.HasSuffix(g, " "+p) {
				ok = true
			}
		}
		if !ok {
			unapproved = append(unapproved, p)
		}
	}
	if len(unapproved) > 0 {
		return "", false, fmt.Errorf("kernel boundary violated: %s changed without owner approval", strings.Join(unapproved, ", "))
	}
	next := e.BaseGeneration + 1
	scope := "organism"
	if e.Plan != nil && e.Plan.Scope != "" {
		scope = e.Plan.Scope
	}
	msg := fmt.Sprintf("evolve(%s): %s\n\n%s\n\n%s\n\nEvolution: %s\nGeneration: %d -> %d\n",
		scope, lowerFirst(e.Title), wrap(e.Intent), wrap(e.Summary), e.ID, e.BaseGeneration, next)
	// The generation is authored by who the Seed has become.
	ws.repo.AuthorName = knowledge.Name(ws.dir)
	ws.repo.AuthorEmail = o.Cfg.Name + "@seed.local"
	commit, err := ws.repo.CommitAll(ctx, msg)
	if err != nil {
		return "", false, err
	}
	o.event(ctx, e, "note", "Committed "+short(commit), map[string]any{"commit": commit, "files": changes})
	return commit, len(protected) > 0, nil
}

func (o *Orchestrator) apply(ctx context.Context, e *memory.Evolution) error {
	if err := o.transition(ctx, e, memory.Applying); err != nil {
		return err
	}
	// From here on, finish what we started even if cancelled.
	ctx = context.WithoutCancel(ctx)
	if status, err := o.Repo.Status(ctx); err != nil || status != "" {
		return fmt.Errorf("my live working tree has uncommitted changes, refusing to apply:\n%s", status)
	}
	head, err := o.Repo.Head(ctx)
	if err != nil {
		return err
	}
	if head != e.BaseCommit {
		return fmt.Errorf("my live generation moved from %s to %s during this evolution; evolve again from the new state", short(e.BaseCommit), short(head))
	}
	if err := o.Repo.MergeFastForward(ctx, e.Commit); err != nil {
		return err
	}
	if err := o.Live.Deploy(ctx); err != nil {
		o.event(ctx, e, "error", "New generation unhealthy; restoring previous generation", map[string]string{"error": err.Error()})
		if rerr := o.Repo.ResetHard(ctx, e.BaseCommit); rerr != nil {
			return fmt.Errorf("deploy failed (%v) and restoring failed: %w", err, rerr)
		}
		if derr := o.Live.Deploy(ctx); derr != nil {
			slog.Error("redeploy of previous generation failed", "err", derr)
		}
		e.Status = memory.RolledBack
		return fmt.Errorf("deploy failed: %w", err)
	}
	if err := o.recordGeneration(ctx, e); err != nil {
		return err
	}
	if err := o.transition(ctx, e, memory.Complete); err != nil {
		return err
	}
	now := time.Now()
	e.CompletedAt = &now
	_ = o.save(ctx, e)
	o.say(ctx, e, completionMessage(e))
	return nil
}

func completionMessage(e *memory.Evolution) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "I am now **generation %d**: %s (`%s`).", *e.NewGeneration, e.Title, short(e.Commit))
	if e.Reflection != nil && e.Reflection.Summary != "" {
		sb.WriteString("\n\n" + e.Reflection.Summary)
	}
	if e.Reflection != nil && !e.Reflection.GoalSatisfied && len(e.Reflection.Gaps) > 0 {
		sb.WriteString("\n\n**Not fully done yet:**")
		for _, g := range e.Reflection.Gaps {
			sb.WriteString("\n- " + g)
		}
	}
	return sb.String()
}

func (o *Orchestrator) recordGeneration(ctx context.Context, e *memory.Evolution) error {
	n := e.BaseGeneration + 1
	if cur, err := o.Store.CurrentGeneration(ctx); err == nil && cur.Number >= n {
		n = cur.Number + 1
	}
	g := &memory.Generation{Number: n, Title: e.Title, Intent: e.Intent, EvolutionID: e.ID, Commit: e.Commit, ParentCommit: e.BaseCommit}
	buildOK, healthOK := false, false
	for _, c := range e.Checks {
		if c.Attempt != e.Attempts {
			continue
		}
		switch c.Name {
		case "build":
			buildOK = c.OK
		case "health":
			healthOK = c.OK
		case "test":
			g.TestsPassed = c.TestsPassed
		}
	}
	if e.Kind == "evolve" {
		g.BuildOK, g.HealthOK = &buildOK, &healthOK
	}
	if err := o.Store.AddGeneration(ctx, g); err != nil {
		return err
	}
	_ = o.Repo.Tag(ctx, fmt.Sprintf("seed/gen-%d", n), e.Commit)
	e.NewGeneration = &n
	return nil
}

// ---- rollback

// rollback creates a new generation whose tree equals an earlier one. History
// is preserved: rolling back is itself an evolution.
func (o *Orchestrator) rollback(ctx context.Context, e *memory.Evolution) error {
	target, err := o.Store.Generation(ctx, *e.TargetGeneration)
	if err != nil {
		return err
	}
	base, err := o.Repo.Head(ctx)
	if err != nil {
		return err
	}
	e.BaseCommit = base
	if g, err := o.Store.CurrentGeneration(ctx); err == nil {
		e.BaseGeneration = g.Number
	}
	if err := o.transition(ctx, e, memory.Applying); err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	if status, _ := o.Repo.Status(ctx); status != "" {
		return fmt.Errorf("my live working tree has uncommitted changes, refusing to roll back:\n%s", status)
	}
	if err := o.Repo.RestoreTree(ctx, target.Commit); err != nil {
		return err
	}
	o.Repo.AuthorName, o.Repo.AuthorEmail = knowledge.Name(o.Cfg.Root), o.Cfg.Name+"@seed.local"
	msg := fmt.Sprintf("rollback: return to generation %d\n\n%s\n\nRestores the tree of generation %d (%s). Database migrations applied since then are kept.\n\nEvolution: %s\nGeneration: %d -> %d\n",
		target.Number, target.Title, target.Number, short(target.Commit), e.ID, e.BaseGeneration, e.BaseGeneration+1)
	commit, err := o.Repo.CommitAll(ctx, msg)
	if err != nil {
		_ = o.Repo.ResetHard(ctx, base)
		return err
	}
	e.Commit = commit
	if err := o.Live.Deploy(ctx); err != nil {
		_ = o.Repo.ResetHard(ctx, base)
		_ = o.Live.Deploy(ctx)
		e.Status = memory.RolledBack
		return fmt.Errorf("generation %d did not come up healthy: %w", target.Number, err)
	}
	if err := o.recordGeneration(ctx, e); err != nil {
		return err
	}
	if err := o.transition(ctx, e, memory.Complete); err != nil {
		return err
	}
	now := time.Now()
	e.CompletedAt = &now
	_ = o.save(ctx, e)
	o.say(ctx, e, fmt.Sprintf("I rolled back to what I was in generation %d (%s). That is now **generation %d** (`%s`).", target.Number, target.Title, *e.NewGeneration, short(commit)))
	return nil
}

// ---- agents

func (o *Orchestrator) newAgent(ctx context.Context, e *memory.Evolution, task string, reg *tools.Registry, maxTurns int) *agent.Agent {
	a := &agent.Agent{
		Model: o.Model, Tools: reg, System: agent.PromptFor(task, knowledge.Name(o.Cfg.Root)), MaxTurns: maxTurns,
		MaxTokens: o.Cfg.Model.MaxTokens, RequireTerminal: true,
	}
	a.Hooks.OnToolResult = o.toolHook(ctx, e)
	a.Hooks.OnText = func(text string) {
		o.event(ctx, e, "agent_text", firstLine(text), map[string]string{"text": tools.Truncate(text, 4000), "phase": task})
	}
	a.Hooks.OnUsage = func(u models.Usage) {
		e.Usage.InputTokens += u.InputTokens
		e.Usage.OutputTokens += u.OutputTokens
		e.Usage.CacheReadTokens += u.CacheReadTokens
		e.Usage.ModelCalls++
	}
	return a
}

func (o *Orchestrator) toolHook(ctx context.Context, e *memory.Evolution) func(models.ToolCall, tools.Outcome) {
	return func(call models.ToolCall, out tools.Outcome) {
		summary := out.Action
		if out.Result.IsError {
			summary = "✗ " + summary + ": " + firstLine(out.Result.Content)
		}
		var input any
		_ = json.Unmarshal(call.Input, &input)
		input = elideLarge(input)
		o.event(ctx, e, "tool_call", tools.Truncate(summary, 200), map[string]any{
			"tool": call.Name, "input": input, "output": tools.Truncate(out.Result.Content, 4000),
			"error": out.Result.IsError, "level": out.Level.String(),
		})
	}
}

func elideLarge(v any) any {
	m, ok := v.(map[string]any)
	if !ok {
		return v
	}
	for k, val := range m {
		if s, ok := val.(string); ok && len(s) > 4000 {
			m[k] = s[:4000] + fmt.Sprintf("…[%d chars]", len(s))
		}
	}
	return m
}

// ---- helpers

func short(commit string) string {
	if len(commit) > 10 {
		return commit[:10]
	}
	return commit
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	return tools.Truncate(s, 200)
}

func titleOr(e *memory.Evolution) string {
	if e.Title != "" {
		return e.Title
	}
	return tools.Truncate(e.Intent, 80)
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func wrap(s string) string {
	words := strings.Fields(s)
	var sb strings.Builder
	n := 0
	for _, w := range words {
		if n > 0 && n+len(w) > 72 {
			sb.WriteString("\n")
			n = 0
		} else if n > 0 {
			sb.WriteString(" ")
			n++
		}
		sb.WriteString(w)
		n += len(w)
	}
	return sb.String()
}

// workspacesDir is where evolution worktrees live.
func (o *Orchestrator) workspacesDir() string {
	return filepath.Join(o.Cfg.Root, ".seed", "worktrees")
}

func ensureDir(p string) error { return os.MkdirAll(p, 0o755) }
