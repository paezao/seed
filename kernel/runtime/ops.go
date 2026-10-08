package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"seed/kernel/permissions"
	"seed/kernel/tools"
)

// Operating myself: the chat is not only for building. These tools let me
// answer questions about my live data and act on it. Reading is free;
// changing real data needs my owner's approval for each change.

func (c *Chat) opsTools() []*tools.Tool {
	if c.Live == nil {
		return nil
	}
	live := c.Live
	return []*tools.Tool{
		{
			Name: "query_data",
			Description: "Run a read-only SQL query against my LIVE database (my real data) to answer my owner's questions: counts, lists, lookups. " +
				"It runs in a READ ONLY transaction (writes are refused by PostgreSQL). Returns up to 100 rows.",
			Schema:   tools.Schema(tools.Props{"sql": tools.Str("a SELECT (or WITH … SELECT) query")}, "sql"),
			Classify: tools.Fixed(permissions.Safe, "read my live data"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ SQL string }
				if err := json.Unmarshal(in, &a); err != nil {
					return "", err
				}
				return tools.QueryTx(ctx, live.ReaderURL(), a.SQL, 100, true)
			},
		},
		{
			Name:        "describe_data",
			Description: "Describe the tables and columns of my live database.",
			Schema:      tools.Schema(tools.Props{}),
			Classify:    tools.Fixed(permissions.Safe, "describe my live data"),
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				return tools.DescribeSchema(ctx, live.DatabaseURL())
			},
		},
		{
			Name: "change_data",
			Description: "Change my LIVE data (INSERT/UPDATE/DELETE) when my owner asks for it. My owner must approve each change in the control plane " +
				"before it runs; give the exact SQL and a one-line reason. Never change data on your own initiative or to test something.",
			Schema: tools.Schema(tools.Props{
				"sql":    tools.Str("the statement(s) to run, in one transaction"),
				"reason": tools.Str("why, in one line, for the approval"),
			}, "sql", "reason"),
			Classify: func(in json.RawMessage) (permissions.Level, string) {
				var a struct{ SQL, Reason string }
				_ = json.Unmarshal(in, &a)
				return permissions.Dangerous, "change live data: " + tools.Truncate(a.Reason, 120)
			},
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ SQL, Reason string }
				if err := json.Unmarshal(in, &a); err != nil {
					return "", err
				}
				return tools.QueryTx(ctx, live.DatabaseURL(), a.SQL, 50, false)
			},
		},
		{
			Name:        "call_api",
			Description: "Call my own live API (e.g. GET /api/recipes) and return the response. GET is free; other methods change real data and need my owner's approval.",
			Schema: tools.Schema(tools.Props{
				"method": tools.Enum("HTTP method", "GET", "POST", "PUT", "PATCH", "DELETE"),
				"path":   tools.Str("path, e.g. /api/recipes?q=soup"),
				"body":   tools.Str("JSON body for writes"),
			}, "method", "path"),
			Classify: func(in json.RawMessage) (permissions.Level, string) {
				var a struct{ Method, Path string }
				_ = json.Unmarshal(in, &a)
				if strings.EqualFold(a.Method, "GET") {
					return permissions.Safe, "read my live API"
				}
				return permissions.Dangerous, "change live data: " + strings.ToUpper(a.Method) + " " + a.Path
			},
			Run: func(ctx context.Context, in json.RawMessage) (string, error) {
				var a struct{ Method, Path, Body string }
				if err := json.Unmarshal(in, &a); err != nil {
					return "", err
				}
				return live.Call(ctx, a.Method, a.Path, a.Body)
			},
		},
	}
}

// Call makes a request to the live organism (through its private bridge).
func (o *Organism) Call(ctx context.Context, method, path, body string) (string, error) {
	o.mu.Lock()
	sb := o.sb
	o.mu.Unlock()
	if sb == nil {
		return "", errors.New("my body is not running")
	}
	base, err := sb.URL(ctx)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), base+path, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	if body != "" {
		req.Header.Set("content-type", "application/json")
	}
	resp, err := (&http.Client{Transport: sb.Transport(), Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return fmt.Sprintf("HTTP %d\n%s", resp.StatusCode, tools.Truncate(string(b), 8000)), nil
}
