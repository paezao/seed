package evolution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"seed/kernel/config"
	"seed/kernel/events"
	"seed/kernel/git"
	"seed/kernel/infra"
	"seed/kernel/memory"
	"seed/kernel/models"
	"seed/kernel/permissions"
	"seed/kernel/sandbox"
	"seed/kernel/testutil"
)

// fakeLive records deployments and can be told to fail.
type fakeLive struct {
	mu      sync.Mutex
	deploys int
	fail    bool
	url     string
}

func (f *fakeLive) Deploy(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deploys++
	if f.fail {
		f.fail = false // the restored generation deploys fine
		return errors.New("health check failed")
	}
	return nil
}
func (f *fakeLive) DatabaseURL() string { return f.url }

type harness struct {
	t     *testing.T
	o     *Orchestrator
	repo  *git.Repo
	live  *fakeLive
	model *models.Scripted
	base  string
}

const testServer = `exec python3 -c "
import http.server,os
class H(http.server.BaseHTTPRequestHandler):
    def log_message(s,*a): pass
    def do_GET(s):
        s.send_response(200); s.end_headers(); s.wfile.write(b'ok')
    do_POST=do_GET
http.server.HTTPServer(('127.0.0.1', int(os.environ['PORT'])), H).serve_forever()"`

func newHarness(t *testing.T, mutate func(cfg *config.Config)) *harness {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 required")
	}
	ctx := context.Background()
	dbURL := testutil.Database(t)
	store, err := memory.Open(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)

	root := t.TempDir()
	write(t, root, "knowledge/self.yaml", "identity:\n  slug: orchtest\n  name: Seed\npurpose:\n  description: \"\"\n")
	write(t, root, "kernel/core.go", "package kernel\n")
	write(t, root, "organism/migrations/0001_init.sql", "CREATE TABLE things (id serial primary key);\n")
	write(t, root, ".gitignore", ".seed/\n")
	repo := git.Open(root)
	if err := repo.Init(ctx); err != nil {
		t.Fatal(err)
	}
	base, err := repo.CommitAll(ctx, "seed: initial seed\n\nGeneration: 1")
	if err != nil {
		t.Fatal(err)
	}
	_ = store.AddGeneration(ctx, &memory.Generation{Number: 1, Title: "Initial seed", Commit: base})

	cfg := config.Defaults()
	cfg.Name = "orchtest" + strings.ToLower(filepath.Base(root))[len(filepath.Base(root))-3:]
	cfg.Root = root
	cfg.Sandbox.Driver = "local"
	cfg.Organism.Build = "true"
	cfg.Organism.Test = "echo '--- PASS: TestThing (0.00s)'"
	cfg.Organism.Run = testServer
	cfg.Evolution.MaxRepairAttempts = 2
	cfg.Evolution.CommandTimeoutSeconds = 30
	if mutate != nil {
		mutate(&cfg)
	}
	admin := infra.Admin{URL: testutil.AdminURL()}
	role := "seedtest_organism"
	if err := admin.EnsureRole(ctx, role, "pw"); err != nil {
		t.Fatal(err)
	}
	policy := permissions.NewPolicy(cfg.Permissions.Safe, cfg.Permissions.Review, cfg.Permissions.Dangerous, cfg.Kernel.Evolvable)
	live := &fakeLive{url: dbURL}
	model := &models.Scripted{}
	bus := events.NewBus()
	o := &Orchestrator{
		Cfg: &cfg, Store: store, Bus: bus, Repo: repo, Model: model, Driver: sandbox.LocalDriver{},
		Admin: admin, Policy: policy, Approvals: &Approvals{Store: store, Bus: bus}, Live: live,
		DB: DBCreds{Role: role, Password: "pw"},
	}
	o.init()
	return &harness{t: t, o: o, repo: repo, live: live, model: model, base: base}
}

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

type step = func(models.Request) (*models.Response, error)

func planStep() step {
	return models.Call("submit_plan", map[string]any{
		"title": "Become a thing tracker", "scope": "things", "summary": "I track things.",
		"steps": []map[string]string{{"title": "Add things"}}, "capabilities": []string{"things"},
	})
}

func finishStep(checks ...map[string]any) step {
	if checks == nil {
		checks = []map[string]any{}
	}
	return models.Call("finish", map[string]any{"summary": "Added things.", "checks": checks})
}

func reflectSteps() []step {
	return []step{
		models.Call("write_file", map[string]string{"path": "knowledge/self.yaml",
			"content": "identity:\n  slug: orchtest\n  name: Thingy\npurpose:\n  description: I track things.\ncapabilities:\n  - name: things\n"}),
		models.Call("submit_reflection", map[string]any{"summary": "I am Thingy now.", "goal_satisfied": true}),
	}
}

