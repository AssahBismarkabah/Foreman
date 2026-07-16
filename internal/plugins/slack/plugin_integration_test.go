package slack

import (
	"context"
	"testing"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/slacktest"
	"github.com/slack-go/slack/socketmode"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

// setupTestPlugin creates a Plugin wired to a mock Slack server and a memory
// event bus. Returns the plugin, the mock server, and the bus so tests can
// inject events and assert on bus output.
func setupTestPlugin(t *testing.T) (*Plugin, *slacktest.Server, eventbus.EventBus) {
	t.Helper()

	// Start a mock Slack HTTP API server.
	srv := slacktest.NewTestServer()
	srv.Start()
	t.Cleanup(srv.Stop)

	// Create a Slack client pointing at the mock server.
	api := slack.New(
		"xoxb-fake-bot-token",
		slack.OptionAppLevelToken("xapp-fake-app-token"),
		slack.OptionAPIURL(srv.GetAPIURL()),
	)

	// Create a Socket Mode client. Events is a public channel (buffer 50)
	// so we can inject events without a real WebSocket connection.
	sm := socketmode.New(api, socketmode.OptionDebug(false))

	bus := eventbus.NewMemoryBus()

	p := New(Config{
		BotToken: "xoxb-fake-bot-token",
		AppToken: "xapp-fake-app-token",
	})
	p.client = api
	p.sm = sm
	p.bus = bus

	return p, srv, bus
}

// TestSlackPlugin_SlashCommand verifies that a /foreman slash command event
// publishes a user.message event on the event bus.
func TestSlackPlugin_SlashCommand(t *testing.T) {
	p, _, bus := setupTestPlugin(t)

	// Subscribe to plugin.slack.message events.
	got := make(chan schemas.UserMessage, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.plugin.slack.message",
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

	// Inject a slash command event.
	p.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeSlashCommand,
		Data: slack.SlashCommand{
			Command:   "/foreman",
			Text:      "deploy to staging",
			UserID:    "U123456",
			ChannelID: "C789012",
		},
	})

	select {
	case msg := <-got:
		if msg.Plugin != "slack" {
			t.Errorf("expected plugin 'slack', got %q", msg.Plugin)
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

// TestSlackPlugin_ApproveButton verifies that an approve button click publishes
// an approval.granted event on the event bus.
func TestSlackPlugin_ApproveButton(t *testing.T) {
	p, _, bus := setupTestPlugin(t)

	got := make(chan schemas.Event, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.approval.ses_test_123.granted",
		func(ctx context.Context, evt schemas.Event) error {
			got <- evt
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	p.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: slack.InteractionCallback{
			Type: slack.InteractionTypeBlockActions,
			ActionCallback: slack.ActionCallbacks{
				BlockActions: []*slack.BlockAction{
					{
						ActionID: "approve",
						Value:    "ses_test_123",
					},
				},
			},
			User: slack.User{
				ID: "U123456",
			},
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C789012",
					},
				},
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

// TestSlackPlugin_DenyButton verifies that a deny button click publishes an
// approval.denied event on the event bus.
func TestSlackPlugin_DenyButton(t *testing.T) {
	p, _, bus := setupTestPlugin(t)

	got := make(chan schemas.Event, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.approval.ses_test_456.denied",
		func(ctx context.Context, evt schemas.Event) error {
			got <- evt
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	p.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeInteractive,
		Data: slack.InteractionCallback{
			Type: slack.InteractionTypeBlockActions,
			ActionCallback: slack.ActionCallbacks{
				BlockActions: []*slack.BlockAction{
					{
						ActionID: "deny",
						Value:    "ses_test_456",
					},
				},
			},
			User: slack.User{
				ID: "U123456",
			},
			Channel: slack.Channel{
				GroupConversation: slack.GroupConversation{
					Conversation: slack.Conversation{
						ID: "C789012",
					},
				},
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

// TestSlackPlugin_AppMention verifies that an app_mention event publishes a
// user.message event on the event bus.
func TestSlackPlugin_AppMention(t *testing.T) {
	p, _, bus := setupTestPlugin(t)

	got := make(chan schemas.UserMessage, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.plugin.slack.message",
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

	// Construct an EventsAPIEvent with an AppMentionEvent inner event.
	eventsAPIEvent := slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.AppMentionEvent{
				User:    "U999",
				Channel: "C888",
				Text:    "<@BOT123> check the logs",
			},
		},
	}

	p.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: eventsAPIEvent,
	})

	select {
	case msg := <-got:
		if msg.UserID != "U999" {
			t.Errorf("expected user ID 'U999', got %q", msg.UserID)
		}
		if msg.Channel != "C888" {
			t.Errorf("expected channel 'C888', got %q", msg.Channel)
		}
		if msg.Text != "<@BOT123> check the logs" {
			t.Errorf("expected mention text, got %q", msg.Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for user.message event from app_mention")
	}
}

// TestSlackPlugin_DMMessage verifies that a DM (im channel) message event
// publishes a user.message event on the event bus.
func TestSlackPlugin_DMMessage(t *testing.T) {
	p, _, bus := setupTestPlugin(t)

	got := make(chan schemas.UserMessage, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.plugin.slack.message",
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

	eventsAPIEvent := slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.MessageEvent{
				User:        "U777",
				Channel:     "D666",
				Text:        "hello bot",
				ChannelType: "im",
			},
		},
	}

	p.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: eventsAPIEvent,
	})

	select {
	case msg := <-got:
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

// TestSlackPlugin_NonDMMessageIgnored verifies that a non-DM channel message
// does NOT publish a user.message event (only DMs are handled).
func TestSlackPlugin_NonDMMessageIgnored(t *testing.T) {
	p, _, bus := setupTestPlugin(t)

	got := make(chan schemas.UserMessage, 1)
	_, err := bus.Subscribe(context.Background(), "foreman.plugin.slack.message",
		func(ctx context.Context, evt schemas.Event) error {
			if msg, ok := evt.Payload.(schemas.UserMessage); ok {
				got <- msg
			}
			return nil
		})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	eventsAPIEvent := slackevents.EventsAPIEvent{
		InnerEvent: slackevents.EventsAPIInnerEvent{
			Data: &slackevents.MessageEvent{
				User:        "U777",
				Channel:     "C888",
				Text:        "hello everyone",
				ChannelType: "channel",
			},
		},
	}

	p.handleEvent(context.Background(), socketmode.Event{
		Type: socketmode.EventTypeEventsAPI,
		Data: eventsAPIEvent,
	})

	select {
	case <-got:
		t.Fatal("expected no event for non-DM channel message")
	case <-time.After(500 * time.Millisecond):
		// Good -- no event published.
	}
}
