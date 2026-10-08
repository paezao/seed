package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"seed/kernel/agent"
	"seed/kernel/events"
	"seed/kernel/evolution"
	"seed/kernel/fsx"
	"seed/kernel/git"
	"seed/kernel/knowledge"
	"seed/kernel/memory"
	"seed/kernel/models"
	"seed/kernel/permissions"
	"seed/kernel/skills"
	"seed/kernel/tools"
)

// Chat is the Seed's conversational interface with its owner.
type Chat struct {
	Root   string
	Store  *memory.Store
	Bus    *events.Bus
	Model  models.Model
	Orch   *evolution.Orchestrator
	Repo   *git.Repo
	Policy *permissions.Policy
	// Live is my running organism and its data (for operating myself).
	Live *Organism
	// Approvals asks my owner before I change live data.
	Approvals permissions.Approver

	mu sync.Mutex
}

// Post records an owner message and responds asynchronously.
func (c *Chat) Post(ctx context.Context, content string) (*memory.Message, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty message")
	}
	// An evolution waiting on questions takes the owner's next message as
	// its answer, deterministically: no model decides where it goes.
	if waiting := c.Orch.WaitingForAnswer(); waiting != "" {
		m, err := c.Store.AddMessage(ctx, memory.DefaultConversation, "user", content, waiting)
		if err != nil {
			return nil, err
		}
		c.Bus.Publish("message", m)
		if err := c.Orch.Answer(ctx, waiting, content); err != nil {
			return nil, err
		}
		return m, nil
	}
	m, err := c.Store.AddMessage(ctx, memory.DefaultConversation, "user", content, "")
	if err != nil {
		return nil, err
	}
	c.Bus.Publish("message", m)
	go c.respond(context.WithoutCancel(ctx))
	return m, nil
}

func (c *Chat) respond(ctx context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.Bus.Publish("chat", map[string]bool{"thinking": true})
	defer c.Bus.Publish("chat", map[string]bool{"thinking": false})

	history, err := c.Store.Messages(ctx, memory.DefaultConversation, 40)
	if err != nil {
		c.reply(ctx, "I couldn't read our conversation: "+err.Error(), "")
		return
	}
	var started string
	reg := tools.NewRegistry(c.Policy, c.Approvals)
	ws := &tools.Workspace{Root: c.Root, Policy: c.Policy}
	reg.Add(tools.ReadOnlyFileTools(ws)...).Add(tools.SkillTools(skills.Library{Root: c.Root})...).Add(tools.GitTools(c.Repo, "HEAD")[2])
	reg.Add(c.opsTools()...)
	reg.Add(&tools.Tool{
		Name:        "start_evolution",
		Description: "Start evolving myself according to the owner's intent. The intent must be self-contained: include every relevant detail from the conversation.",
		Schema:      tools.Schema(tools.Props{"intent": tools.Str("what I should become or change, in full")}, "intent"),
		Classify:    tools.Fixed(permissions.Safe, "start evolution"),
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var a struct{ Intent string }
			if err := json.Unmarshal(in, &a); err != nil {
				return "", err
			}
			e, err := c.Orch.Request(ctx, memory.DefaultConversation, withOwnerWords(a.Intent, lastOwnerMessage(history)))
			if err != nil {
				return "", err
			}
			started = e.ID
			return "evolution " + e.ID + " started; the owner sees its plan and progress live", nil
		},
	}, &tools.Tool{
		Name:        "list_evolutions",
		Description: "List my recent evolutions with their status.",
		Schema:      tools.Schema(tools.Props{}),
		Classify:    tools.Fixed(permissions.Safe, "list evolutions"),
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			evs, err := c.Store.Evolutions(ctx, 15)
			if err != nil {
				return "", err
			}
			var sb strings.Builder
			for _, e := range evs {
				fmt.Fprintf(&sb, "- %s [%s] %s", e.ID, e.Status, firstNonEmpty(e.Title, e.Intent))
				if e.Error != "" {
					fmt.Fprintf(&sb, " — error: %s", tools.Truncate(e.Error, 300))
				}
				sb.WriteString("\n")
			}
			if sb.Len() == 0 {
				return "no evolutions yet", nil
			}
			return sb.String(), nil
		},
	})

	// Continuing a roadmap is only possible when one exists: the tool appears
	// only then, and the kernel writes the intent from the roadmap itself.
	if next := nextRoadmapStage(c.Root); next != "" {
		reg.Add(&tools.Tool{
			Name:        "continue_roadmap",
			Description: "Start the next stage of my roadmap (knowledge/roadmap.md): " + next + ". Use it when the owner asks me to continue.",
			Schema:      tools.Schema(tools.Props{"note": tools.Str("optional extra wishes from the owner for this stage")}),
			Classify:    tools.Fixed(permissions.Safe, "continue roadmap"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ Note string }
				_ = json.Unmarshal(in, &a)
				intent := "Continue my roadmap (knowledge/roadmap.md) with the next stage: " + next + "."
				if strings.TrimSpace(a.Note) != "" {
					intent += "\n\nOwner's wishes for this stage: " + a.Note
				}
				e, err := c.Orch.Request(ctx, memory.DefaultConversation, withOwnerWords(intent, lastOwnerMessage(history)))
				if err != nil {
					return "", err
				}
				started = e.ID
				return "evolution " + e.ID + " started for " + next, nil
			},
		})
	}

	a := &agent.Agent{
		Model: c.Model, Tools: reg, MaxTurns: 15, MaxTokens: 4000,
		System: agent.PromptFor("chat", knowledge.Name(c.Root)) + "\n\n" + c.Orch.SelfContext(ctx, c.Root),
	}
	out, err := a.Run(ctx, toModelMessages(history))
	if err != nil {
		slog.Warn("chat", "err", err)
		c.reply(ctx, "I couldn't think just now: "+err.Error(), started)
		return
	}
	text := strings.TrimSpace(out.Text)
	if text == "" {
		text = "On it."
	}
	c.reply(ctx, text, started)
}