func (h *harness) run(intent string, steps ...step) *memory.Evolution {
	h.t.Helper()
	h.model.Steps = steps
	ctx := context.Background()
	e, err := h.o.Request(ctx, memory.DefaultConversation, intent)
	if err != nil {
		h.t.Fatal(err)
	}
	queued, _ := h.o.Store.EvolutionsByStatus(ctx, memory.Requested)
	h.o.process(ctx, queued[0])
	got, err := h.o.Store.Evolution(ctx, e.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	if testing.Verbose() {
		evs, _ := h.o.Store.Events(ctx, e.ID)
		for _, ev := range evs {
			h.t.Logf("%s | %s | %v", ev.Kind, ev.Summary, ev.Data)
		}
	}
	return got
}

func (h *harness) head() string {
	head, _ := h.repo.Head(context.Background())
	return head
}

func TestEvolutionHappyPath(t *testing.T) {
	h := newHarness(t, nil)
	steps := []step{
		planStep(),
		models.Call("write_file", map[string]string{"path": "organism/migrations/0002_more.sql", "content": "ALTER TABLE things ADD COLUMN name text;"}),
		models.Call("migrate", map[string]any{}),
		finishStep(map[string]any{"method": "GET", "path": "/api/things", "expect_status": 200}),
	}
	e := h.run("become a thing tracker", append(steps, reflectSteps()...)...)
	if e.Status != memory.Complete {
		t.Fatalf("status %s, error %q", e.Status, e.Error)
	}
	if e.NewGeneration == nil || *e.NewGeneration != 2 || e.Attempts != 1 {
		t.Fatalf("generation/attempts: %+v", e)
	}
	if h.head() != e.Commit || h.live.deploys != 1 {
		t.Fatalf("main should be at the evolution commit and deployed once (head %s commit %s deploys %d)", h.head(), e.Commit, h.live.deploys)
	}
	logs, _ := h.repo.Log(context.Background(), "HEAD", 1)
	tr := git.Trailers(logs[0].Body)
	if tr["Evolution"] != e.ID || tr["Generation"] != "1 -> 2" || !strings.HasPrefix(logs[0].Subject, "evolve(things):") {
		t.Fatalf("commit metadata: %q %v", logs[0].Subject, tr)
	}
	if b, _ := os.ReadFile(filepath.Join(h.o.Cfg.Root, "knowledge/self.yaml")); !strings.Contains(string(b), "Thingy") {
		t.Fatal("reflection should have updated knowledge in the generation")
	}
	if _, err := os.Stat(e.Worktree); !os.IsNotExist(err) {
		t.Fatal("worktree should be removed after success")
	}
	names := map[string]bool{}
	for _, c := range e.Checks {
		if !c.OK {
			t.Fatalf("unexpected failing check %+v", c)
		}
		names[c.Name] = true
	}
	for _, want := range []string{"build", "migrate", "test", "launch", "health", "http:GET /api/things"} {
		if !names[want] {
			t.Errorf("missing check %s in %v", want, names)
		}
	}
	gen, _ := h.o.Store.CurrentGeneration(context.Background())
	if gen.Number != 2 || *gen.TestsPassed != 1 {
		t.Fatalf("generation record: %+v", gen)
	}
	msgs, _ := h.o.Store.Messages(context.Background(), memory.DefaultConversation, 10)
	if len(msgs) == 0 || !strings.Contains(msgs[len(msgs)-1].Content, "generation 2") {
		t.Fatalf("expected completion message, got %+v", msgs)
	}
}

func TestEvolutionRepairsFailedBuild(t *testing.T) {
	h := newHarness(t, func(c *config.Config) {
		c.Organism.Build = "test -f organism/fixed || (echo 'compile error: missing fixed'; exit 1)"
	})
	steps := []step{
		planStep(),
		models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v1"}),
		finishStep(),
		// The builder sees the failure and repairs it.
		func(r models.Request) (*models.Response, error) {
			last := r.Messages[len(r.Messages)-1].Content
			if !strings.Contains(last, "compile error: missing fixed") {
				return nil, errors.New("builder did not receive build output: " + last)
			}
			return models.Call("write_file", map[string]string{"path": "organism/fixed", "content": "ok"})(r)
		},
		finishStep(),
	}
	e := h.run("become a thing tracker", append(steps, reflectSteps()...)...)
	if e.Status != memory.Complete || e.Attempts != 2 {
		t.Fatalf("expected completion after repair: status %s attempts %d err %q", e.Status, e.Attempts, e.Error)
	}
	if e.Checks[0].Name != "build" || e.Checks[0].OK {
		t.Fatalf("first check should be the failed build: %+v", e.Checks[0])
	}
}

func TestEvolutionFailingTestsExhaustAttempts(t *testing.T) {
	h := newHarness(t, func(c *config.Config) { c.Organism.Test = "echo '--- FAIL: TestThing'; exit 1" })
	steps := []step{
		planStep(),
		models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v1"}),
		finishStep(),
		models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v2"}),
		finishStep(),
	}
	e := h.run("become a thing tracker", steps...)
	if e.Status != memory.Failed || !strings.Contains(e.Error, "after 2 attempts") {
		t.Fatalf("expected failure after attempts: %s %q", e.Status, e.Error)
	}
	if h.head() != h.base || h.live.deploys != 0 {
		t.Fatal("a failed evolution must not touch the live generation")
	}
	if _, err := os.Stat(filepath.Join(e.Worktree, "organism/app.txt")); err != nil {
		t.Fatal("failed evolution's worktree should be preserved for inspection")
	}
	var failed int
	for _, c := range e.Checks {
		if c.Name == "test" && !c.OK && c.TestsFailed != nil && *c.TestsFailed == 1 {
			failed++
		}
	}
	if failed != 2 {
		t.Fatalf("expected 2 failed test checks, got %+v", e.Checks)
	}
}

func TestMigrationFailureIsReported(t *testing.T) {
	h := newHarness(t, nil)
	steps := []step{
		planStep(),
		models.Call("write_file", map[string]string{"path": "organism/migrations/0002_bad.sql", "content": "ALTER TABLE nope ADD COLUMN x int;"}),
		finishStep(),
		func(r models.Request) (*models.Response, error) {
			if !strings.Contains(r.Messages[len(r.Messages)-1].Content, "0002_bad") {
				return nil, errors.New("migration failure not reported")
			}
			return models.Call("write_file", map[string]string{"path": "organism/migrations/0002_bad.sql", "content": "ALTER TABLE things ADD COLUMN x int;"})(r)
		},
		finishStep(),
	}
	e := h.run("add x", append(steps, reflectSteps()...)...)
	if e.Status != memory.Complete || e.Attempts != 2 {
		t.Fatalf("status %s attempts %d err %q", e.Status, e.Attempts, e.Error)
	}
}

func TestKernelBoundary(t *testing.T) {
	// The file tools refuse kernel writes (dangerous -> deny here)...
	h := newHarness(t, func(c *config.Config) { c.Permissions.Dangerous = "deny" })
	h.o.Policy = permissions.NewPolicy("allow", "allow", "deny", h.o.Cfg.Kernel.Evolvable)
	steps := []step{
		planStep(),
		func(r models.Request) (*models.Response, error) {
			return models.Call("write_file", map[string]string{"path": "kernel/core.go", "content": "package hacked"})(r)
		},
		func(r models.Request) (*models.Response, error) {
			res := r.Messages[len(r.Messages)-1].ToolResults[0]
			if !res.IsError || !strings.Contains(res.Content, "permission denied") {
				return nil, errors.New("kernel write was not denied")
			}
			// ...and a sandbox escape attempt (possible only with the unisolated
			// local driver) is caught at commit time.
			return models.Call("run", map[string]string{"command": "echo hacked > kernel/core.go"})(r)
		},
		models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v1"}),
		finishStep(),
	}
	e := h.run("do something", append(steps, reflectSteps()...)...)
	if e.Status != memory.Failed || !strings.Contains(e.Error, "kernel boundary violated: kernel/core.go") {
		t.Fatalf("expected kernel boundary failure, got %s %q", e.Status, e.Error)
	}
	if h.head() != h.base {
		t.Fatal("live generation must be untouched")
	}
}

func TestKernelChangeWithApproval(t *testing.T) {
	h := newHarness(t, nil) // dangerous: ask
	restarted := false
	h.o.OnKernelChanged = func() { restarted = true }
	go func() {
		// The owner approves the pending request from the control plane.
		for i := 0; i < 200; i++ {
			aps, _ := h.o.Store.PendingApprovals(context.Background())
			if len(aps) > 0 {
				_, _ = h.o.Approvals.Decide(context.Background(), aps[0].ID, true)
				return
			}
			time.Sleep(25 * time.Millisecond)
		}
	}()
	steps := []step{
		planStep(),
		models.Call("write_file", map[string]string{"path": "kernel/core.go", "content": "package kernel // improved\n"}),
		finishStep(),
	}
	e := h.run("improve my kernel", append(steps, reflectSteps()...)...)
	if e.Status != memory.Complete || !restarted {
		t.Fatalf("approved kernel change should complete and request restart: %s %q restarted=%v", e.Status, e.Error, restarted)
	}
}

func TestDeployFailureRollsBack(t *testing.T) {
	h := newHarness(t, nil)
	h.live.fail = true
	steps := []step{planStep(), models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v1"}), finishStep()}
	e := h.run("become a thing tracker", append(steps, reflectSteps()...)...)
	if e.Status != memory.RolledBack {
		t.Fatalf("expected rolled_back, got %s %q", e.Status, e.Error)
	}
	if h.head() != h.base || h.live.deploys != 2 {
		t.Fatalf("expected main restored to base and redeployed (head %s deploys %d)", h.head(), h.live.deploys)
	}
	gen, _ := h.o.Store.CurrentGeneration(context.Background())
	if gen.Number != 1 {
		t.Fatal("no generation should be recorded")
	}
}

func TestModelFailureFailsEvolution(t *testing.T) {
	h := newHarness(t, nil)
	e := h.run("anything", models.Fail(errors.New("provider unavailable")))
	if e.Status != memory.Failed || !strings.Contains(e.Error, "provider unavailable") {
		t.Fatalf("expected failure, got %s %q", e.Status, e.Error)
	}
}

func TestRollbackCreatesNewGeneration(t *testing.T) {
	h := newHarness(t, nil)
	steps := []step{planStep(), models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v1"}), finishStep()}
	e := h.run("become a thing tracker", append(steps, reflectSteps()...)...)
	if e.Status != memory.Complete {
		t.Fatalf("setup evolution failed: %q", e.Error)
	}
	ctx := context.Background()
	rb, err := h.o.RequestRollback(ctx, memory.DefaultConversation, 1)
	if err != nil {
		t.Fatal(err)
	}
	h.o.process(ctx, rb)
	rb, _ = h.o.Store.Evolution(ctx, rb.ID)
	if rb.Status != memory.Complete || *rb.NewGeneration != 3 {
		t.Fatalf("rollback: %s %q", rb.Status, rb.Error)
	}
	if d, _ := h.repo.Diff(ctx, h.base, "HEAD"); d != "" {
		t.Fatalf("tree should equal generation 1:\n%s", d)
	}
	if _, err := h.o.RequestRollback(ctx, memory.DefaultConversation, 3); err == nil {
		t.Fatal("rolling back to the current generation should be refused")
	}
}

func TestRecoverInterruptedEvolutions(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	// An evolution interrupted mid-test, and one interrupted after its merge.
	stuck := &memory.Evolution{Intent: "stuck", ConversationID: memory.DefaultConversation}
	_ = h.o.Store.CreateEvolution(ctx, stuck)
	stuck.Status = memory.Testing
	_ = h.o.Store.SaveEvolution(ctx, stuck)

	write(t, h.o.Cfg.Root, "organism/app.txt", "v1")
	commit, _ := h.repo.CommitAll(ctx, "evolve(x): applied before crash")
	applied := &memory.Evolution{Intent: "applied", Title: "Applied", ConversationID: memory.DefaultConversation, BaseGeneration: 1}
	_ = h.o.Store.CreateEvolution(ctx, applied)
	applied.Status, applied.Commit, applied.BaseCommit = memory.Applying, commit, h.base
	_ = h.o.Store.SaveEvolution(ctx, applied)

	queued := &memory.Evolution{Intent: "queued"}
	_ = h.o.Store.CreateEvolution(ctx, queued)

	if err := h.o.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	s, _ := h.o.Store.Evolution(ctx, stuck.ID)
	if s.Status != memory.Failed || !strings.Contains(s.Error, "interrupted") {
		t.Fatalf("stuck evolution: %s %q", s.Status, s.Error)
	}
	a, _ := h.o.Store.Evolution(ctx, applied.ID)
	if a.Status != memory.Complete || a.NewGeneration == nil || *a.NewGeneration != 2 {
		t.Fatalf("applied evolution should be completed on recovery: %+v", a)
	}
	q, _ := h.o.Store.Evolution(ctx, queued.ID)
	if q.Status != memory.Requested {
		t.Fatal("queued evolutions should stay queued")
	}
}

func TestCancelQueued(t *testing.T) {
	h := newHarness(t, nil)
	ctx := context.Background()
	e, _ := h.o.Request(ctx, memory.DefaultConversation, "something")
	got, err := h.o.Cancel(ctx, e.ID)
	if err != nil || got.Status != memory.Cancelled {
		t.Fatalf("cancel: %+v %v", got, err)
	}
}

func TestCountTests(t *testing.T) {
	out := "=== RUN TestA\n--- PASS: TestA (0.00s)\n    --- PASS: TestA/sub (0.00s)\n--- FAIL: TestB\n Test Files  2 passed (2)\n      Tests  5 passed (5)\n"
	p, f := CountTests(out)
	if p != 7 || f != 1 {
		t.Fatalf("got %d passed %d failed", p, f)
	}
	p, f = CountTests("      Tests  1 failed | 3 passed (4)")
	if p != 3 || f != 1 {
		t.Fatalf("vitest mixed: %d %d", p, f)
	}
}
