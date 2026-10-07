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

// Info describes the selected model for display.
type Info struct {
	Provider   string `json:"provider"`
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
}

type unavailable struct{ reason string }

func (u unavailable) Generate(context.Context, Request) (*Response, error) {
	return nil, errors.New(u.reason)
}
func (u unavailable) Name() string { return "unavailable" }
