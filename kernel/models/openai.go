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

// OpenAI implements Model with the OpenAI-compatible Chat Completions API
// (OpenAI, OpenRouter, vLLM, Ollama, ...).
type OpenAI struct {
	APIKey    string
	Model     string
	BaseURL   string
	MaxTokens int
	Provider  string
	// PromptCaching adds Anthropic-style cache_control breakpoints, which
	// OpenRouter forwards to Anthropic models.
	PromptCaching bool
	Client        *http.Client
}

func (o *OpenAI) Name() string { return o.Provider + "/" + o.Model }

type oaPart struct {
	Type         string        `json:"type"`
	Text         string        `json:"text"`
	CacheControl *cacheControl `json:"cache_control,omitempty"`
}

type oaMessage struct {
	Role       string       `json:"role"`
	Content    any          `json:"content"` // string or []oaPart
	ToolCalls  []oaToolCall `json:"tool_calls,omitempty"`
	ToolCallID string       `json:"tool_call_id,omitempty"`
}

type oaToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type oaTool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

type oaRequest struct {
	Model     string      `json:"model"`
	Messages  []oaMessage `json:"messages"`
	Tools     []oaTool    `json:"tools,omitempty"`
	MaxTokens int         `json:"max_tokens,omitempty"`
}

type oaResponse struct {
	Choices []struct {
		Message struct {
			Content   *string      `json:"content"`
			ToolCalls []oaToolCall `json:"tool_calls"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens        int `json:"prompt_tokens"`
		CompletionTokens    int `json:"completion_tokens"`
		PromptTokensDetails struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	Error *struct {
		Message string `json:"message"`
		Code    any    `json:"code"`
	} `json:"error"`
}

func (o *OpenAI) text(s string, cache bool) any {
	if !o.PromptCaching {
		return s
	}
	p := oaPart{Type: "text", Text: s}
	if cache {
		p.CacheControl = &cacheControl{Type: "ephemeral"}
	}
	return []oaPart{p}
}

func (o *OpenAI) Generate(ctx context.Context, req Request) (*Response, error) {
	body := oaRequest{Model: o.Model, MaxTokens: req.MaxTokens}
	if body.MaxTokens == 0 {
		body.MaxTokens = o.MaxTokens
	}
	if req.System != "" {
		body.Messages = append(body.Messages, oaMessage{Role: "system", Content: o.text(req.System, true)})
	}
	for _, t := range req.Tools {
		var ot oaTool
		ot.Type = "function"
		ot.Function.Name = t.Name
		ot.Function.Description = t.Description
		ot.Function.Parameters = t.Schema
		body.Tools = append(body.Tools, ot)
	}
	lastUser := -1
	for _, m := range req.Messages {
		switch m.Role {
		case Assistant:
			om := oaMessage{Role: "assistant", Content: m.Content}
			for _, c := range m.ToolCalls {
				var tc oaToolCall
				tc.ID, tc.Type = c.ID, "function"
				tc.Function.Name = c.Name
				tc.Function.Arguments = string(c.Input)
				if tc.Function.Arguments == "" {
					tc.Function.Arguments = "{}"
				}
				om.ToolCalls = append(om.ToolCalls, tc)
			}
			body.Messages = append(body.Messages, om)
		default:
			for _, r := range m.ToolResults {
				content := r.Content
				if r.IsError {
					content = "ERROR: " + content
				}
				if content == "" {
					content = "(no output)"
				}
				body.Messages = append(body.Messages, oaMessage{Role: "tool", ToolCallID: r.CallID, Content: content})
				lastUser = len(body.Messages) - 1
			}
			if strings.TrimSpace(m.Content) != "" {
				body.Messages = append(body.Messages, oaMessage{Role: "user", Content: m.Content})
				lastUser = len(body.Messages) - 1
			}
		}
	}
	// Cache breakpoint on the latest user/tool turn so each step reuses the prefix.
	if o.PromptCaching && lastUser >= 0 {
		if s, ok := body.Messages[lastUser].Content.(string); ok {
			body.Messages[lastUser].Content = o.text(s, true)
		}
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.BaseURL, "/")+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("content-type", "application/json")
	if o.APIKey != "" {
		httpReq.Header.Set("authorization", "Bearer "+o.APIKey)
	}
	if o.Provider == "openrouter" {
		httpReq.Header.Set("X-Title", "Seed")
	}
	client := o.Client
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
		return nil, &APIError{Provider: o.Provider, Status: resp.StatusCode, Body: string(raw)}
	}
	var or oaResponse
	if err := json.Unmarshal(raw, &or); err != nil {
		return nil, fmt.Errorf("%s: malformed response: %w", o.Provider, err)
	}
	if or.Error != nil {
		// Some gateways report upstream failures inside a 200 response.
		return nil, &APIError{Provider: o.Provider, Status: 502, Body: or.Error.Message}
	}
	if len(or.Choices) == 0 {
		return nil, &APIError{Provider: o.Provider, Status: 502, Body: "response had no choices"}
	}
	ch := or.Choices[0]
	out := &Response{Usage: Usage{
		InputTokens: or.Usage.PromptTokens, OutputTokens: or.Usage.CompletionTokens,
		CacheReadTokens: or.Usage.PromptTokensDetails.CachedTokens,
	}}
	if ch.Message.Content != nil {
		out.Text = *ch.Message.Content
	}
	for _, tc := range ch.Message.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCall{ID: tc.ID, Name: tc.Function.Name, Input: json.RawMessage(tc.Function.Arguments)})
	}
	switch ch.FinishReason {
	case "tool_calls":
		out.StopReason = "tool_use"
	case "length":
		out.StopReason = "max_tokens"
	case "stop":
		out.StopReason = "end_turn"
	default:
		out.StopReason = ch.FinishReason
	}
	if len(out.ToolCalls) > 0 && out.StopReason != "max_tokens" {
		out.StopReason = "tool_use"
	}
	return out, nil
}
