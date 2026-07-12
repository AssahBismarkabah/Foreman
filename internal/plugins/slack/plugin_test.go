package slack

import (
	"context"
	"testing"

	"github.com/foreman/foreman/internal/schemas"
	"github.com/slack-go/slack"
)

func TestNew(t *testing.T) {
	p := New(Config{BotToken: "xoxb-test", AppToken: "xapp-test"})
	if p.Name() != "slack" {
		t.Fatalf("expected name 'slack', got %q", p.Name())
	}
}

func TestStart_NoBotToken(t *testing.T) {
	p := New(Config{AppToken: "xapp-test"})
	err := p.Start(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for missing bot_token")
	}
}

func TestStart_NoAppToken(t *testing.T) {
	p := New(Config{BotToken: "xoxb-test"})
	err := p.Start(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for missing app_token")
	}
}

func TestSendMessage_NilClient(t *testing.T) {
	p := New(Config{BotToken: "xoxb-test", AppToken: "xapp-test"})
	// Without Start(), client is nil -- SendMessage should not panic.
	err := p.SendMessage(context.Background(), "C123", schemas.Message{Text: "hello"})
	if err == nil {
		t.Fatal("expected error with nil client")
	}
}

func TestSendBlockMessage_NilClient(t *testing.T) {
	p := New(Config{BotToken: "xoxb-test", AppToken: "xapp-test"})
	err := p.SendBlockMessage(context.Background(), "C123", []schemas.Block{
		{Type: "section", Fields: map[string]any{"text": "hello"}},
	})
	if err == nil {
		t.Fatal("expected error with nil client")
	}
}

func TestToSlackBlock_Section(t *testing.T) {
	b := toSlackBlock(schemas.Block{
		Type:   "section",
		Fields: map[string]any{"text": "hello world"},
	})
	section, ok := b.(*slack.SectionBlock)
	if !ok {
		t.Fatalf("expected SectionBlock, got %T", b)
	}
	if section.Text == nil {
		t.Fatal("expected non-nil Text")
	}
	if section.Text.Text != "hello world" {
		t.Fatalf("expected text 'hello world', got %q", section.Text.Text)
	}
}

func TestToSlackBlock_Actions(t *testing.T) {
	b := toSlackBlock(schemas.Block{
		Type: "actions",
		Elements: []map[string]any{
			{"button": map[string]any{
				"action_id": "approve",
				"value":     "yes",
				"text":      "Approve",
			}},
		},
	})
	actions, ok := b.(*slack.ActionBlock)
	if !ok {
		t.Fatalf("expected ActionBlock, got %T", b)
	}
	if actions.Elements == nil || len(actions.Elements.ElementSet) != 1 {
		t.Fatalf("expected 1 element, got %d", len(actions.Elements.ElementSet))
	}
}

func TestToSlackBlock_Context(t *testing.T) {
	b := toSlackBlock(schemas.Block{
		Type:   "context",
		Fields: map[string]any{"text": "some context"},
	})
	contextBlock, ok := b.(*slack.ContextBlock)
	if !ok {
		t.Fatalf("expected ContextBlock, got %T", b)
	}
	if len(contextBlock.ContextElements.Elements) != 1 {
		t.Fatalf("expected 1 context element, got %d", len(contextBlock.ContextElements.Elements))
	}
}

func TestToSlackBlock_Divider(t *testing.T) {
	b := toSlackBlock(schemas.Block{Type: "divider"})
	if _, ok := b.(*slack.DividerBlock); !ok {
		t.Fatalf("expected DividerBlock, got %T", b)
	}
}

func TestToSlackBlock_Unknown(t *testing.T) {
	b := toSlackBlock(schemas.Block{Type: "unknown_type"})
	if _, ok := b.(*slack.SectionBlock); !ok {
		t.Fatalf("expected SectionBlock (fallback), got %T", b)
	}
}
