package permissions

import (
	"context"
	"errors"
	"testing"
)

type fakeApprover struct {
	answer bool
	calls  int
}

func (f *fakeApprover) Approve(context.Context, Request) (bool, error) {
	f.calls++
	return f.answer, nil
}

func TestIsProtected(t *testing.T) {
	p := NewPolicy("allow", "allow", "ask", []string{"kernel/", "go.mod", "seed.yaml"})
	cases := map[string]bool{
		"kernel/agent/loop.go":     true,
		"kernel":                   true,
		"./kernel/x.go":            true,
		"organism/../kernel/x.go":  true,
		"go.mod":                   true,
		"organism/go.mod":          false,
		"kernelish/x":              false,
		"organism/backend/main.go": false,
		"seed.yaml":                true,
	}
	for in, want := range cases {
		if got := p.IsProtected(in); got != want {
			t.Errorf("IsProtected(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestCheckDecisions(t *testing.T) {
	ctx := context.Background()
	p := NewPolicy("allow", "deny", "ask", nil)
	if err := Check(ctx, p, nil, Request{Level: Safe, Action: "read"}); err != nil {
		t.Fatalf("safe should be allowed: %v", err)
	}
	if err := Check(ctx, p, nil, Request{Level: Review, Action: "write"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("review should be denied, got %v", err)
	}
	if err := Check(ctx, p, nil, Request{Level: Dangerous, Action: "kernel"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("ask without approver should be denied, got %v", err)
	}
	a := &fakeApprover{answer: false}
	if err := Check(ctx, p, a, Request{Level: Dangerous, Action: "kernel"}); !errors.Is(err, ErrDenied) {
		t.Fatalf("denied approval should deny, got %v", err)
	}
	a.answer = true
	if err := Check(ctx, p, a, Request{Level: Dangerous, Action: "kernel"}); err != nil {
		t.Fatalf("approved should pass: %v", err)
	}
}

func TestGrantsRemember(t *testing.T) {
	a := &fakeApprover{answer: true}
	g := NewGrants(a)
	for i := 0; i < 3; i++ {
		ok, _ := g.Approve(context.Background(), Request{Action: "write kernel/x.go"})
		if !ok {
			t.Fatal("expected approval")
		}
	}
	if a.calls != 1 {
		t.Fatalf("expected 1 call to inner approver, got %d", a.calls)
	}
}
