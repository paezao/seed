// Package agent is the Seed's own lightweight agent harness: a model in a
// loop with tools. There is no external agent framework; this file is the
// whole mechanism, kept small so the Seed can understand itself.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"seed/kernel/models"
	"seed/kernel/tools"
)

// Hooks observe the loop (for persistence and live progress).
type Hooks struct {
	OnText       func(text string)
	OnToolCall   func(call models.ToolCall)
	OnToolResult func(call models.ToolCall, out tools.Outcome)
	OnUsage      func(u models.Usage)
}

type Agent struct {
	// Purpose labels this agent's model calls (for keeping track of spending).
	Purpose   *models.Purpose
	Model     models.Model
	Tools     *tools.Registry
	System    string
	MaxTurns  int
	MaxTokens int
	// RequireTerminal makes the agent keep working until a terminal tool
	// succeeds (used for planning, mutation and reflection). Chat agents
	// leave it false and finish when the model stops calling tools.
	RequireTerminal bool
	// ContextBudget is the approximate size (in characters) above which old
	// tool output is elided from the conversation.
	ContextBudget int
	Hooks         Hooks
}

// Outcome is the result of a run.
type Outcome struct {
	Text     string
	Terminal *models.ToolCall // the successful terminal call, if any
	Messages []models.Message
	Turns    int
	Usage    models.Usage
}

var ErrMaxTurns = errors.New("agent reached its turn limit without finishing")

// Run continues the conversation in msgs until done.
func (a *Agent) Run(ctx context.Context, msgs []models.Message) (*Outcome, error) {
	if a.Purpose != nil {
		ctx = models.WithPurpose(ctx, *a.Purpose)
	}
	maxTurns := a.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 50
	}
	budget := a.ContextBudget
	if budget <= 0 {
		budget = 600_000
	}
	out := &Outcome{}
	nudges := 0
	for turn := 0; turn < maxTurns; turn++ {
		if err := ctx.Err(); err != nil {
			out.Messages = msgs
			return out, err
		}
		msgs = Compact(msgs, budget)
		resp, err := a.Model.Generate(ctx, models.Request{
			System: a.System, Messages: msgs, Tools: a.Tools.Specs(), MaxTokens: a.MaxTokens,
		})
		out.Turns = turn + 1
		if err != nil {
			out.Messages = msgs
			return out, fmt.Errorf("model: %w", err)
		}
		addUsage(&out.Usage, resp.Usage)
		if a.Hooks.OnUsage != nil {
			a.Hooks.OnUsage(resp.Usage)
		}
		msgs = append(msgs, models.Message{Role: models.Assistant, Content: resp.Text, ToolCalls: resp.ToolCalls})
		if strings.TrimSpace(resp.Text) != "" {
			out.Text = resp.Text
			if a.Hooks.OnText != nil {
				a.Hooks.OnText(resp.Text)
			}
		}

		if len(resp.ToolCalls) == 0 {
			if resp.StopReason == "max_tokens" {
				msgs = append(msgs, models.Message{Role: models.User, Content: "Your last response hit the output token limit and was cut off. Continue, in smaller steps (for example, write large files in several parts)."})
				continue
			}
			if !a.RequireTerminal {
				out.Messages = msgs
				return out, nil
			}
			nudges++
			if nudges > 3 {
				out.Messages = msgs
				return out, fmt.Errorf("agent stopped without calling %s", strings.Join(a.Tools.TerminalNames(), " or "))
			}
			msgs = append(msgs, models.Message{Role: models.User, Content: fmt.Sprintf(
				"Keep going: use your tools to make progress. When the work is complete and verified, call %s.",
				strings.Join(a.Tools.TerminalNames(), " or "))})
			continue
		}

		results := make([]models.ToolResult, 0, len(resp.ToolCalls))
		var terminal *models.ToolCall
		for i := range resp.ToolCalls {
			call := resp.ToolCalls[i]
			if call.ID == "" {
				call.ID = fmt.Sprintf("call_%d_%d", turn, i)
				resp.ToolCalls[i].ID = call.ID
			}
			if a.Hooks.OnToolCall != nil {
				a.Hooks.OnToolCall(call)
			}
			o := a.Tools.Execute(ctx, call)
			if a.Hooks.OnToolResult != nil {
				a.Hooks.OnToolResult(call, o)
			}
			results = append(results, o.Result)
			if o.Terminal {
				c := call
				terminal = &c
			}
			if err := ctx.Err(); err != nil {
				out.Messages = append(msgs, models.Message{Role: models.User, ToolResults: results})
				return out, err
			}
		}
		// The assistant message may have been rewritten with generated IDs.
		msgs[len(msgs)-1].ToolCalls = resp.ToolCalls
		msgs = append(msgs, models.Message{Role: models.User, ToolResults: results})
		if terminal != nil {
			out.Terminal = terminal
			out.Messages = msgs
			return out, nil
		}
	}
	out.Messages = msgs
	return out, ErrMaxTurns
}

