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

	mu sync.Mutex
}

// Post records an owner message and responds asynchronously.
func (c *Chat) Post(ctx context.Context, content string) (*memory.Message, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, fmt.Errorf("empty message")
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
	reg := tools.NewRegistry(c.Policy, nil)
	ws := &tools.Workspace{Root: c.Root, Policy: c.Policy}
	reg.Add(tools.ReadOnlyFileTools(ws)...).Add(tools.SkillTools(skills.Library{Root: c.Root})...).Add(tools.GitTools(c.Repo, "HEAD")[2])
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
			e, err := c.Orch.Request(ctx, memory.DefaultConversation, a.Intent)
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
		if m.Role == "seed" {
			role = models.Assistant
		} else if m.Role == "system" {
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