func (c *Chat) reply(ctx context.Context, text, evolutionID string) {
	m, err := c.Store.AddMessage(ctx, memory.DefaultConversation, "seed", text, evolutionID)
	if err != nil {
		slog.Error("store reply", "err", err)
		return
	}
	c.Bus.Publish("message", m)
}

// toModelMessages converts stored chat to alternating model turns.
func toModelMessages(history []memory.Message) []models.Message {
	var out []models.Message
	for _, m := range history {
		role := models.User
		content := m.Content
		switch {
		case m.Kind == "report":
			// Written by the kernel, not by the chat agent: present it as a
			// record so the agent never imitates it (and never claims an
			// evolution happened without starting one).
			content = "[kernel record] " + content
		case m.Role == "seed":
			role = models.Assistant
		case m.Role == "system":
			content = "[system] " + content
		}
		if n := len(out); n > 0 && out[n-1].Role == role {
			out[n-1].Content += "\n\n" + content
			continue
		}
		out = append(out, models.Message{Role: role, Content: content})
	}
	for len(out) > 0 && out[0].Role != models.User {
		out = out[1:]
	}
	return out
}

func firstNonEmpty(s ...string) string {
	for _, v := range s {
		if v != "" {
			return v
		}
	}
	return ""
}

func lastOwnerMessage(history []memory.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == "user" {
			return strings.TrimSpace(history[i].Content)
		}
	}
	return ""
}

// withOwnerWords makes sure the evolution sees the owner's exact request,
// not only the chat model's paraphrase (which can drop details).
func withOwnerWords(intent, owner string) string {
	intent = strings.TrimSpace(intent)
	if owner == "" || strings.Contains(intent, owner) {
		return intent
	}
	return intent + "\n\nThe owner's exact words: \"" + owner + "\""
}

// nextRoadmapStage returns the roadmap's next unfinished stage ("Stage 2:
// Cart"), or "" when there is no roadmap or it is done.
func nextRoadmapStage(root string) string {
	b, err := fsx.ReadFile(root, evolution.RoadmapPath)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "- [ ] ") {
			stage := strings.TrimPrefix(line, "- [ ] ")
			stage = strings.ReplaceAll(stage, "**", "")
			stage = strings.TrimSuffix(strings.SplitN(stage, " (next)", 2)[0], ".")
			if i := strings.Index(stage, ": "); i >= 0 {
				if j := strings.Index(stage[i+2:], ": "); j >= 0 {
					stage = stage[:i+2+j] // drop the summary
				}
			}
			return stage
		}
	}
	return ""
}
