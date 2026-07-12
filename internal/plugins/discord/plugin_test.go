package discord

import (
	"context"
	"testing"
	"time"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

func TestNew(t *testing.T) {
	p := New(Config{BotToken: "test-token"})
	if p.Name() != "discord" {
		t.Errorf("expected name discord, got %s", p.Name())
	}
}

func TestStart_EmptyToken(t *testing.T) {
	p := New(Config{BotToken: ""})
	err := p.Start(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty token")
	}
}

func TestStart_InvalidToken(t *testing.T) {
	// A bad token should fail to connect.
	// This test makes a real Discord API call and takes ~4s.
	// It's kept as a basic integration check but can be skipped in short mode.
	if testing.Short() {
		t.Skip("skipping network-dependent test in short mode")
	}
	p := New(Config{BotToken: "not-a-valid-token"})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := p.Start(ctx, eventbus.NewMemoryBus())
	if err == nil {
		t.Fatal("expected error for invalid token")
	}
}

func TestName(t *testing.T) {
	p := New(Config{BotToken: "test"})
	if got := p.Name(); got != "discord" {
		t.Errorf("Name() = %q, want %q", got, "discord")
	}
}

func TestSendMessage_NilClient(t *testing.T) {
	p := New(Config{BotToken: "test"})
	err := p.SendMessage(context.Background(), "ch", schemas.Message{Text: "hello"})
	if err == nil {
		t.Fatal("expected error when client not initialized")
	}
}

func TestSendBlockMessage_NilClient(t *testing.T) {
	p := New(Config{BotToken: "test"})
	err := p.SendBlockMessage(context.Background(), "ch", []schemas.Block{
		{Type: "section", Fields: map[string]any{"text": "hello"}},
	})
	if err == nil {
		t.Fatal("expected error when client not initialized")
	}
}

func TestStop_Idempotent(t *testing.T) {
	p := New(Config{BotToken: "test"})
	// Stopping a plugin that was never started should not panic.
	ctx := context.Background()
	if err := p.Stop(ctx); err != nil {
		t.Errorf("Stop on unstarted plugin returned error: %v", err)
	}
}

func TestToString(t *testing.T) {
	tests := []struct {
		input any
		want  string
	}{
		{"hello", "hello"},
		{42, ""},
		{nil, ""},
		{struct{}{}, ""},
	}
	for _, tt := range tests {
		if got := toString(tt.input); got != tt.want {
			t.Errorf("toString(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestButtonStyle(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"primary", 1},
		{"secondary", 2},
		{"success", 3},
		{"danger", 4},
		{"unknown", 1},
		{"", 1},
	}
	for _, tt := range tests {
		got := buttonStyle(tt.input)
		if int(got) != tt.want {
			t.Errorf("buttonStyle(%q) = %d, want %d", tt.input, int(got), tt.want)
		}
	}
}
