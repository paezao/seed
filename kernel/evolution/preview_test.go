package evolution

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"seed/kernel/config"
	"seed/kernel/memory"
	"seed/kernel/models"
)

func previewHarness(t *testing.T) *harness {
	h := newHarness(t, func(cfg *config.Config) {})
	h.o.PreviewOn = func(context.Context) bool { return true }
	return h
}

// owner waits for a preview, checks it, and decides.
func owner(t *testing.T, h *harness, decisions ...Decision) {
	t.Helper()
	go func() {
		for _, d := range decisions {
			ok := false
			for i := 0; i < 800 && !ok; i++ {
				evs, _ := h.o.Store.EvolutionsByStatus(context.Background(), memory.Ready)
				for _, e := range evs {
					if e.Preview != nil && (e.Preview.State == "ready" || e.Preview.State == "failed") {
						if _, running := h.o.PreviewSandbox(e.ID); !running {
							continue
						}
						if err := h.o.Decide(e.ID, d); err != nil {
							t.Errorf("decide: %v", err)
						}
						ok = true
						break
					}
				}
				time.Sleep(25 * time.Millisecond)
			}
			if !ok {
				t.Error("never got to preview")
				return
			}
			time.Sleep(100 * time.Millisecond) // let it move past this round
		}
	}()
}

func TestPreviewThenApply(t *testing.T) {
	h := previewHarness(t)
	owner(t, h, Decision{Action: "apply"})
	steps := []step{
		planStep(),
		models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v1"}),
		finishStep(),
	}
	e := h.run("become a thing tracker", append(steps, reflectSteps()...)...)
	if e.Status != memory.Complete || e.Preview == nil || e.Preview.State != "done" || e.Preview.Data != "copy" {
		t.Fatalf("applied after the preview, on a copy of the live data: %s %q %+v", e.Status, e.Error, e.Preview)
	}
	if h.head() != e.Commit || h.live.deploys != 1 {
		t.Fatal("it went live")
	}
	msgs, _ := h.o.Store.Messages(context.Background(), memory.DefaultConversation, 20)
	found := false
	for _, m := range msgs {
		if strings.Contains(m.Content, "ready to try before it goes live") {
			found = true
		}
	}
	if !found {
		t.Fatal("my owner is told it's ready to try")
	}
	if _, running := h.o.PreviewSandbox(e.ID); running {
		t.Fatal("the preview stops once decided")
	}
}

func TestPreviewChangesThenApply(t *testing.T) {
	h := previewHarness(t)
	owner(t, h, Decision{Action: "changes", Feedback: "Make the title bigger."}, Decision{Action: "apply"})
	steps := []step{
		planStep(),
		models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v1"}),
		finishStep(),
	}
	steps = append(steps, reflectSteps()...)
	steps = append(steps,
		func(r models.Request) (*models.Response, error) {
			last := r.Messages[len(r.Messages)-1]
			if !strings.Contains(last.Content, "Make the title bigger.") {
				return nil, errors.New("the owner's words should come back to me: " + last.Content)
			}
			return models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v2 with a bigger title"})(r)
		},
		finishStep(),
	)
	steps = append(steps, reflectSteps()...)
	e := h.run("become a thing tracker", steps...)
	if e.Status != memory.Complete || e.Preview.Round != 1 {
		t.Fatalf("applied after one round of changes: %s %q %+v", e.Status, e.Error, e.Preview)
	}
	logs, _ := h.repo.Log(context.Background(), "HEAD", 3)
	if logs[1].Hash != h.base {
		t.Fatal("the change is still one generation (one commit on top of the base)")
	}
	b, _ := h.repo.Run(context.Background(), "show", "HEAD:organism/app.txt")
	if !strings.Contains(b, "bigger title") {
		t.Fatalf("the generation has the requested change: %q", b)
	}
}

func TestPreviewDiscard(t *testing.T) {
	h := previewHarness(t)
	owner(t, h, Decision{Action: "discard"})
	steps := []step{planStep(), models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "v1"}), finishStep()}
	e := h.run("become a thing tracker", append(steps, reflectSteps()...)...)
	if e.Status != memory.Cancelled || h.head() != h.base || h.live.deploys != 0 {
		t.Fatalf("discarded: nothing changes: %s head %s deploys %d", e.Status, h.head(), h.live.deploys)
	}
}

func TestNoPreviewWhenSkipped(t *testing.T) {
	h := previewHarness(t)
	h.model.Steps = append([]step{planStep(), models.Call("write_file", map[string]string{"path": "organism/app.txt", "content": "fix"}), finishStep()}, reflectSteps()...)
	ctx := context.Background()
	e, err := h.o.RequestWithoutPreview(ctx, memory.DefaultConversation, "fix it")
	if err != nil {
		t.Fatal(err)
	}
	queued, _ := h.o.Store.EvolutionsByStatus(ctx, memory.Requested)
	h.o.process(ctx, queued[0])
	got, _ := h.o.Store.Evolution(ctx, e.ID)
	if got.Status != memory.Complete {
		t.Fatalf("a fix I start on my own goes live without waiting: %s %q", got.Status, got.Error)
	}
}

func TestDecideValidates(t *testing.T) {
	h := previewHarness(t)
	if err := h.o.Decide("evo_nope", Decision{Action: "apply"}); err == nil {
		t.Fatal("only a waiting evolution can be decided")
	}
	h.o.previews["evo_x"] = &previewRun{ch: make(chan Decision, 1)}
	if err := h.o.Decide("evo_x", Decision{Action: "changes"}); err == nil {
		t.Fatal("changes need words")
	}
	if err := h.o.Decide("evo_x", Decision{Action: "apply"}); err != nil {
		t.Fatal(err)
	}
	if err := h.o.Decide("evo_x", Decision{Action: "discard"}); err == nil {
		t.Fatal("one decision per preview")
	}
}
