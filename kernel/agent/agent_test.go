package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"seed/kernel/models"
	"seed/kernel/permissions"
	"seed/kernel/tools"
)

func registry(policy *permissions.Policy) (*tools.Registry, *[]string) {
	var ran []string
	r := tools.NewRegistry(policy, nil)
	r.Add(&tools.Tool{
		Name: "echo", Schema: tools.Schema(tools.Props{"text": tools.Str("")}, "text"),
		Classify: tools.Fixed(permissions.Safe, "echo"),
		Run: func(ctx context.Context, in json.RawMessage) (string, error) {
			var a struct{ Text string }
			if err := json.Unmarshal(in, &a); err != nil {
				return "", err
			}
			ran = append(ran, a.Text)
			return "echo: " + a.Text, nil
		},
	}, &tools.Tool{
		Name: "danger", Schema: tools.Schema(tools.Props{}),
		Classify: tools.Fixed(permissions.Dangerous, "danger"),
		Run: func(context.Context, json.RawMessage) (string, error) {
			ran = append(ran, "danger")
			return "boom", nil
		},
	}, &tools.Tool{
		Name: "finish", Schema: tools.Schema(tools.Props{"summary": tools.Str("")}, "summary"), Terminal: true,
		Classify: tools.Fixed(permissions.Safe, "finish"),
		Run:      func(context.Context, json.RawMessage) (string, error) { return "done", nil },
	})
	return r, &ran
}

var allowAll = permissions.NewPolicy("allow", "allow", "deny", nil)

func TestRunUntilTerminal(t *testing.T) {
	reg, ran := registry(allowAll)
	m := &models.Scripted{Steps: []func(models.Request) (*models.Response, error){
		models.Call("echo", map[string]string{"text": "a"}),
		models.Say("thinking out loud"), // no tool call -> nudged
		models.Call("finish", map[string]string{"summary": "ok"}),
	}}
	a := &Agent{Model: m, Tools: reg, RequireTerminal: true}
	out, err := a.Run(context.Background(), []models.Message{{Role: models.User, Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Terminal == nil || out.Terminal.Name != "finish" || len(*ran) != 1 {
		t.Fatalf("unexpected outcome %+v ran=%v", out, *ran)
	}
	// The nudge must be visible to the model.
	last := m.Requests[2].Messages
	if !strings.Contains(last[len(last)-1].Content, "finish") {
		t.Fatalf("expected nudge mentioning finish, got %q", last[len(last)-1].Content)
	}
}

func TestMalformedAndUnknownToolCalls(t *testing.T) {
	reg, _ := registry(allowAll)
	m := &models.Scripted{Steps: []func(models.Request) (*models.Response, error){
		models.Calls(models.ToolCall{ID: "1", Name: "echo", Input: json.RawMessage(`{"text": "unterminated`)}),
		models.Calls(models.ToolCall{ID: "2", Name: "nope", Input: json.RawMessage(`{}`)}),
		models.Calls(models.ToolCall{ID: "3", Name: "echo", Input: json.RawMessage(`{"text": 5}`)}),
		models.Say("done"),
	}}
	a := &Agent{Model: m, Tools: reg}
	out, err := a.Run(context.Background(), []models.Message{{Role: models.User, Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if out.Text != "done" {
		t.Fatalf("expected chat to finish with text, got %q", out.Text)
	}
	results := []string{}
	for _, msg := range out.Messages {
		for _, r := range msg.ToolResults {
			if !r.IsError {
				t.Fatalf("expected error result, got %+v", r)
			}
			results = append(results, r.Content)
		}
	}
	if len(results) != 3 || !strings.Contains(results[0], "invalid JSON") || !strings.Contains(results[1], "unknown tool") || !strings.Contains(results[2], "cannot unmarshal") {
		t.Fatalf("unexpected results: %v", results)
	}
}

func TestPermissionDeniedIsReported(t *testing.T) {
	reg, ran := registry(allowAll)
	m := &models.Scripted{Steps: []func(models.Request) (*models.Response, error){
		models.Call("danger", map[string]any{}),
		models.Say("ok, I won't"),
	}}
	a := &Agent{Model: m, Tools: reg}
	out, err := a.Run(context.Background(), []models.Message{{Role: models.User, Content: "go"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(*ran) != 0 {
		t.Fatal("dangerous tool must not run when denied")
	}
	r := out.Messages[2].ToolResults[0]
	if !r.IsError || !strings.Contains(r.Content, "permission denied") {
		t.Fatalf("expected permission denied result, got %+v", r)
	}
}

func TestProviderFailurePropagates(t *testing.T) {
	reg, _ := registry(allowAll)
	m := &models.Scripted{Steps: []func(models.Request) (*models.Response, error){
		models.Fail(errors.New("provider down")),
	}}
	a := &Agent{Model: m, Tools: reg}
	if _, err := a.Run(context.Background(), []models.Message{{Role: models.User, Content: "go"}}); err == nil || !strings.Contains(err.Error(), "provider down") {
		t.Fatalf("expected provider error, got %v", err)
	}
}

func TestMaxTurnsAndStopWithoutFinish(t *testing.T) {
	reg, _ := registry(allowAll)
	steps := []func(models.Request) (*models.Response, error){}
	for i := 0; i < 5; i++ {
		steps = append(steps, models.Call("echo", map[string]string{"text": "x"}))
	}
	a := &Agent{Model: &models.Scripted{Steps: steps}, Tools: reg, MaxTurns: 3, RequireTerminal: true}
	if _, err := a.Run(context.Background(), []models.Message{{Role: models.User, Content: "go"}}); !errors.Is(err, ErrMaxTurns) {
		t.Fatalf("expected ErrMaxTurns, got %v", err)
	}
	quiet := []func(models.Request) (*models.Response, error){models.Say("a"), models.Say("b"), models.Say("c"), models.Say("d")}
	a = &Agent{Model: &models.Scripted{Steps: quiet}, Tools: reg, RequireTerminal: true}
	if _, err := a.Run(context.Background(), []models.Message{{Role: models.User, Content: "go"}}); err == nil || !strings.Contains(err.Error(), "without calling finish") {
		t.Fatalf("expected stop-without-finish error, got %v", err)
	}
}

func TestCompactPreservesStructure(t *testing.T) {
	big := strings.Repeat("x", 5000)
	msgs := []models.Message{{Role: models.User, Content: "task"}}
	for i := 0; i < 30; i++ {
		msgs = append(msgs,
			models.Message{Role: models.Assistant, ToolCalls: []models.ToolCall{{ID: "c", Name: "write_file", Input: json.RawMessage(`{"path":"a","content":"` + big + `"}`)}}},
			models.Message{Role: models.User, ToolResults: []models.ToolResult{{CallID: "c", Content: big}}},
		)
	}
	before := Size(msgs)
	out := Compact(msgs, 100_000)
	if Size(out) >= before || Size(out) > 100_000 {
		t.Fatalf("compaction ineffective: %d -> %d", before, Size(out))
	}
	if len(out) != len(msgs) || out[0].Content != "task" {
		t.Fatal("compaction must keep message structure and the task")
	}
	if !json.Valid(out[1].ToolCalls[0].Input) {
		t.Fatal("elided input must remain valid JSON")
	}
	if out[len(out)-1].ToolResults[0].Content != big {
		t.Fatal("recent messages must be intact")
	}
	if Size(msgs) != before {
		t.Fatal("compaction must not mutate its input")
	}
}
