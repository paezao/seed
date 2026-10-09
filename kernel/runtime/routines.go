package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"seed/kernel/agent"
	"seed/kernel/events"
	"seed/kernel/knowledge"
	"seed/kernel/memory"
	"seed/kernel/permissions"
	"seed/kernel/routines"
	"seed/kernel/skills"
	"seed/kernel/tools"
)

// Scheduler runs my routines: agent routines (a prompt I run with my chat
// tools, reporting to my owner) and jobs (a request to my organism's own
// endpoint). Schedules are kept in memory; my organism's jobs are synced
// from organism/routines.yaml whenever a generation goes live.
type Scheduler struct {
	Root  string
	Store *memory.Store
	Bus   *events.Bus
	Agent interface {
		RunRoutine(ctx context.Context, r *memory.Routine) (string, error)
	}
	Jobs interface {
		CallJob(ctx context.Context, name, method, path string) (int, string, error)
	}
	// OnJob hears how each job run went (my doctor watches for failing jobs).
	OnJob func(name string, ok bool, output string)

	mu      sync.Mutex
	running map[string]bool
	agent   chan struct{} // one agent routine at a time (they cost tokens)
	jobs    chan struct{}
	now     func() time.Time
}

const (
	maxAgentRoutines = 20
	agentRunTimeout  = 30 * time.Minute // includes waiting for an approval
	jobRunTimeout    = 5 * time.Minute
	nothingToReport  = "NOTHING_TO_REPORT"
)

func (s *Scheduler) init() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running == nil {
		s.running = map[string]bool{}
		s.agent = make(chan struct{}, 1)
		s.jobs = make(chan struct{}, 4)
	}
	if s.now == nil {
		s.now = time.Now
	}
}

// Start runs the scheduler until ctx ends.
func (s *Scheduler) Start(ctx context.Context) {
	s.init()
	if err := s.Store.FailInterruptedRuns(ctx); err != nil {
		slog.Warn("routines", "err", err)
	}
	s.SyncJobs(ctx)
	if all, err := s.Store.Routines(ctx); err == nil {
		for _, r := range all {
			if r.Enabled && r.NextRunAt == nil {
				s.schedule(ctx, r)
			}
		}
	}
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		s.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}

// Tick starts every due routine. A routine missed while I was stopped runs
// once, not once per missed time.
func (s *Scheduler) Tick(ctx context.Context) {
	s.init()
	due, err := s.Store.DueRoutines(ctx, s.now())
	if err != nil {
		slog.Warn("routines", "err", err)
		return
	}
	for _, r := range due {
		if _, err := s.Launch(ctx, r, "schedule"); err != nil && !errors.Is(err, errAlreadyRunning) {
			slog.Warn("routine", "name", r.Name, "err", err)
		}
	}
}

// schedule sets a routine's next run from now (nil when paused).
func (s *Scheduler) schedule(ctx context.Context, r *memory.Routine) {
	var next *time.Time
	if r.Enabled {
		if sch, err := routines.Parse(r.Schedule, r.Timezone); err == nil {
			if t := sch.Next(s.now()); !t.IsZero() {
				next = &t
			}
		}
	}
	r.NextRunAt = next
	_ = s.Store.SetRoutineNextRun(ctx, r.ID, next)
}

var errAlreadyRunning = errors.New("this routine is already running")

// Launch starts a run now. The next scheduled run is set first, so a long
// run is never started twice.
func (s *Scheduler) Launch(ctx context.Context, r *memory.Routine, trigger string) (*memory.RoutineRun, error) {
	s.init()
	s.mu.Lock()
	if s.running[r.ID] {
		s.mu.Unlock()
		return nil, errAlreadyRunning
	}
	s.running[r.ID] = true
	s.mu.Unlock()
	if trigger == "schedule" {
		s.schedule(ctx, r)
	}
	run, err := s.Store.StartRoutineRun(ctx, r.ID, trigger)
	if err != nil {
		s.done(r.ID)
		return nil, err
	}
	s.Bus.Publish("routine", map[string]any{"id": r.ID, "run": run})
	go s.run(context.WithoutCancel(ctx), r, run)
	return run, nil
}

