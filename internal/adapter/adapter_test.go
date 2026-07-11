package adapter

import (
	"context"
	"testing"
	"time"
)

func TestOpenCodeMeta(t *testing.T) {
	a := NewOpenCodeAdapter("opencode", "/tmp", nil, 30*time.Second, 90*time.Second)
	if a.Name() != "opencode" {
		t.Errorf("Name() = %q", a.Name())
	}
	meta := a.Meta()
	if meta.Name != "opencode" {
		t.Errorf("Meta().Name = %q", meta.Name)
	}
}

func TestOpenCodeBuildConfig(t *testing.T) {
	a := NewOpenCodeAdapter("opencode", "/tmp", nil, 30*time.Second, 90*time.Second)
	cfg := BuildConfig{
		TaskDescription: "fix the login bug",
		SessionID:       "ses_1",
	}
	args, err := a.BuildConfig(context.Background(), cfg)
	if err != nil {
		t.Fatalf("BuildConfig: %v", err)
	}
	if len(args) < 2 {
		t.Fatalf("expected at least 2 args, got %d", len(args))
	}
	if args[0] != "opencode" {
		t.Errorf("expected first arg 'opencode', got %q", args[0])
	}
	if args[1] != "run" {
		t.Errorf("expected second arg 'run', got %q", args[1])
	}
}

func TestOpenCodeParseEvent(t *testing.T) {
	a := NewOpenCodeAdapter("opencode", "/tmp", nil, 30*time.Second, 90*time.Second)
	ctx := context.Background()

	tests := []struct {
		name   string
		input  []byte
		wantOk bool
	}{
		{"text event", []byte(`{"type":"text","part":{"text":"hello"}}`), true},
		{"tool_use event", []byte(`{"type":"tool_use","part":{"tool":"Read","state":{"output":"file content"}}}`), true},
		{"step_start event", []byte(`{"type":"step_start","sessionID":"ses_1"}`), true},
		{"step_finish event", []byte(`{"type":"step_finish","part":{"reason":"stop","cost":0.01}}`), true},
		{"error event", []byte(`{"type":"error","error":{"name":"TestError","data":{"message":"something broke"}}}`), true},
		{"raw text line", []byte(`plain text output`), true},
		{"empty line", []byte{}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := a.ParseEvent(ctx, tt.input)
			if err != nil {
				t.Fatalf("ParseEvent: %v", err)
			}
			if tt.wantOk && result == nil {
				t.Fatal("expected non-nil result")
			}
			if !tt.wantOk && result != nil {
				t.Fatal("expected nil result for empty input")
			}
			if result != nil {
				switch v := result.(type) {
				case *OpenCodeEvent:
					if v.Type == "" {
						t.Error("OpenCodeEvent has empty type")
					}
				case map[string]string:
					if v["type"] != "text" {
						t.Errorf("expected type 'text', got %q", v["type"])
					}
				default:
					t.Errorf("unexpected result type %T", v)
				}
			}
		})
	}
}

func TestOpenCodeHeartbeatTimeout(t *testing.T) {
	a := NewOpenCodeAdapter("opencode", "/tmp", nil, 30*time.Second, 90*time.Second)
	if a.HeartbeatTimeout() != 90*time.Second {
		t.Errorf("expected 90s timeout, got %v", a.HeartbeatTimeout())
	}
}

func TestOpenCodeStartCommand(t *testing.T) {
	a := NewOpenCodeAdapter("opencode", "/tmp", nil, 30*time.Second, 90*time.Second)
	ctx := context.Background()

	args, err := a.StartCommand(ctx, map[string]any{"task": "do something"})
	if err != nil {
		t.Fatalf("StartCommand: %v", err)
	}
	if len(args) == 0 {
		t.Fatal("expected non-empty args")
	}

	// Missing task key should return error
	_, err = a.StartCommand(ctx, map[string]any{})
	if err == nil {
		t.Error("expected error for missing task key")
	}
}

func TestOpenCodeInjectPrompt(t *testing.T) {
	a := NewOpenCodeAdapter("opencode", "/tmp", nil, 30*time.Second, 90*time.Second)
	_, err := a.InjectPrompt(context.Background(), "do more stuff")
	if err == nil {
		t.Error("expected error (not supported in run mode)")
	}
}

func TestToolDef(t *testing.T) {
	td := ToolDef{Name: "read", Description: "Read files", InputSchema: "string"}
	if td.Name != "read" {
		t.Errorf("Name = %q", td.Name)
	}
}
