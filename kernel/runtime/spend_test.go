package runtime

import (
	"context"
	"strings"
	"testing"
	"time"

	"seed/kernel/events"
	"seed/kernel/memory"
	"seed/kernel/models"
)

func TestSpendingAndBudget(t *testing.T) {
	k, _ := ownerKernel(t)
	k.Bus = events.NewBus()
	ctx := context.Background()
	info := models.Info{Provider: "openrouter", Name: "anthropic/claude-sonnet-5.5"}
	call := func(kind, ref string, cost float64) {
		k.meter(models.WithPurpose(ctx, models.Purpose{Kind: kind, Ref: ref}), info, models.Usage{InputTokens: 100, OutputTokens: 10, CostUSD: cost, Priced: true})
	}
	call("evolution", "evo_1", 1.25)
	call("evolution", "evo_1", 0.75)
	call("chat", "", 0.5)
	k.meter(ctx, info, models.Usage{InputTokens: 5}) // no purpose, no price

	rep, err := k.Store.SpendReportSince(ctx, monthStart(time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	if rep.Total != 2.5 || rep.Calls != 4 || rep.Unpriced != 1 || len(rep.ByDay) != 1 {
		t.Fatalf("report: %+v", rep)
	}
	if rep.ByKind[0].Key != "evolution" || rep.ByKind[0].Cost != 2 || rep.Top[0].Ref != "evo_1" || rep.Top[0].Calls != 2 {
		t.Fatalf("by kind / top: %+v %+v", rep.ByKind, rep.Top)
	}

	if k.spendPaused(ctx) != "" {
		t.Fatal("no budget: never paused")
	}
	_ = k.Store.SetSetting(ctx, settingBudget, "2.00")
	if !strings.Contains(k.spendPaused(ctx), "$2.00") {
		t.Fatal("budget spent: paused")
	}
	call("chat", "", 0.1) // my owner's chat still happens, and I tell them once
	call("chat", "", 0.1)
	msgs, _ := k.Store.Messages(ctx, memory.DefaultConversation, 10)
	if len(msgs) != 1 || !strings.Contains(msgs[0].Content, "this month's budget of **$2.00**") {
		t.Fatalf("one note about the budget: %+v", msgs)
	}
	_ = k.Store.SetSetting(ctx, settingBudget, "10.00")
	if k.spendPaused(ctx) != "" {
		t.Fatal("a bigger budget: not paused")
	}
	if st := k.spendStatus(ctx); st.Paused || st.BudgetUSD != 10 || st.MonthUSD < 2.69 || st.MonthUSD > 2.71 {
		t.Fatalf("status: %+v", st)
	}
}

func TestScheduledAgentRoutinesWaitWhenPaused(t *testing.T) {
	s, ag, _ := testScheduler(t)
	ctx := context.Background()
	r := &memory.Routine{Name: "daily-summary", Kind: "agent", Schedule: "every 1h", Prompt: "Summarize."}
	if err := s.Create(ctx, r); err != nil {
		t.Fatal(err)
	}
	s.Paused = func(context.Context) string { return "Paused: budget spent." }
	s.now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	s.Tick(ctx)
	if run := waitRun(t, s, r.ID); run.Status != "skipped" || !strings.Contains(run.Output, "budget") {
		t.Fatalf("scheduled run while paused: %+v", run)
	}
	if len(ag.runs) != 0 {
		t.Fatal("no model calls while paused")
	}
	// My owner can still run it.
	if _, err := s.Launch(ctx, r, "manual"); err != nil {
		t.Fatal(err)
	}
	if run := waitRun(t, s, r.ID); run.Status != "ok" {
		t.Fatalf("manual run while paused: %+v", run)
	}
}
