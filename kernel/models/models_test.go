package models

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

var sampleReq = Request{
	System: "You are a seed.",
	Tools:  []ToolSpec{{Name: "read_file", Description: "read", Schema: json.RawMessage(`{"type":"object"}`)}},
	Messages: []Message{
		{Role: User, Content: "hello"},
		{Role: Assistant, Content: "looking", ToolCalls: []ToolCall{{ID: "t1", Name: "read_file", Input: json.RawMessage(`{"path":"a"}`)}}},
		{Role: User, ToolResults: []ToolResult{{CallID: "t1", Content: "contents"}}},
	},
}

func TestAnthropicRoundTrip(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "k" || r.URL.Path != "/v1/messages" {
			t.Errorf("bad request: %s %v", r.URL.Path, r.Header)
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		io.WriteString(w, `{"content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"t2","name":"read_file","input":{"path":"b"}}],"stop_reason":"tool_use","usage":{"input_tokens":10,"output_tokens":5}}`)
	}))
	defer srv.Close()
	m := &Anthropic{APIKey: "k", Model: "m", BaseURL: srv.URL, MaxTokens: 100}
	resp, err := m.Generate(context.Background(), sampleReq)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Text != "ok" || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" || resp.StopReason != "tool_use" {
		t.Fatalf("unexpected response %+v", resp)
	}
	msgs := got["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	third := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)
	if third["type"] != "tool_result" || third["tool_use_id"] != "t1" {
		t.Fatalf("tool result not translated: %v", third)
	}
}

func TestOpenAIRoundTrip(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &got)
		io.WriteString(w, `{"choices":[{"message":{"content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"x\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":3,"completion_tokens":4}}`)
	}))
	defer srv.Close()
	m := &OpenAI{APIKey: "k", Model: "m", BaseURL: srv.URL, Provider: "openrouter", PromptCaching: true}
	resp, err := m.Generate(context.Background(), sampleReq)
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || string(resp.ToolCalls[0].Input) != `{"path":"x"}` || resp.StopReason != "tool_use" {
		t.Fatalf("unexpected response %+v", resp)
	}
	msgs := got["messages"].([]any)
	// system, user, assistant, tool
	if len(msgs) != 4 || msgs[3].(map[string]any)["role"] != "tool" {
		t.Fatalf("unexpected translated messages: %v", msgs)
	}
}

func TestAPIErrorAndMalformed(t *testing.T) {
	status := 500
	body := `oops`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	}))
	defer srv.Close()
	m := &OpenAI{Model: "m", BaseURL: srv.URL, Provider: "openai"}
	_, err := m.Generate(context.Background(), sampleReq)
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != 500 || !Retryable(err) {
		t.Fatalf("expected retryable APIError, got %v", err)
	}
	status, body = 400, `bad`
	_, err = m.Generate(context.Background(), sampleReq)
	if Retryable(err) {
		t.Fatalf("400 should not be retryable")
	}
	status, body = 200, `{not json`
	_, err = m.Generate(context.Background(), sampleReq)
	if err == nil || !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("expected malformed error, got %v", err)
	}
	body = `{"choices":[]}`
	if _, err = m.Generate(context.Background(), sampleReq); err == nil {
		t.Fatal("expected error for empty choices")
	}
}

func TestRetryRecovers(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) < 3 {
			w.WriteHeader(529)
			return
		}
		io.WriteString(w, `{"choices":[{"message":{"content":"done"},"finish_reason":"stop"}]}`)
	}))
	defer srv.Close()
	m := &retrying{inner: &OpenAI{Model: "m", BaseURL: srv.URL, Provider: "openai"}, attempts: 5, base: time.Millisecond}
	resp, err := m.Generate(context.Background(), sampleReq)
	if err != nil || resp.Text != "done" || n.Load() != 3 {
		t.Fatalf("retry failed: resp=%+v err=%v n=%d", resp, err, n.Load())
	}
}

func TestRetryGivesUpOnClientError(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(401)
	}))
	defer srv.Close()
	m := &retrying{inner: &OpenAI{Model: "m", BaseURL: srv.URL, Provider: "openai"}, attempts: 5, base: time.Millisecond}
	if _, err := m.Generate(context.Background(), sampleReq); err == nil || n.Load() != 1 {
		t.Fatalf("expected single failed attempt, n=%d err=%v", n.Load(), err)
	}
}

func TestResolveAndMissingKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("OPENROUTER_API_KEY", "x")
	t.Setenv("OPENAI_API_KEY", "")
	p, name, env := Resolve(Settings{Provider: "auto"})
	if p != "openrouter" || env != "OPENROUTER_API_KEY" || name == "" {
		t.Fatalf("resolve: %s %s %s", p, name, env)
	}
	t.Setenv("OPENROUTER_API_KEY", "")
	m, info := New(Settings{Provider: "openrouter"})
	if info.Configured {
		t.Fatal("should be unconfigured")
	}
	if _, err := m.Generate(context.Background(), sampleReq); err == nil || !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Fatalf("expected key hint, got %v", err)
	}
}
