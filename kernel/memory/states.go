package memory

import "fmt"

// Status is an evolution's state in its lifecycle.
type Status string

const (
	Requested  Status = "requested"
	Planning   Status = "planning"
	Planned    Status = "planned"
	Mutating   Status = "mutating"
	Building   Status = "building"
	Testing    Status = "testing"
	Running    Status = "running"
	Observing  Status = "observing"
	Reflecting Status = "reflecting"
	Ready      Status = "ready"
	Applying   Status = "applying"
	Complete   Status = "complete"

	Failed     Status = "failed"
	NeedsInput Status = "needs_input"
	Cancelled  Status = "cancelled"
	RolledBack Status = "rolled_back"
)

// transitions lists the allowed forward edges of the state machine.
// Any active state may additionally move to failed, cancelled or
// needs_input, and needs_input may return to the state it interrupted.
var transitions = map[Status][]Status{
	Requested:  {Planning, Applying}, // rollbacks skip straight to applying
	Planning:   {Planned},
	Planned:    {Mutating},
	Mutating:   {Building},
	Building:   {Testing, Mutating},
	Testing:    {Running, Mutating},
	Running:    {Observing, Mutating},
	Observing:  {Reflecting, Mutating},
	Reflecting: {Ready},
	Ready:      {Applying, Mutating}, // back to mutating: the owner asked for changes after previewing
	Applying:   {Complete, RolledBack},
}

// Terminal reports whether s is a final state.
func (s Status) Terminal() bool {
	switch s {
	case Complete, Failed, Cancelled, RolledBack:
		return true
	}
	return false
}

// Active reports whether an evolution in s is in progress.
func (s Status) Active() bool { return !s.Terminal() }

// CanTransition reports whether from -> to is allowed.
func CanTransition(from, to Status) bool {
	if from.Terminal() {
		return false
	}
	switch to {
	case Failed, Cancelled, NeedsInput:
		return from != to
	}
	if from == NeedsInput {
		return to.Active() && to != Requested
	}
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// Transition moves e to state to, or returns an error if not allowed.
func (e *Evolution) Transition(to Status) error {
	if !CanTransition(e.Status, to) {
		return fmt.Errorf("evolution %s: illegal transition %s -> %s", e.ID, e.Status, to)
	}
	e.Status = to
	return nil
}

// ActiveStates lists all non-terminal states.
func ActiveStates() []Status {
	return []Status{Requested, Planning, Planned, Mutating, Building, Testing, Running, Observing, Reflecting, Ready, Applying, NeedsInput}
}