func (s *Scheduler) done(id string) {
	s.mu.Lock()
	delete(s.running, id)
	s.mu.Unlock()
}

func (s *Scheduler) run(ctx context.Context, r *memory.Routine, run *memory.RoutineRun) {
	defer s.done(r.ID)
	switch r.Kind {
	case routines.KindAgent:
		s.agent <- struct{}{}
		runCtx, cancel := context.WithTimeout(ctx, agentRunTimeout)
		text, err := s.Agent.RunRoutine(runCtx, r)
		cancel()
		<-s.agent
		switch {
		case err != nil:
			run.Status, run.Output = "failed", err.Error()
		case strings.Contains(text, nothingToReport) || strings.TrimSpace(text) == "":
			run.Status, run.Output = "quiet", "Nothing to report."
		default:
			run.Status, run.Output = "ok", text
			if m, err := s.Store.AddRoutineReport(ctx, memory.DefaultConversation, "**Routine · "+r.Name+"**\n\n"+text); err != nil {
				slog.Warn("routine report", "name", r.Name, "err", err)
			} else {
				s.Bus.Publish("message", m)
			}
		}
	case routines.KindJob:
		s.jobs <- struct{}{}
		runCtx, cancel := context.WithTimeout(ctx, jobRunTimeout)
		status, body, err := s.Jobs.CallJob(runCtx, r.Name, r.Method, r.Path)
		cancel()
		<-s.jobs
		switch {
		case err != nil:
			run.Status, run.Output = "failed", err.Error()
		case status < 200 || status > 299:
			run.Status, run.Output = "failed", fmt.Sprintf("HTTP %d\n%s", status, tools.Truncate(body, 4000))
		default:
			run.Status, run.Output = "ok", fmt.Sprintf("HTTP %d\n%s", status, tools.Truncate(body, 4000))
		}
		if s.OnJob != nil {
			s.OnJob(r.Name, run.Status == "ok", run.Output)
		}
	default:
		run.Status, run.Output = "failed", "unknown routine kind "+r.Kind
	}
	if err := s.Store.FinishRoutineRun(ctx, run); err != nil {
		slog.Warn("routine", "name", r.Name, "err", err)
	}
	s.Bus.Publish("routine", map[string]any{"id": r.ID, "run": run})
}

// SyncJobs makes my organism's declared jobs (organism/routines.yaml in the
// live checkout) my organism-sourced routines, keeping whether my owner
// paused each one.
func (s *Scheduler) SyncJobs(ctx context.Context) {
	s.init()
	jobs, err := routines.LoadFile(s.Root)
	if err != nil {
		slog.Warn("my organism's jobs are invalid; keeping the previous ones", "err", err)
		return
	}
	all, err := s.Store.Routines(ctx)
	if err != nil {
		return
	}
	have := map[string]*memory.Routine{}
	for _, r := range all {
		if r.Source == "organism" {
			have[r.Name] = r
		}
	}
	for _, j := range jobs {
		r := have[j.Name]
		delete(have, j.Name)
		tz := j.Timezone
		if tz == "" {
			tz = "UTC"
		}
		if r == nil {
			r = &memory.Routine{Name: j.Name, Kind: routines.KindJob, Source: "organism", Enabled: true}
		} else if r.Schedule == j.Schedule && r.Timezone == tz && r.Method == j.Method && r.Path == j.Path && r.Description == j.Description {
			continue
		}
		r.Schedule, r.Timezone, r.Method, r.Path, r.Description = j.Schedule, tz, j.Method, j.Path, j.Description
		if err := s.Store.SaveRoutine(ctx, r); err != nil {
			slog.Warn("routine", "name", j.Name, "err", err)
			continue
		}
		s.schedule(ctx, r)
	}
	for _, gone := range have {
		_ = s.Store.DeleteRoutine(ctx, gone.ID)
	}
	s.Bus.Publish("routine", map[string]any{"synced": true})
}

