package discord

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

// setupTestPlugin creates a Plugin wired to a mock Discord HTTP server and a
// memory event bus. Returns the plugin and the bus so tests can call handlers
// directly and assert on bus output.
func setupTestPlugin(t *testing.T) (*Plugin, eventbus.EventBus) {
	t.Helper()

	// Mock Discord HTTP API server.
	//   - POST   /interactions/{id}/{token}/callback  -> 204 (InteractionRespond)
	//   - PATCH  /channels/{id}/messages/{msg_id}      -> 200 (ChannelMessageEdit)
	//   - GET    /channels/{id}                         -> 200 with Channel JSON
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case "POST":
			w.WriteHeader(http.StatusNoContent)
		case "PATCH":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{}`))
		case "GET":
			// Extract channel ID from path: /api/v9/channels/{id}
			channelID := ""
			if parts := strings.Split(r.URL.Path, "/"); len(parts) > 0 {
				channelID = parts[len(parts)-1]
			}
			// D-prefixed channels are DMs (type 1), C-prefixed are guild text (type 0).
			channelType := "1"
			if strings.HasPrefix(channelID, "C") {
				channelType = "0"
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id": "` + channelID + `", "type": ` + channelType + `}`))
		default:
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	t.Cleanup(srv.Close)

	// Create a discordgo session with a fake token. discordgo.New initializes
	// State (empty Channels map, nil User).
	sess, err := discordgo.New("Bot fake-token")
	if err != nil {
		t.Fatalf("discordgo.New: %v", err)
	}

	// Replace the HTTP client with one that redirects all requests to the
	// mock server. discordgo builds URLs as baseURL + path; we intercept at
	// the transport level so the actual host doesn't matter.
	sess.Client = &http.Client{Transport: &mockTransport{serverURL: srv.URL}}

	// Set a bot user so handleMessageCreate can compare Author.ID without
	// nil-dereferencing s.State.User.
	sess.State.User = &discordgo.User{ID: "BOT123", Username: "foreman-bot"}

	bus := eventbus.NewMemoryBus()

	p := New(Config{BotToken: "fake-token"})
	p.sess = sess
	p.bus = bus

	return p, bus
}

// mockTransport redirects all HTTP requests to the mock test server.
type mockTransport struct {
	serverURL string
}

func (m *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Rewrite the URL to point at the local mock server, preserving the path.
	req.URL.Scheme = "http"
	req.URL.Host = m.serverURL[len("http://"):]
	if m.serverURL[:4] == "http" {
		// Extract host:port from serverURL.
		hostPart := m.serverURL[len("http://"):]
		req.URL.Host = hostPart
	}
	req.Host = req.URL.Host
	return http.DefaultTransport.RoundTrip(req)
}

