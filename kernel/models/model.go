// Package models is the provider-neutral interface to language models.
//
// The agent speaks only in these types. Each provider translates them to its
// own wire format, so the rest of the kernel never depends on a vendor.
package models

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
)

type Role string

const (
	User      Role = "user"
	Assistant Role = "assistant"
)

// Message is one conversational turn. An assistant message may carry tool
// calls; the following user message carries their results.
type Message struct {
	Role        Role         `json:"role"`
	Content     string       `json:"content,omitempty"`
	ToolCalls   []ToolCall   `json:"tool_calls,omitempty"`
	ToolResults []ToolResult `json:"tool_results,omitempty"`
}

type ToolCall struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

type ToolResult struct {
	CallID  string `json:"call_id"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

// ToolSpec describes a tool to the model. Schema is a JSON Schema object.
type ToolSpec struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"input_schema"`
}

type Request struct {
	System    string
	Messages  []Message
	Tools     []ToolSpec
	MaxTokens int
}

type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

type Response struct {
	Text       string
	ToolCalls  []ToolCall
	StopReason string // end_turn | tool_use | max_tokens | other
	Usage      Usage
}

// Model generates the next assistant turn.
type Model interface {
	Generate(ctx context.Context, req Request) (*Response, error)
	Name() string
}

// APIError is a non-2xx response from a provider.
type APIError struct {
	Provider string
	Status   int
	Body     string
}

func (e *APIError) Error() string {
	body := e.Body
	if len(body) > 500 {
		body = body[:500] + "…"
	}
	return fmt.Sprintf("%s: HTTP %d: %s", e.Provider, e.Status, body)
}

// Retryable reports whether the error is worth retrying.
func Retryable(err error) bool {
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.Status == 408 || ae.Status == 409 || ae.Status == 429 || ae.Status >= 500
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	return true // network errors, truncated bodies
}

// Settings selects and configures a provider.
type Settings struct {
	Provider  string
	Name      string
	BaseURL   string
	APIKeyEnv string
	MaxTokens int
}

// Info describes the resolved provider for display.
type Info struct {
	Provider   string `json:"provider"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
}

var providerKeys = []struct{ provider, env string }{
	{"anthropic", "ANTHROPIC_API_KEY"},
	{"openrouter", "OPENROUTER_API_KEY"},
	{"openai", "OPENAI_API_KEY"},
}

// Resolve returns the provider, model name and key env var that Settings selects.
func Resolve(s Settings) (provider, name, keyEnv string) {
	provider = s.Provider
	if provider == "" || provider == "auto" {
		provider = "anthropic"
		for _, pk := range providerKeys {
			if os.Getenv(pk.env) != "" {
				provider = pk.provider
				break
			}
		}
	}
	keyEnv = s.APIKeyEnv
	if keyEnv == "" {
		for _, pk := range providerKeys {
			if pk.provider == provider {
				keyEnv = pk.env
			}
		}
	}
	name = s.Name
	if name == "" {
		switch provider {
		case "anthropic":
			name = "claude-sonnet-5-5"
		case "openrouter":
			name = "anthropic/claude-sonnet-5.5"
		case "openai":
			name = "gpt-5.5"
		}
	}
	return provider, name, keyEnv
}

// New builds a Model from settings. Missing keys produce a model that fails
// with a clear message on use, so the Seed can still boot and explain itself.
func New(s Settings) (Model, Info) {
	provider, name, keyEnv := Resolve(s)
	key := ""
	if keyEnv != "" {
		key = os.Getenv(keyEnv)
	}
	info := Info{Provider: provider, Name: name, Configured: key != "" || provider == "openai-compatible"}
	maxTokens := s.MaxTokens
	if maxTokens == 0 {
		maxTokens = 16000
	}
	var m Model
	switch provider {
	case "anthropic":
		m = &Anthropic{APIKey: key, Model: name, BaseURL: s.BaseURL, MaxTokens: maxTokens}
	case "openrouter":
		base := s.BaseURL
		if base == "" {
			base = "https://openrouter.ai/api/v1"
		}
		m = &OpenAI{APIKey: key, Model: name, BaseURL: base, MaxTokens: maxTokens, Provider: "openrouter",
			PromptCaching: strings.HasPrefix(name, "anthropic/")}
	case "openai", "openai-compatible":
		base := s.BaseURL
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		m = &OpenAI{APIKey: key, Model: name, BaseURL: base, MaxTokens: maxTokens, Provider: provider}
	default:
		return unavailable{fmt.Sprintf("unknown model provider %q", provider)}, info
	}
	if !info.Configured {
		return unavailable{fmt.Sprintf("no API key: set %s (provider %s) to give the Seed a mind", keyEnv, provider)}, info
	}
	return WithRetry(m, 5), info
}

type unavailable struct{ reason string }

func (u unavailable) Generate(context.Context, Request) (*Response, error) {
	return nil, errors.New(u.reason)
}
func (u unavailable) Name() string { return "unavailable" }