// Create validates and saves a routine my owner made.
func (s *Scheduler) Create(ctx context.Context, r *memory.Routine) error {
	s.init()
	r.Name = strings.TrimSpace(r.Name)
	if err := routines.ValidateName(r.Name); err != nil {
		return err
	}
	if r.Timezone == "" {
		r.Timezone = "UTC"
	}
	if _, err := routines.ValidateSchedule(r.Kind, r.Schedule, r.Timezone); err != nil {
		return err
	}
	all, err := s.Store.Routines(ctx)
	if err != nil {
		return err
	}
	agents := 0
	for _, x := range all {
		if x.Source == "owner" && x.Name == r.Name {
			return fmt.Errorf("there is already a routine named %s", r.Name)
		}
		if x.Kind == routines.KindAgent {
			agents++
		}
	}
	switch r.Kind {
	case routines.KindAgent:
		r.Prompt = strings.TrimSpace(r.Prompt)
		if r.Prompt == "" || len(r.Prompt) > 4000 {
			return errors.New("say what I should do (up to 4000 characters)")
		}
		if agents >= maxAgentRoutines {
			return fmt.Errorf("at most %d agent routines", maxAgentRoutines)
		}
		r.Method, r.Path = "", ""
	case routines.KindJob:
		m, err := routines.ValidateRequest(r.Method, r.Path)
		if err != nil {
			return err
		}
		r.Method, r.Prompt = m, ""
	default:
		return errors.New("kind must be agent or job")
	}
	r.ID, r.Source, r.Enabled = "", "owner", true
	if err := s.Store.SaveRoutine(ctx, r); err != nil {
		return err
	}
	s.schedule(ctx, r)
	s.Bus.Publish("routine", map[string]any{"id": r.ID})
	return nil
}

// Describe tells me (the agent) about my routines and how to add jobs.
func (s *Scheduler) Describe(ctx context.Context) string {
	var sb strings.Builder
	sb.WriteString("\n## My routines\n")
	sb.WriteString("Agent routines are prompts I run on a schedule with my chat tools; I report to my owner. Jobs call my organism's own endpoints on a schedule: " +
		"to add one, write the endpoint (with tests) and declare it in " + routines.File + " (jobs: - name, schedule (\"every 1h\" or cron \"0 8 * * *\"), path, optional method (POST), timezone, description). " +
		"The kernel calls it with header X-Seed-Job equal to the environment variable SEED_JOB_TOKEN: the endpoint must refuse calls without it.\n")
	all, err := s.Store.Routines(ctx)
	if err != nil || len(all) == 0 {
		sb.WriteString("- none yet\n")
		return sb.String()
	}
	for _, r := range all {
		state := "enabled"
		if !r.Enabled {
			state = "paused"
		}
		what := r.Method + " " + r.Path
		if r.Kind == routines.KindAgent {
			what = tools.Truncate(r.Prompt, 120)
		}
		fmt.Fprintf(&sb, "- %s (%s, from %s, %s, %s): %s\n", r.Name, r.Kind, r.Source, describeSchedule(r), state, what)
	}
	return sb.String()
}

func describeSchedule(r *memory.Routine) string {
	if sch, err := routines.Parse(r.Schedule, r.Timezone); err == nil {
		return sch.String() + " " + r.Timezone
	}
	return r.Schedule
}

// RunRoutine runs an agent routine: my chat agent, unattended, with a fresh
// conversation. It can read and call my organism; anything dangerous still
// waits for my owner's approval. It cannot evolve me or create routines.
func (c *Chat) RunRoutine(ctx context.Context, r *memory.Routine) (string, error) {
	reg := tools.NewRegistry(c.Policy, c.Approvals)
	ws := &tools.Workspace{Root: c.Root, Policy: c.Policy}
	reg.Add(tools.ReadOnlyFileTools(ws)...).Add(tools.SkillTools(skills.Library{Root: c.Root})...)
	reg.Add(c.opsTools()...)
	if c.Egress != nil {
		reg.Add(c.Egress.RequestTool())
	}
	system := agent.PromptFor("chat", knowledge.Name(c.Root)) + "\n\n" + c.Orch.SelfContext(ctx, c.Root) +
		"\n\n# Now\nI am running my routine \"" + r.Name + "\" on my own; my owner is not watching. " +
		"I do the task with my tools, then write a short report for my owner (it is posted in our chat). " +
		"If there is nothing worth telling them, I reply with exactly " + nothingToReport + ". " +
		"I cannot evolve myself from a routine; if the task needs that, I say so in the report."
	a := &agent.Agent{Model: c.Model, Tools: reg, MaxTurns: 15, MaxTokens: 4000, System: system}
	out, err := a.Run(ctx, toModelMessages([]memory.Message{{Role: "user", Content: "Routine task:\n\n" + r.Prompt}}))
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out.Text), nil
}

