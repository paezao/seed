package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Scripted is a deterministic Model for tests. Each Generate call pops the
// next step. A step may inspect the request (to assert on context) and return
// a response or an error.
type Scripted struct {
	mu       sync.Mutex
	Steps    []func(Request) (*Response, error)
	Requests []Request
}

func (s *Scripted) Name() string { return "scripted" }

func (s *Scripted) Generate(_ context.Context, req Request) (*Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, req)
	if len(s.Steps) == 0 {
		return nil, errors.New("scripted model: no more steps")
	}
	step := s.Steps[0]
	s.Steps = s.Steps[1:]
	return step(req)
}

// Remaining reports how many steps were not consumed.
func (s *Scripted) Remaining() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.Steps)
}

// Say returns a step that answers with text and no tool calls.
func Say(text string) func(Request) (*Response, error) {
	return func(Request) (*Response, error) { return &Response{Text: text, StopReason: "end_turn"}, nil }
}

var callSeq int
var callMu sync.Mutex

// Call returns a step that calls one tool with the given input (marshalled to JSON).
func Call(name string, input any) func(Request) (*Response, error) {
	return Calls(Tc(name, input))
}

// Tc builds a tool call with a unique id.
func Tc(name string, input any) ToolCall {
	callMu.Lock()
	callSeq++
	id := fmt.Sprintf("call_%d", callSeq)
	callMu.Unlock()
	var raw json.RawMessage
	switch v := input.(type) {
	case string:
		raw = json.RawMessage(v)
	default:
		raw, _ = json.Marshal(v)
	}
	return ToolCall{ID: id, Name: name, Input: raw}
}

// Calls returns a step that issues several tool calls in one turn.
func Calls(calls ...ToolCall) func(Request) (*Response, error) {
	return func(Request) (*Response, error) {
		return &Response{ToolCalls: calls, StopReason: "tool_use"}, nil
	}
}

// Fail returns a step that fails with err.
func Fail(err error) func(Request) (*Response, error) {
	return func(Request) (*Response, error) { return nil, err }
}
