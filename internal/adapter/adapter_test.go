package adapter

import (
	"context"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/foreman/foreman/internal/eventbus"
)

func newTestAdapter() *OpenCodeAdapter {
	bus := eventbus.NewMemoryBus()
	return NewOpenCodeAdapter("opencode", "/tmp", bus, 30*time.Second, 90*time.Second)
}

func TestMeta(t *testing.T) {
	a := newTestAdapter()
	meta := a.Meta()
	if meta.Name != "opencode" {
		t.Errorf("Name = %q, want %q", meta.Name, "opencode")
	}
	if meta.Version == "" {
		t.Error("expected non-empty Version")
	}
}

func TestBuildConfig(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	args, err := a.BuildConfig(ctx, BuildConfig{
		TaskDescription: "do something",
		SessionID:       "sess-1",
	})
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if len(args) < 5 {
		t.Fatalf("expected at least 5 args, got %d: %v", len(args), args)
	}

	// Verify the task prompt is in the args
	foundTask := false
	for _, arg := range args {
		if arg == "do something" {
			foundTask = true
			break
		}
	}
	if !foundTask {
		t.Errorf("task prompt not found in args: %v", args)
	}
}

func TestParseEvent_StepStart(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	line := []byte(`{"type":"step_start","sessionID":"abc","part":{"id":"step-1","metadata":{"isLatest":true}}}`)
	evt, err := a.ParseEvent(ctx, line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	oe, ok := evt.(*OpenCodeEvent)
	if !ok {
		t.Fatalf("expected *OpenCodeEvent, got %T", evt)
	}
	if oe.Type != "step_start" {
		t.Errorf("Type = %q, want %q", oe.Type, "step_start")
	}
	if oe.SessionID != "abc" {
		t.Errorf("SessionID = %q, want %q", oe.SessionID, "abc")
	}
}

func TestParseEvent_Text(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	line := []byte(`{"type":"text","sessionID":"abc","part":{"text":"Hello world"}}`)
	evt, err := a.ParseEvent(ctx, line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	oe, ok := evt.(*OpenCodeEvent)
	if !ok {
		t.Fatalf("expected *OpenCodeEvent, got %T", evt)
	}
	if oe.Type != "text" {
		t.Errorf("Type = %q, want %q", oe.Type, "text")
	}
	if oe.Part == nil || oe.Part.Text != "Hello world" {
		t.Errorf("Part.Text = %q, want %q", oe.Part.Text, "Hello world")
	}
}

func TestParseEvent_ToolUse(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	line := []byte(`{"type":"tool_use","sessionID":"abc","part":{"tool":"read","state":{"output":"file content"}}}`)
	evt, err := a.ParseEvent(ctx, line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	oe, ok := evt.(*OpenCodeEvent)
	if !ok {
		t.Fatalf("expected *OpenCodeEvent, got %T", evt)
	}
	if oe.Type != "tool_use" {
		t.Errorf("Type = %q, want %q", oe.Type, "tool_use")
	}
	if oe.Part.Tool != "read" {
		t.Errorf("Part.Tool = %q, want %q", oe.Part.Tool, "read")
	}
}

func TestParseEvent_StepFinish(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	line := []byte(`{"type":"step_finish","sessionID":"abc","part":{"reason":"stop"}}`)
	evt, err := a.ParseEvent(ctx, line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	oe, ok := evt.(*OpenCodeEvent)
	if !ok {
		t.Fatalf("expected *OpenCodeEvent, got %T", evt)
	}
	if oe.Type != "step_finish" {
		t.Errorf("Type = %q, want %q", oe.Type, "step_finish")
	}
	if oe.Part.Reason != "stop" {
		t.Errorf("Part.Reason = %q, want %q", oe.Part.Reason, "stop")
	}
}

func TestParseEvent_Error(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	line := []byte(`{"type":"error","sessionID":"abc","error":{"name":"APIError","data":{"message":"rate limited"}}}`)
	evt, err := a.ParseEvent(ctx, line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	oe, ok := evt.(*OpenCodeEvent)
	if !ok {
		t.Fatalf("expected *OpenCodeEvent, got %T", evt)
	}
	if oe.Type != "error" {
		t.Errorf("Type = %q, want %q", oe.Type, "error")
	}
	if oe.Error.Name != "APIError" {
		t.Errorf("Error.Name = %q, want %q", oe.Error.Name, "APIError")
	}
}

func TestParseEvent_RawText(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	line := []byte("this is not JSON, just raw output")
	evt, err := a.ParseEvent(ctx, line)
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	raw, ok := evt.(map[string]string)
	if !ok {
		t.Fatalf("expected map[string]string, got %T", evt)
	}
	if raw["type"] != "text" {
		t.Errorf("type = %q, want %q", raw["type"], "text")
	}
	if raw["text"] != "this is not JSON, just raw output" {
		t.Errorf("text = %q, want %q", raw["text"], "this is not JSON, just raw output")
	}
}

func TestParseEvent_Empty(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	evt, err := a.ParseEvent(ctx, []byte{})
	if err != nil {
		t.Fatalf("ParseEvent: %v", err)
	}
	if evt != nil {
		t.Fatalf("expected nil for empty input, got %T", evt)
	}
}

func TestHeartbeatTimeout(t *testing.T) {
	a := newTestAdapter()
	if timeout := a.HeartbeatTimeout(); timeout != 90*time.Second {
		t.Errorf("HeartbeatTimeout = %v, want %v", timeout, 90*time.Second)
	}
}

func TestStartCommand(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	args, err := a.StartCommand(ctx, map[string]any{"task": "hello"})
	if err != nil {
		t.Fatalf("StartCommand: %v", err)
	}
	if len(args) < 5 {
		t.Fatalf("expected at least 5 args, got %d: %v", len(args), args)
	}
}

func TestStartCommandMissingTask(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	_, err := a.StartCommand(ctx, map[string]any{})
	if err == nil {
		t.Fatal("expected error when task key is missing")
	}
}

func TestInjectPrompt(t *testing.T) {
	ctx := context.Background()
	a := newTestAdapter()

	_, err := a.InjectPrompt(ctx, "do more work")
	if err == nil {
		t.Fatal("expected error (inject not supported in run mode)")
	}
}

func TestToolDef(t *testing.T) {
	td := ToolDef{
		Name:        "test-tool",
		Description: "a test tool",
		InputSchema: map[string]any{"type": "object"},
	}
	if td.Name != "test-tool" {
		t.Errorf("Name = %q, want %q", td.Name, "test-tool")
	}
}

func TestVerify_findsOpenCode(t *testing.T) {
	if _, err := exec.LookPath("opencode"); err != nil {
		t.Skip("opencode not installed")
	}

	ctx := context.Background()
	a := newTestAdapter()

	if err := a.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}
}

// TestRunOpenCode is an integration test that actually runs opencode.
// It requires FOREMAN_INTEGRATION=1 environment variable.
func TestRunOpenCode(t *testing.T) {
	if os.Getenv("FOREMAN_INTEGRATION") != "1" {
		t.Skip("set FOREMAN_INTEGRATION=1 to run")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	a := newTestAdapter()

	if err := a.Verify(ctx); err != nil {
		t.Fatalf("Verify: %v", err)
	}

	args, err := a.BuildConfig(ctx, BuildConfig{
		TaskDescription: "say hello and nothing else",
		SessionID:       "test-int-1",
	})
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}

	t.Logf("opencode args: %v", args)
	t.Log("FOREMAN_INTEGRATION test: verify only (no actual run)")
}
