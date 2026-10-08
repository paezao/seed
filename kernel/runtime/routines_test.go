package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"seed/kernel/events"
	"seed/kernel/memory"
	"seed/kernel/permissions"
	"seed/kernel/testutil"
)

type fakeAgent struct {
	mu    sync.Mutex
	reply string
	runs  []string
}

func (f *fakeAgent) RunRoutine(_ context.Context, r *memory.Routine) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runs = append(f.runs, r.Name)
	return f.reply, nil
}

type fakeJobs struct {
	mu     sync.Mutex
	status int
	calls  []string
}

func (f *fakeJobs) CallJob(_ context.Context, name, method, path string) (int, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name+" "+method+" "+path)
	return f.status, "done", nil
}

func testScheduler(t *testing.T) (*Scheduler, *fakeAgent, *fakeJobs) {
	t.Helper()
	store, err := memory.Open(context.Background(), testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	ag, jobs := &fakeAgent{reply: "3 new recipes yesterday."}, &fakeJobs{status: 200}
	return &Scheduler{Root: t.TempDir(), Store: store, Bus: events.NewBus(), Agent: ag, Jobs: jobs}, ag, jobs
}

// waitRun waits for a routine's latest run to finish.
func waitRun(t *testing.T, s *Scheduler, id string) memory.RoutineRun {
	t.Helper()
	for i := 0; i < 100; i++ {
		runs, _ := s.Store.RoutineRuns(context.Background(), id, 1)
		if len(runs) > 0 && runs[0].Status != "running" {
			return runs[0]
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("run did not finish")
	return memory.RoutineRun{}
}

func TestCreateValidates(t *testing.T) {
	s, _, _ := testScheduler(t)
	ctx := context.Background()
	for _, bad := range []*memory.Routine{
		{Name: "Daily Summary", Kind: "agent", Schedule: "every 1d", Prompt: "x"},
		{Name: "fast", Kind: "agent", Schedule: "every 5m", Prompt: "x"},
		{Name: "empty", Kind: "agent", Schedule: "every 1h"},
		{Name: "probe", Kind: "job", Schedule: "every 1h", Path: "/_seed/api/approvals"},
		{Name: "tz", Kind: "agent", Schedule: "0 8 * * *", Timezone: "Nowhere/City", Prompt: "x"},
	} {
		if err := s.Create(ctx, bad); err == nil {
			t.Errorf("%+v should be refused", bad)
		}
	}
	r := &memory.Routine{Name: "daily-summary", Kind: "agent", Schedule: "0 8 * * *", Timezone: "Europe/Lisbon", Prompt: "Summarize yesterday."}
	if err := s.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	if r.NextRunAt == nil || r.NextRunAt.In(time.UTC).Minute() != 0 || r.Source != "owner" {
		t.Fatalf("scheduled from now: %+v", r)
	}
	if err := s.Create(ctx, &memory.Routine{Name: "daily-summary", Kind: "agent", Schedule: "every 1h", Prompt: "again"}); err == nil {
		t.Fatal("names are unique")
	}
}

func TestDueRoutinesRunAndReport(t *testing.T) {
	s, ag, jobs := testScheduler(t)
	ctx := context.Background()
	agentR := &memory.Routine{Name: "daily-summary", Kind: "agent", Schedule: "every 1h", Prompt: "Summarize."}
	jobR := &memory.Routine{Name: "cleanup", Kind: "job", Schedule: "every 1h", Path: "/internal/jobs/cleanup"}
	for _, r := range []*memory.Routine{agentR, jobR} {
		if err := s.Create(ctx, r); err != nil {
			t.Fatal(err)
		}
	}
	// Two hours pass (I was stopped): each runs once, not twice.
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	s.Tick(ctx)
	s.Tick(ctx)
	if run := waitRun(t, s, agentR.ID); run.Status != "ok" || run.Trigger != "schedule" {
		t.Fatalf("agent run: %+v", run)
	}
	if run := waitRun(t, s, jobR.ID); run.Status != "ok" || !strings.Contains(run.Output, "HTTP 200") {
		t.Fatalf("job run: %+v", run)
	}
	if len(ag.runs) != 1 || len(jobs.calls) != 1 || jobs.calls[0] != "cleanup POST /internal/jobs/cleanup" {
		t.Fatalf("each due routine runs once: %v %v", ag.runs, jobs.calls)
	}
	msgs, _ := s.Store.Messages(ctx, memory.DefaultConversation, 10)
	if len(msgs) != 1 || msgs[0].Kind != "routine" || !strings.Contains(msgs[0].Content, "3 new recipes") {
		t.Fatalf("an agent routine reports in chat: %+v", msgs)
	}
	// Nothing to report: no chat message.
	ag.reply = "NOTHING_TO_REPORT"
	if _, err := s.Launch(ctx, agentR, "manual"); err != nil {
		t.Fatal(err)
	}
	if run := waitRun(t, s, agentR.ID); run.Status != "quiet" {
		t.Fatalf("quiet run: %+v", run)
	}
	if msgs, _ := s.Store.Messages(ctx, memory.DefaultConversation, 10); len(msgs) != 1 {
		t.Fatal("a quiet run posts nothing")
	}
	// A failing job is recorded as failed.
	jobs.status = 500
	if _, err := s.Launch(ctx, jobR, "manual"); err != nil {
		t.Fatal(err)
	}
	if run := waitRun(t, s, jobR.ID); run.Status != "failed" || !strings.Contains(run.Output, "HTTP 500") {
		t.Fatalf("failed job: %+v", run)
	}
	// Paused routines don't run.
	jobR.Enabled = false
	_ = s.Store.SaveRoutine(ctx, jobR)
	s.schedule(ctx, jobR)
	s.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	before := len(jobs.calls)
	s.Tick(ctx)
	time.Sleep(100 * time.Millisecond)
	if len(jobs.calls) != before {
		t.Fatal("a paused routine must not run")
	}
}

func TestOrganismJobsSync(t *testing.T) {
	s, _, _ := testScheduler(t)
	ctx := context.Background()
	write := func(y string) {
		os.MkdirAll(filepath.Join(s.Root, "organism"), 0o755)
		os.WriteFile(filepath.Join(s.Root, "organism", "routines.yaml"), []byte(y), 0o644)
	}
	write("jobs:\n  - name: send-reminders\n    schedule: every 1h\n    path: /internal/jobs/reminders\n  - name: cleanup\n    schedule: every 1d\n    path: /internal/jobs/cleanup\n")
	s.SyncJobs(ctx)
	all, _ := s.Store.Routines(ctx)
	if len(all) != 2 || all[0].Source != "organism" {
		t.Fatalf("declared jobs become routines: %+v", all)
	}
	// My owner pauses one; a new generation changes its schedule and drops the other.
	for _, r := range all {
		if r.Name == "send-reminders" {
			r.Enabled = false
			_ = s.Store.SaveRoutine(ctx, r)
		}
	}
	write("jobs:\n  - name: send-reminders\n    schedule: every 2h\n    path: /internal/jobs/reminders\n")
	s.SyncJobs(ctx)
	all, _ = s.Store.Routines(ctx)
	if len(all) != 1 || all[0].Schedule != "every 2h" || all[0].Enabled {
		t.Fatalf("sync keeps my owner's pause and follows the code: %+v", all)
	}
	// An invalid file keeps what was there.
	write("jobs:\n  - name: x\n    schedule: often\n    path: /a\n")
	s.SyncJobs(ctx)
	if all, _ := s.Store.Routines(ctx); len(all) != 1 {
		t.Fatal("an invalid file must not wipe the jobs")
	}
}

func TestCreateRoutineToolNeedsApprovalEveryTime(t *testing.T) {
	s, _, _ := testScheduler(t)
	c := &Chat{Store: s.Store, Routines: s}
	var create = c.routineTools()[1]
	in := json.RawMessage(`{"name":"weekly-digest","schedule":"0 9 * * 1","timezone":"Europe/Lisbon","prompt":"Summarize last week."}`)
	level, action := create.Classify(in)
	if level != permissions.Dangerous || !create.AskEveryTime || !strings.Contains(action, "every Monday at 09:00") {
		t.Fatalf("creating a routine needs my owner, every time, in words: %v %q", level, action)
	}
	if out, err := create.Run(context.Background(), in); err != nil || !strings.Contains(out, "weekly-digest") {
		t.Fatalf("%s %v", out, err)
	}
}
