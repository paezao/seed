package runtime

import (
	"strings"
	"testing"

	"seed/kernel/events"
	"seed/kernel/memory"
	"seed/kernel/update"
)

func TestWhatGetsNotified(t *testing.T) {
	n := &Notifier{}
	kindOf := func(typ string, data any) string {
		k, m, ok := n.notificationFor(events.Event{Type: typ, Data: data})
		if !ok {
			return ""
		}
		if !strings.HasPrefix(m.URL, "/_seed/") || m.Title == "" {
			t.Fatalf("a notification opens my control plane and says something: %+v", m)
		}
		return k
	}
	e := &memory.Evolution{ID: "evo_1", Kind: "evolve", Intent: "Make it green", Status: memory.Mutating}
	if kindOf("evolution", e) != "" {
		t.Fatal("work in progress: nothing to say")
	}
	e.Status, e.Preview = memory.Ready, &memory.Preview{State: "ready"}
	if kindOf("evolution", e) != "needs_you" {
		t.Fatal("ready to try needs my owner")
	}
	if kindOf("evolution", e) != "" {
		t.Fatal("once per state, however often it's published")
	}
	e.Status, e.Preview = memory.Mutating, nil // my owner asked for changes
	kindOf("evolution", e)
	e.Status, e.Preview = memory.Ready, &memory.Preview{State: "ready"}
	if kindOf("evolution", e) != "needs_you" {
		t.Fatal("ready to try again, after changes")
	}
	gen := 7
	e.Status, e.NewGeneration = memory.Complete, &gen
	if kindOf("evolution", e) != "evolutions" {
		t.Fatal("live")
	}
	q := &memory.Evolution{ID: "evo_2", Status: memory.NeedsInput, Questions: []memory.Question{{Question: "Which color?"}}}
	if kindOf("evolution", q) != "needs_you" {
		t.Fatal("a question needs my owner")
	}
	if kindOf("approval", &memory.Approval{ID: "a1", Status: "pending", Action: "delete 3 rows"}) != "needs_you" {
		t.Fatal("an approval needs my owner")
	}
	if kindOf("approval", &memory.Approval{ID: "a2", Status: "approved"}) != "" {
		t.Fatal("a decided approval: nothing")
	}
	if kindOf("incident", &memory.Incident{ID: "i1", Status: "diagnosing"}) != "" || kindOf("incident", &memory.Incident{ID: "i1", Status: "diagnosed", Title: "GET /a → 500"}) != "health" {
		t.Fatal("a diagnosed incident")
	}
	if kindOf("message", &memory.Message{ID: "m1", Role: "seed", Kind: "routine", Content: "**Routine · digest**\n\n3 new recipes"}) != "routines" {
		t.Fatal("routine reports")
	}
	if kindOf("message", &memory.Message{ID: "m2", Role: "seed", Kind: "chat", Content: "hi"}) != "" {
		t.Fatal("chat replies: no")
	}
	if kindOf("budget", budgetSpent{Month: "2026-10", Budget: 5}) != "budget" || kindOf("budget", budgetSpent{Month: "2026-10", Budget: 5}) != "" {
		t.Fatal("budget: once a month")
	}
	if kindOf("restore", map[string]string{"id": "b1", "state": "restoring"}) != "" || kindOf("restore", map[string]string{"id": "b1", "state": "done"}) != "backups" {
		t.Fatal("restores, when done")
	}
	ks := KernelStatus{Available: true, Latest: &update.Manifest{Version: "2026.11.01"}}
	if kindOf("kernel", ks) != "updates" || kindOf("kernel", ks) != "" {
		t.Fatal("an update, once per version")
	}
}