func addUsage(total *models.Usage, u models.Usage) {
	total.InputTokens += u.InputTokens
	total.OutputTokens += u.OutputTokens
	total.CacheReadTokens += u.CacheReadTokens
	total.CacheWriteTokens += u.CacheWriteTokens
	total.CostUSD += u.CostUSD
	total.Priced = total.Priced || u.Priced
}

// Size estimates the size of a conversation in characters.
func Size(msgs []models.Message) int {
	n := 0
	for _, m := range msgs {
		n += len(m.Content)
		for _, c := range m.ToolCalls {
			n += len(c.Input) + len(c.Name)
		}
		for _, r := range m.ToolResults {
			n += len(r.Content)
		}
	}
	return n
}

// keepRecent messages are never compacted.
const keepRecent = 12

// Compact elides old tool outputs and large tool inputs (oldest first) until
// the conversation fits the budget. The first message (the task) and the most
// recent exchanges are kept intact. Tool call/result pairing is preserved.
func Compact(msgs []models.Message, budget int) []models.Message {
	if Size(msgs) <= budget || len(msgs) <= keepRecent+1 {
		return msgs
	}
	out := make([]models.Message, len(msgs))
	copy(out, msgs)
	size := Size(out)
	for i := 1; i < len(out)-keepRecent && size > budget; i++ {
		m := out[i]
		if len(m.ToolResults) > 0 {
			rs := make([]models.ToolResult, len(m.ToolResults))
			copy(rs, m.ToolResults)
			for j, r := range rs {
				if len(r.Content) > 400 {
					size -= len(r.Content)
					rs[j].Content = r.Content[:200] + fmt.Sprintf("\n…[older output elided to save context; %d chars]", len(r.Content))
					size += len(rs[j].Content)
				}
			}
			m.ToolResults = rs
		}
		if len(m.ToolCalls) > 0 {
			cs := make([]models.ToolCall, len(m.ToolCalls))
			copy(cs, m.ToolCalls)
			for j, c := range cs {
				if len(c.Input) > 1000 {
					size -= len(c.Input)
					cs[j].Input = elideInput(c.Input)
					size += len(cs[j].Input)
				}
			}
			m.ToolCalls = cs
		}
		out[i] = m
	}
	return out
}

// elideInput keeps short fields of a tool input and replaces long string
// values (e.g. file contents) with a placeholder, keeping valid JSON.
func elideInput(in json.RawMessage) json.RawMessage {
	var obj map[string]any
	if err := json.Unmarshal(in, &obj); err != nil {
		return json.RawMessage(`{}`)
	}
	for k, v := range obj {
		if s, ok := v.(string); ok && len(s) > 300 {
			obj[k] = fmt.Sprintf("[elided: %d chars]", len(s))
		}
	}
	b, _ := json.Marshal(obj)
	return b
}
