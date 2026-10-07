package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Anthropic implements Model with the Anthropic Messages API.
type Anthropic struct {
	APIKey    string
	Model     string
	BaseURL   string
	MaxTokens int
	Client    *http.Client
}

func (a *Anthropic) Name() string { return "anthropic/" + a.Model }

type antBlock struct {
	Type         string          `json:"type"`
	Text         string          `json:"text,omitempty"`
	ID           string          `json:"id,omitempty"`
	Name         string          `json:"name,omitempty"`
	Input        json.RawMessage `json:"input,omitempty"`
	ToolUseID    string          `json:"tool_use_id,omitempty"`
	Content      string          `json:"content,omitempty"`
	IsError      bool            `json:"is_error,omitempty"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type cacheControl struct {
	Type string `json:"type"`
}

type antMessage struct {
	Role    string     `json:"role"`
	Content []antBlock `json:"content"`
}

type antTool struct {
	Name         string          `json:"name"`
	Description  string          `json:"description"`
	InputSchema  json.RawMessage `json:"input_schema"`
	CacheControl *cacheControl   `json:"cache_control,omitempty"`
}

type antRequest struct {
	Model     string       `json:"model"`
	MaxTokens int          `json:"max_tokens"`
	System    []antBlock   `json:"system,omitempty"`
	Messages  []antMessage `json:"messages"`
	Tools     []antTool    `json:"tools,omitempty"`
}

type antResponse struct {
	Content    []antBlock `json:"content"`
	StopReason string     `json:"stop_reason"`
	Usage      struct {
		InputTokens              int `json:"input_tokens"`
		OutputTokens             int `json:"output_tokens"`
		CacheReadInputTokens     int `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
	} `json:"usage"`
}

func (a *Anthropic) Generate(ctx context.Context, req Request) (*Response, error) {
	ephemeral := &cacheControl{Type: "ephemeral"}
	body := antRequest{Model: a.Model, MaxTokens: req.MaxTokens}
	if body.MaxTokens == 0 {
		body.MaxTokens = a.MaxTokens
	}
	if req.System != "" {
		body.System = []antBlock{{Type: "text", Text: req.System, CacheControl: ephemeral}}
	}
	for i, t := range req.Tools {
		at := antTool{Name: t.Name, Description: t.Description, InputSchema: t.Schema}
		if i == len(req.Tools)-1 {
			at.CacheControl = ephemeral
		}
		body.Tools = append(body.Tools, at)
	}
	for _, m := range req.Messages {
		var blocks []antBlock
		for _, r := range m.ToolResults {
			content := r.Content
			if content == "" {
				content = "(no output)"
			}
			blocks = append(blocks, antBlock{Type: "tool_result", ToolUseID: r.CallID, Content: content, IsError: r.IsError})
		}
		if strings.TrimSpace(m.Content) != "" {
			blocks = append(blocks, antBlock{Type: "text", Text: m.Content})
		}
		for _, c := range m.ToolCalls {
			input := c.Input
			if !json.Valid(input) || len(input) == 0 {
				input = json.RawMessage("{}")
			}
			blocks = append(blocks, antBlock{Type: "tool_use", ID: c.ID, Name: c.Name, Input: input})
		}
		if len(blocks) == 0 {
			blocks = []antBlock{{Type: "text", Text: "(empty)"}}
		}
		body.Messages = append(body.Messages, antMessage{Role: string(m.Role), Content: blocks})
	}
	// Cache the conversation prefix: mark the last block of the last message.
	if n := len(body.Messages); n > 0 {
		last := &body.Messages[n-1]
		last.Content[len(last.Content)-1].CacheControl = ephemeral
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	base := a.BaseURL
	if base == "" {
		base = "https://api.anthropic.com"
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(base, "/")+"/v1/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	httpReq.Header.Set("x-api-key", a.APIKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	client := a.Client
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Minute}
	}
	resp, err := client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, &APIError{Provider: "anthropic", Status: resp.StatusCode, Body: string(raw)}
	}
	var ar antResponse
	if err := json.Unmarshal(raw, &ar); err != nil {
		return nil, fmt.Errorf("anthropic: malformed response: %w", err)
	}
	out := &Response{StopReason: ar.StopReason, Usage: Usage{
		InputTokens: ar.Usage.InputTokens, OutputTokens: ar.Usage.OutputTokens,
		CacheReadTokens: ar.Usage.CacheReadInputTokens, CacheWriteTokens: ar.Usage.CacheCreationInputTokens,
	}}
	var texts []string
	for _, b := range ar.Content {
		switch b.Type {
		case "text":
			texts = append(texts, b.Text)
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, ToolCall{ID: b.ID, Name: b.Name, Input: b.Input})
		}
	}
	out.Text = strings.Join(texts, "\n")
	return out, nil
}
