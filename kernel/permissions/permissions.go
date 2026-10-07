// Package permissions classifies actions by risk and decides whether they may
// proceed. It is the trust boundary between the agent and the world.
package permissions

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"
)

type Level int

const (
	Safe Level = iota
	Review
	Dangerous
)

func (l Level) String() string {
	switch l {
	case Safe:
		return "safe"
	case Review:
		return "review"
	default:
		return "dangerous"
	}
}

type Decision string

const (
	Allow Decision = "allow"
	Ask   Decision = "ask"
	Deny  Decision = "deny"
)

// Policy maps risk levels to decisions.
type Policy struct {
	Decisions map[Level]Decision
	// Protected paths (kernel boundary). Directory entries end with "/".
	Protected []string
}

func NewPolicy(safe, review, dangerous string, protected []string) *Policy {
	return &Policy{
		Decisions: map[Level]Decision{Safe: Decision(safe), Review: Decision(review), Dangerous: Decision(dangerous)},
		Protected: protected,
	}
}

func (p *Policy) Decide(l Level) Decision {
	if p == nil {
		return Deny
	}
	d, ok := p.Decisions[l]
	if !ok {
		return Deny
	}
	return d
}

// IsProtected reports whether a repository-relative path is inside the kernel boundary.
func (p *Policy) IsProtected(rel string) bool {
	rel = strings.TrimPrefix(path.Clean("/"+strings.ReplaceAll(rel, "\\", "/")), "/")
	for _, pr := range p.Protected {
		if strings.HasSuffix(pr, "/") {
			dir := strings.TrimSuffix(pr, "/")
			if rel == dir || strings.HasPrefix(rel, dir+"/") {
				return true
			}
		} else if rel == pr {
			return true
		}
	}
	return false
}

// ProtectedIn returns the protected paths among the given ones.
func (p *Policy) ProtectedIn(paths []string) []string {
	var out []string
	for _, f := range paths {
		if p.IsProtected(f) {
			out = append(out, f)
		}
	}
	return out
}

// Request describes an action awaiting a human decision.
type Request struct {
	EvolutionID string
	Action      string
	Level       Level
	Detail      string
}

// Approver obtains a human decision. Implementations block until decided or
// the context ends.
type Approver interface {
	Approve(ctx context.Context, req Request) (bool, error)
}

var ErrDenied = errors.New("permission denied")

// Check applies the policy, consulting the approver when the decision is Ask.
func Check(ctx context.Context, p *Policy, a Approver, req Request) error {
	switch p.Decide(req.Level) {
	case Allow:
		return nil
	case Ask:
		if a == nil {
			return fmt.Errorf("%w: %s requires approval but no approver is available", ErrDenied, req.Action)
		}
		ok, err := a.Approve(ctx, req)
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: the owner denied %s", ErrDenied, req.Action)
		}
		return nil
	default:
		return fmt.Errorf("%w: %s is %s and the policy denies it", ErrDenied, req.Action, req.Level)
	}
}

// Grants remembers approvals within one evolution so the owner is not asked
// twice for the same action class (e.g. editing the same kernel file).
type Grants struct {
	inner   Approver
	granted map[string]bool
}

func NewGrants(inner Approver) *Grants { return &Grants{inner: inner, granted: map[string]bool{}} }

func (g *Grants) Approve(ctx context.Context, req Request) (bool, error) {
	if g.granted[req.Action] {
		return true, nil
	}
	ok, err := g.inner.Approve(ctx, req)
	if ok && err == nil {
		g.granted[req.Action] = true
	}
	return ok, err
}
