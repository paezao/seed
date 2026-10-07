package evolution

import (
	"context"
	"os/exec"
	"sync"

	"seed/kernel/events"
	"seed/kernel/memory"
	"seed/kernel/permissions"
)

// Approvals is the queue of actions waiting for the owner. It implements
// permissions.Approver by persisting a request, broadcasting it to the
// control plane, and blocking until the owner decides.
type Approvals struct {
	Store *memory.Store
	Bus   *events.Bus

	mu      sync.Mutex
	waiters map[string]chan bool
}

func (a *Approvals) Approve(ctx context.Context, req permissions.Request) (bool, error) {
	ap := &memory.Approval{EvolutionID: req.EvolutionID, Action: req.Action, Level: req.Level.String(), Detail: req.Detail}
	if err := a.Store.CreateApproval(ctx, ap); err != nil {
		return false, err
	}
	ch := make(chan bool, 1)
	a.mu.Lock()
	if a.waiters == nil {
		a.waiters = map[string]chan bool{}
	}
	a.waiters[ap.ID] = ch
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		delete(a.waiters, ap.ID)
		a.mu.Unlock()
	}()
	a.Bus.Publish("approval", ap)
	select {
	case ok := <-ch:
		return ok, nil
	case <-ctx.Done():
		if d, err := a.Store.DecideApproval(context.WithoutCancel(ctx), ap.ID, false); err == nil {
			a.Bus.Publish("approval", d)
		}
		return false, ctx.Err()
	}
}

// Decide records the owner's decision and wakes the waiting evolution.
func (a *Approvals) Decide(ctx context.Context, id string, approved bool) (*memory.Approval, error) {
	ap, err := a.Store.DecideApproval(ctx, id, approved)
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	ch := a.waiters[id]
	a.mu.Unlock()
	if ch != nil {
		ch <- approved
	}
	a.Bus.Publish("approval", ap)
	return ap, nil
}

func dockerRm(ctx context.Context, name string) ([]byte, error) {
	return exec.CommandContext(ctx, "docker", "rm", "-f", name).CombinedOutput()
}
