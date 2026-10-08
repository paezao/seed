// Package tools defines the primitive capabilities the agent acts through.
//
// Every tool call passes through the Registry, which classifies its risk and
// enforces the permission policy before anything runs. Tools are deliberately
// primitive (read, write, run, request, query); higher-level abilities are
// built by the Seed itself as skills and code.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"seed/kernel/models"
	"seed/kernel/permissions"
)

// Tool is one capability exposed to the model.
type Tool struct {
	Name        string
	Description string
	Schema      json.RawMessage
	// Classify returns the risk level of a call and a short description of the
	// concrete action (e.g. "write organism/backend/main.go").
	Classify func(input json.RawMessage) (permissions.Level, string)
	Run      func(ctx context.Context, input json.RawMessage) (string, error)
	// Terminal tools end the agent loop when they succeed.
	Terminal bool
}

// Registry holds the tools available to one agent run.
type Registry struct {
	tools       map[string]*Tool
	order       []string
	Policy      *permissions.Policy
	Approver    permissions.Approver
	EvolutionID string
	// MaxOutput bounds the size of a tool result returned to the model.
	MaxOutput int
}

func NewRegistry(policy *permissions.Policy, approver permissions.Approver) *Registry {
	return &Registry{tools: map[string]*Tool{}, Policy: policy, Approver: approver, MaxOutput: 20000}
}

func (r *Registry) Add(ts ...*Tool) *Registry {
	for _, t := range ts {
		if _, dup := r.tools[t.Name]; !dup {
			r.order = append(r.order, t.Name)
		}
		r.tools[t.Name] = t
	}
	return r
}

func (r *Registry) Get(name string) *Tool { return r.tools[name] }

func (r *Registry) Names() []string {
	out := append([]string(nil), r.order...)
	sort.Strings(out)
	return out
}

// TerminalNames lists tools that end the loop.
func (r *Registry) TerminalNames() []string {
	var out []string
	for _, n := range r.order {
		if r.tools[n].Terminal {
			out = append(out, n)
		}
	}
	return out
}

func (r *Registry) Specs() []models.ToolSpec {
	specs := make([]models.ToolSpec, 0, len(r.order))
	for _, n := range r.order {
		t := r.tools[n]
		specs = append(specs, models.ToolSpec{Name: t.Name, Description: t.Description, Schema: t.Schema})
	}
	return specs
}

// maxReviewable bounds a dangerous request an owner is asked to approve.
const maxReviewable = 16 << 10

// Outcome is the result of executing one call.
type Outcome struct {
	Result   models.ToolResult
	Level    permissions.Level
	Action   string
	Terminal bool // a terminal tool succeeded
	Denied   bool
}

// Execute validates, authorizes and runs a tool call. Errors are reported to
// the model as error results rather than aborting the loop, so the agent can
// adapt; only context cancellation propagates.
func (r *Registry) Execute(ctx context.Context, call models.ToolCall) Outcome {
	out := Outcome{Result: models.ToolResult{CallID: call.ID}}
	t := r.tools[call.Name]
	if t == nil {
		out.Result.IsError = true
		out.Result.Content = fmt.Sprintf("unknown tool %q; available tools: %s", call.Name, strings.Join(r.Names(), ", "))
		return out
	}
	input := call.Input
	if len(strings.TrimSpace(string(input))) == 0 {
		input = json.RawMessage("{}")
	}
	if !json.Valid(input) {
		out.Result.IsError = true
		out.Result.Content = "invalid JSON arguments (if you were writing a large file, your output may have been cut off by the token limit: write it in smaller pieces)"
		return out
	}
	level, action := permissions.Safe, t.Name
	if t.Classify != nil {
		level, action = t.Classify(input)
	}
	out.Level, out.Action = level, action
	// The owner must see exactly what they approve: dangerous requests are
	// shown in full, and anything too large to review is refused.
	detail := truncate(string(input), 2000)
	if level == permissions.Dangerous {
		if len(input) > maxReviewable {
			out.Result.IsError = true
			out.Result.Content = fmt.Sprintf("this request is too large for my owner to review (%d bytes; limit %d): split it into smaller changes", len(input), maxReviewable)
			out.Denied = true
			return out
		}
		detail = string(input)
	}
	err := permissions.Check(ctx, r.Policy, r.Approver, permissions.Request{
		EvolutionID: r.EvolutionID, Action: action, Level: level, Detail: detail,
	})
	if err != nil {
		out.Denied = errors.Is(err, permissions.ErrDenied)
		out.Result.IsError = true
		out.Result.Content = err.Error()
		return out
	}
	text, err := t.Run(ctx, input)
	if err != nil {
		out.Result.IsError = true
		if text != "" {
			text += "\n"
		}
		out.Result.Content = truncate(text+"error: "+err.Error(), r.MaxOutput)
		return out
	}
	out.Result.Content = truncate(text, r.MaxOutput)
	out.Terminal = t.Terminal
	return out
}

// truncate keeps the head and tail of long text.
func truncate(s string, max int) string {
	if max <= 0 || len(s) <= max {
		return s
	}
	head := max * 2 / 5
	tail := max - head
	return s[:head] + fmt.Sprintf("\n\n…[%d characters omitted]…\n\n", len(s)-max) + s[len(s)-tail:]
}

// Truncate is exported for other packages that present tool output.
func Truncate(s string, max int) string { return truncate(s, max) }

// decode unmarshals input into v with a helpful error.
func decode(input json.RawMessage, v any) error {
	if err := json.Unmarshal(input, v); err != nil {
		return fmt.Errorf("invalid arguments: %w", err)
	}
	return nil
}

// Schema builds a JSON schema object from property definitions.
//
//	Schema(Props{"path": Str("file path")}, "path")
func Schema(props Props, required ...string) json.RawMessage {
	if required == nil {
		required = []string{}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
	return b
}

type Props map[string]any

func Str(desc string) map[string]any  { return map[string]any{"type": "string", "description": desc} }
func Int(desc string) map[string]any  { return map[string]any{"type": "integer", "description": desc} }
func Bool(desc string) map[string]any { return map[string]any{"type": "boolean", "description": desc} }
func StrList(desc string) map[string]any {
	return map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "description": desc}
}
func Enum(desc string, values ...string) map[string]any {
	return map[string]any{"type": "string", "enum": values, "description": desc}
}

// Fixed returns a classifier that always reports the same level.
func Fixed(level permissions.Level, action string) func(json.RawMessage) (permissions.Level, string) {
	return func(json.RawMessage) (permissions.Level, string) { return level, action }
}