// routineTools let the chat show and create routines (creating needs my
// owner's approval, every time: a routine acts and spends on its own).
func (c *Chat) routineTools() []*tools.Tool {
	if c.Routines == nil {
		return nil
	}
	sched := c.Routines
	type createArgs struct {
		Name, Schedule, Timezone, Prompt string
	}
	parse := func(in json.RawMessage) (*memory.Routine, error) {
		var a createArgs
		if err := json.Unmarshal(in, &a); err != nil {
			return nil, err
		}
		r := &memory.Routine{Name: strings.TrimSpace(a.Name), Kind: routines.KindAgent, Schedule: a.Schedule, Timezone: a.Timezone, Prompt: a.Prompt}
		if r.Timezone == "" {
			r.Timezone = "UTC"
		}
		if err := routines.ValidateName(r.Name); err != nil {
			return nil, err
		}
		sch, err := routines.ValidateSchedule(r.Kind, r.Schedule, r.Timezone)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(r.Prompt) == "" {
			return nil, errors.New("prompt is required")
		}
		r.Description = sch.String()
		return r, nil
	}
	return []*tools.Tool{
		{
			Name:        "list_routines",
			Description: "List my routines (agent routines and my organism's jobs) with their schedules and last results.",
			Schema:      tools.Schema(tools.Props{}),
			Classify:    tools.Fixed(permissions.Safe, "list routines"),
			Run: func(ctx context.Context, _ json.RawMessage) (string, error) {
				all, err := c.Store.Routines(ctx)
				if err != nil {
					return "", err
				}
				var sb strings.Builder
				for _, r := range all {
					fmt.Fprintf(&sb, "- %s [%s, %s, enabled=%v] %s", r.Name, r.Kind, describeSchedule(r), r.Enabled, r.Prompt+r.Method+" "+r.Path)
					if runs, err := c.Store.RoutineRuns(ctx, r.ID, 1); err == nil && len(runs) > 0 {
						fmt.Fprintf(&sb, " — last run %s: %s", runs[0].Status, tools.Truncate(runs[0].Output, 200))
					}
					sb.WriteString("\n")
				}
				if sb.Len() == 0 {
					return "no routines yet", nil
				}
				return sb.String(), nil
			},
		},
		{
			Name: "create_routine",
			Description: "Create an agent routine: a task I do on my own on a schedule and report on in this chat (e.g. a daily summary, a periodic check). " +
				"For work my app's own code should do on a schedule (sending emails, cleanups), evolve a job instead. My owner approves every routine.",
			Schema: tools.Schema(tools.Props{
				"name":     tools.Str("short-kebab-case name, e.g. daily-summary"),
				"schedule": tools.Str(`"every 15m" / "every 2h" / "every 1d", or cron like "0 8 * * 1" (at most every 15 minutes)`),
				"timezone": tools.Str("IANA timezone of the owner, e.g. Europe/Lisbon (default UTC)"),
				"prompt":   tools.Str("the task, self-contained: what to check or do and what to report"),
			}, "name", "schedule", "prompt"),
			AskEveryTime: true,
			Classify: func(in json.RawMessage) (permissions.Level, string) {
				r, err := parse(in)
				if err != nil {
					return permissions.Safe, "check routine"
				}
				return permissions.Dangerous, fmt.Sprintf("create the routine %s, %s (%s)", r.Name, r.Description, r.Timezone)
			},
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				r, err := parse(in)
				if err != nil {
					return "", err
				}
				r.Description = ""
				if err := sched.Create(ctx, r); err != nil {
					return "", err
				}
				return fmt.Sprintf("Routine %s created; next run %s.", r.Name, r.NextRunAt.Format(time.RFC1123)), nil
			},
		},
	}
}