// TestDiscordPlugin_SlashCommand verifies that a /foreman slash command
// publishes a user.message event on the event bus.
func TestDiscordPlugin_SlashCommand(t *testing.T) {
	p, bus := setupTestPlugin(t)

	got := make(chan schemas.UserMessage, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.plugin.discord.message",
		func(ctx context.Context, evt schemas.Event) error {
			msg, ok := evt.Payload.(schemas.UserMessage)
			if !ok {
				t.Errorf("expected UserMessage payload, got %T", evt.Payload)
				return nil
			}
			got <- msg
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	p.handleInteraction(p.sess, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionApplicationCommand,
			Data: discordgo.ApplicationCommandInteractionData{
				Name: "foreman",
				Options: []*discordgo.ApplicationCommandInteractionDataOption{
					{
						Name:  "task",
						Type:  discordgo.ApplicationCommandOptionString,
						Value: "deploy to staging",
					},
				},
			},
			ChannelID: "C789012",
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "U123456"},
			},
		},
	})

	select {
	case msg := <-got:
		if msg.Plugin != "discord" {
			t.Errorf("expected plugin 'discord', got %q", msg.Plugin)
		}
		if msg.UserID != "U123456" {
			t.Errorf("expected user ID 'U123456', got %q", msg.UserID)
		}
		if msg.Channel != "C789012" {
			t.Errorf("expected channel 'C789012', got %q", msg.Channel)
		}
		if msg.Text != "deploy to staging" {
			t.Errorf("expected text 'deploy to staging', got %q", msg.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for user.message event")
	}
}

// TestDiscordPlugin_ApproveButton verifies that an approve button click
// publishes an approval.granted event on the event bus.
func TestDiscordPlugin_ApproveButton(t *testing.T) {
	p, bus := setupTestPlugin(t)

	got := make(chan schemas.Event, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.approval.ses_test_123.granted",
		func(ctx context.Context, evt schemas.Event) error {
			got <- evt
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	p.handleInteraction(p.sess, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "approve:ses_test_123",
			},
			ChannelID: "C789012",
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "U123456"},
			},
			Message: &discordgo.Message{
				ID: "MSG999",
			},
		},
	})

	select {
	case evt := <-got:
		if evt.Type != schemas.EvApprovalGranted {
			t.Errorf("expected EvApprovalGranted, got %v", evt.Type)
		}
		if evt.SessionID != "ses_test_123" {
			t.Errorf("expected session ID 'ses_test_123', got %q", evt.SessionID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for approval.granted event")
	}
}

// TestDiscordPlugin_DenyButton verifies that a deny button click publishes an
// approval.denied event on the event bus.
func TestDiscordPlugin_DenyButton(t *testing.T) {
	p, bus := setupTestPlugin(t)

	got := make(chan schemas.Event, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.approval.ses_test_456.denied",
		func(ctx context.Context, evt schemas.Event) error {
			got <- evt
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	p.handleInteraction(p.sess, &discordgo.InteractionCreate{
		Interaction: &discordgo.Interaction{
			Type: discordgo.InteractionMessageComponent,
			Data: discordgo.MessageComponentInteractionData{
				CustomID: "deny:ses_test_456",
			},
			ChannelID: "C789012",
			Member: &discordgo.Member{
				User: &discordgo.User{ID: "U123456"},
			},
			Message: &discordgo.Message{
				ID: "MSG999",
			},
		},
	})

	select {
	case evt := <-got:
		if evt.Type != schemas.EvApprovalDenied {
			t.Errorf("expected EvApprovalDenied, got %v", evt.Type)
		}
		if evt.SessionID != "ses_test_456" {
			t.Errorf("expected session ID 'ses_test_456', got %q", evt.SessionID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for approval.denied event")
	}
}

// TestDiscordPlugin_DMMessage verifies that a DM message publishes a
// user.message event on the event bus.
func TestDiscordPlugin_DMMessage(t *testing.T) {
	p, bus := setupTestPlugin(t)

	// Pre-populate the state with a DM channel so s.Channel() returns from
	// cache without making an API call.
	if err := p.sess.State.ChannelAdd(&discordgo.Channel{
		ID:   "D666",
		Type: discordgo.ChannelTypeDM,
	}); err != nil {
		t.Fatalf("ChannelAdd: %v", err)
	}

	got := make(chan schemas.UserMessage, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.plugin.discord.message",
		func(ctx context.Context, evt schemas.Event) error {
			msg, ok := evt.Payload.(schemas.UserMessage)
			if !ok {
				t.Errorf("expected UserMessage payload, got %T", evt.Payload)
				return nil
			}
			got <- msg
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	p.handleMessageCreate(p.sess, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: "D666",
			Content:   "hello bot",
			Author:    &discordgo.User{ID: "U777"},
		},
	})

	select {
	case msg := <-got:
		if msg.Plugin != "discord" {
			t.Errorf("expected plugin 'discord', got %q", msg.Plugin)
		}
		if msg.UserID != "U777" {
			t.Errorf("expected user ID 'U777', got %q", msg.UserID)
		}
		if msg.Channel != "D666" {
			t.Errorf("expected channel 'D666', got %q", msg.Channel)
		}
		if msg.Text != "hello bot" {
			t.Errorf("expected text 'hello bot', got %q", msg.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for user.message event from DM")
	}
}

// TestDiscordPlugin_NonDMMessageIgnored verifies that a non-DM channel message
// does NOT publish a user.message event (only DMs are handled).
func TestDiscordPlugin_NonDMMessageIgnored(t *testing.T) {
	p, bus := setupTestPlugin(t)

	got := make(chan schemas.UserMessage, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.plugin.discord.message",
		func(ctx context.Context, evt schemas.Event) error {
			if msg, ok := evt.Payload.(schemas.UserMessage); ok {
				got <- msg
			}
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	p.handleMessageCreate(p.sess, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: "C888",
			Content:   "hello everyone",
			Author:    &discordgo.User{ID: "U777"},
		},
	})

	select {
	case <-got:
		t.Fatal("expected no event for non-DM channel message")
	case <-time.After(500 * time.Millisecond):
		// Good -- no event published.
	}
}

// TestDiscordPlugin_OwnMessageIgnored verifies that messages from the bot itself
// are ignored (no user.message event published).
func TestDiscordPlugin_OwnMessageIgnored(t *testing.T) {
	p, bus := setupTestPlugin(t)

	got := make(chan schemas.UserMessage, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.plugin.discord.message",
		func(ctx context.Context, evt schemas.Event) error {
			if msg, ok := evt.Payload.(schemas.UserMessage); ok {
				got <- msg
			}
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	// Message from the bot itself (same ID as sess.State.User.ID = "BOT123").
	p.handleMessageCreate(p.sess, &discordgo.MessageCreate{
		Message: &discordgo.Message{
			ChannelID: "D666",
			Content:   "I should be ignored",
			Author:    &discordgo.User{ID: "BOT123"},
		},
	})

	select {
	case <-got:
		t.Fatal("expected no event for bot's own message")
	case <-time.After(500 * time.Millisecond):
		// Good -- own message ignored.
	}
}
