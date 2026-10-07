package memory

import (
	"context"
	"testing"

	"seed/kernel/testutil"
)

func TestTransitions(t *testing.T) {
	happy := []Status{Requested, Planning, Planned, Mutating, Building, Testing, Running, Observing, Reflecting, Ready, Applying, Complete}
	for i := 1; i < len(happy); i++ {
		if !CanTransition(happy[i-1], happy[i]) {
			t.Errorf("%s -> %s should be allowed", happy[i-1], happy[i])
		}
	}
	repairs := []Status{Building, Testing, Running, Observing}
	for _, s := range repairs {
		if !CanTransition(s, Mutating) {
			t.Errorf("%s -> mutating (repair) should be allowed", s)
		}
	}
	illegal := [][2]Status{
		{Requested, Mutating}, {Planning, Complete}, {Mutating, Complete}, {Reflecting, Applying},
		{Complete, Planning}, {Failed, Mutating}, {Ready, Mutating}, {Testing, Reflecting},
	}
	for _, p := range illegal {
		if CanTransition(p[0], p[1]) {
			t.Errorf("%s -> %s should be illegal", p[0], p[1])
		}
	}
	for _, s := range ActiveStates() {
		if s != NeedsInput && !CanTransition(s, Failed) {
			t.Errorf("%s -> failed should be allowed", s)
		}
	}
	if !CanTransition(Mutating, NeedsInput) || !CanTransition(NeedsInput, Mutating) {
		t.Error("needs_input round trip should be allowed")
	}
	if !CanTransition(Applying, RolledBack) || CanTransition(Mutating, RolledBack) {
		t.Error("only applying may roll back")
	}
	e := &Evolution{ID: "evo_x", Status: Complete}
	if err := e.Transition(Planning); err == nil {
		t.Error("terminal evolutions must not transition")
	}
}

func TestStoreRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, testutil.Database(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	m, err := s.AddMessage(ctx, DefaultConversation, "user", "become a todo app", "")
	if err != nil {
		t.Fatal(err)
	}
	e := &Evolution{ConversationID: DefaultConversation, Intent: "become a todo app", BaseGeneration: 1}
	if err := s.CreateEvolution(ctx, e); err != nil {
		t.Fatal(err)
	}
	_ = e.Transition(Planning)
	e.Plan = &Plan{Title: "Todo", Steps: []PlanStep{{Title: "tasks table"}}}
	n := 3
	e.Checks = append(e.Checks, Check{Name: "test", OK: true, Attempt: 1, TestsPassed: &n})
	if err := s.SaveEvolution(ctx, e); err != nil {
		t.Fatal(err)
	}
	got, err := s.Evolution(ctx, e.ID)
	if err != nil || got.Status != Planning || got.Plan.Title != "Todo" || *got.Checks[0].TestsPassed != 3 {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	active, _ := s.EvolutionsByStatus(ctx, ActiveStates()...)
	if len(active) != 1 {
		t.Fatalf("expected 1 active evolution, got %d", len(active))
	}
	if _, err := s.AddEvent(ctx, e.ID, "phase", "planning", map[string]string{"a": "b"}); err != nil {
		t.Fatal(err)
	}
	evs, _ := s.Events(ctx, e.ID)
	if len(evs) != 1 || evs[0].Data.(map[string]any)["a"] != "b" {
		t.Fatalf("events: %+v", evs)
	}
	msgs, _ := s.Messages(ctx, DefaultConversation, 10)
	if len(msgs) != 1 || msgs[0].ID != m.ID {
		t.Fatalf("messages: %+v", msgs)
	}

	_ = s.AddGeneration(ctx, &Generation{Number: 1, Title: "Initial seed", Commit: "aaa"})
	_ = s.AddGeneration(ctx, &Generation{Number: 2, Title: "Todo", Commit: "bbb", EvolutionID: e.ID})
	cur, _ := s.CurrentGeneration(ctx)
	if cur.Number != 2 || !cur.Current {
		t.Fatalf("current generation: %+v", cur)
	}

	a := &Approval{EvolutionID: e.ID, Action: "write kernel/x.go", Level: "dangerous"}
	_ = s.CreateApproval(ctx, a)
	if _, err := s.DecideApproval(ctx, a.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DecideApproval(ctx, a.ID, false); err != ErrNotFound {
		t.Fatal("approvals can only be decided once")
	}
}
