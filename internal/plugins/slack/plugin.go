// Package slack implements a Foreman communication plugin for Slack
// using Socket Mode (WebSocket-based event subscription).
package slack

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

// Config holds Slack app credentials.
type Config struct {
	BotToken      string `yaml:"bot_token"` // xoxb-*
	AppToken      string `yaml:"app_token"` // xapp-*
	SigningSecret string `yaml:"signing_secret"`
}

// Plugin connects to Slack via Socket Mode.
type Plugin struct {
	cfg    Config
	name   string
	bus    eventbus.EventBus
	client *slack.Client
	sm     *socketmode.Client

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a Slack plugin.
func New(cfg Config) *Plugin {
	return &Plugin{
		cfg:  cfg,
		name: "slack",
		done: make(chan struct{}),
	}
}

// Name returns "slack".
func (p *Plugin) Name() string { return p.name }

// Start connects to Slack Socket Mode and begins processing events.
func (p *Plugin) Start(ctx context.Context, bus eventbus.EventBus) error {
	p.bus = bus

	if p.cfg.BotToken == "" {
		return fmt.Errorf("slack: bot_token is required")
	}
	if p.cfg.AppToken == "" {
		return fmt.Errorf("slack: app_token is required")
	}

	api := slack.New(
		p.cfg.BotToken,
		slack.OptionAppLevelToken(p.cfg.AppToken),
		slack.OptionDebug(false),
	)

	sm := socketmode.New(api, socketmode.OptionDebug(false))
	p.client = api
	p.sm = sm

	runCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.cancel = cancel
	p.mu.Unlock()

	go p.run(runCtx)

	return nil
}

// Stop closes the Socket Mode connection.
func (p *Plugin) Stop(ctx context.Context) error {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()
	if cancel != nil {
		cancel()
	}

	select {
	case <-p.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// SendMessage posts a simple text message to a Slack channel.
func (p *Plugin) SendMessage(ctx context.Context, channel string, msg schemas.Message) error {
	if p.client == nil {
		return fmt.Errorf("slack: client not initialized (call Start first)")
	}
	opts := []slack.MsgOption{slack.MsgOptionText(msg.Text, false)}
	if msg.Thread != "" {
		opts = append(opts, slack.MsgOptionTS(msg.Thread))
	}
	_, _, err := p.client.PostMessageContext(ctx, channel, opts...)
	return err
}

// SendBlockMessage posts a rich message using Slack Block Kit blocks.
func (p *Plugin) SendBlockMessage(ctx context.Context, channel string, blocks []schemas.Block) error {
	if p.client == nil {
		return fmt.Errorf("slack: client not initialized (call Start first)")
	}
	slackBlocks := make([]slack.Block, 0, len(blocks))
	for _, b := range blocks {
		slackBlocks = append(slackBlocks, toSlackBlock(b))
	}
	_, _, err := p.client.PostMessageContext(ctx, channel, slack.MsgOptionBlocks(slackBlocks...))
	return err
}

// run is the Socket Mode event loop.
func (p *Plugin) run(ctx context.Context) {
	defer close(p.done)
	defer p.recoverPanic()

	// Channel-based event loop (simpler than handler-based for our use case)
	go func() {
		if err := p.sm.RunContext(ctx); err != nil {
			log.Printf("slack: socketmode run: %v", err)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case evt, ok := <-p.sm.Events:
			if !ok {
				return
			}
			p.handleEvent(ctx, evt)
		}
	}
}

func (p *Plugin) recoverPanic() {
	if r := recover(); r != nil {
		log.Printf("slack: panic in event loop: %v", r)
	}
}

func (p *Plugin) handleEvent(ctx context.Context, evt socketmode.Event) {
	switch evt.Type {
	case socketmode.EventTypeConnecting:
		log.Println("slack: connecting...")
	case socketmode.EventTypeConnected:
		log.Println("slack: connected")
	case socketmode.EventTypeConnectionError:
		log.Printf("slack: connection error: %v", evt.Data)
	case socketmode.EventTypeSlashCommand:
		p.handleSlashCommand(ctx, evt)
	case socketmode.EventTypeInteractive:
		p.handleInteractive(ctx, evt)
	case socketmode.EventTypeEventsAPI:
		p.handleEventsAPI(ctx, evt)
	}
}

func (p *Plugin) handleSlashCommand(ctx context.Context, evt socketmode.Event) {
	cmd, ok := evt.Data.(slack.SlashCommand)
	if !ok {
		return
	}
	p.ack(evt)

	// Support optional agent prefix: "opencode: fix the bug" or "exec: ls -la"
	text := cmd.Text
	agent := ""
	if idx := strings.Index(text, ":"); idx > 0 && idx < 20 {
		prefix := strings.TrimSpace(text[:idx])
		if prefix == "opencode" || prefix == "exec" {
			agent = prefix
			text = strings.TrimSpace(text[idx+1:])
		}
	}

	// Publish a user.message event for the control plane.
	msg := schemas.UserMessage{
		Plugin:  "slack",
		UserID:  cmd.UserID,
		Channel: cmd.ChannelID,
		Text:    text,
		Agent:   agent,
	}
	p.publishUserMessage(ctx, msg)

	// Post a "thinking" ephemeral message.
	_, _ = p.client.PostEphemeralContext(ctx, cmd.ChannelID, cmd.UserID,
		slack.MsgOptionText("Got it! I'll start working on that now...", false))
}

func (p *Plugin) handleInteractive(ctx context.Context, evt socketmode.Event) {
	callback, ok := evt.Data.(slack.InteractionCallback)
	if !ok {
		return
	}
	p.ack(evt)

	switch callback.Type {
	case slack.InteractionTypeBlockActions:
		for _, action := range callback.ActionCallback.BlockActions {
			switch action.ActionID {
			case "approve", "deny":
				sessionID := action.Value
				if sessionID == "" {
					log.Printf("slack: approval button pressed without session ID in value")
					return
				}
				evtType := schemas.EvApprovalGranted
				evtLabel := "granted"
				if action.ActionID == "deny" {
					evtType = schemas.EvApprovalDenied
					evtLabel = "denied"
				}
				if err := p.bus.Publish(ctx, schemas.Subject("approval", sessionID, evtLabel), schemas.Event{
					ID:        fmt.Sprintf("approval-%s-%d", sessionID, time.Now().UnixNano()),
					Type:      evtType,
					SessionID: sessionID,
					Timestamp: time.Now(),
					Payload: map[string]string{
						"user_id":    callback.User.ID,
						"channel_id": callback.Channel.ID,
						"action":     action.ActionID,
					},
				}); err != nil {
					log.Printf("slack: publish approval event: %v", err)
				}
			}
		}
	}
}

func (p *Plugin) handleEventsAPI(ctx context.Context, evt socketmode.Event) {
	eventsAPIEvent, ok := evt.Data.(slackevents.EventsAPIEvent)
	if !ok {
		return
	}
	p.ack(evt)

	switch inner := eventsAPIEvent.InnerEvent.Data.(type) {
	case *slackevents.AppMentionEvent:
		msg := schemas.UserMessage{
			Plugin:  "slack",
			UserID:  inner.User,
			Channel: inner.Channel,
			Text:    inner.Text,
		}
		p.publishUserMessage(ctx, msg)
	case *slackevents.MessageEvent:
		// Only handle direct messages to the bot (im channel)
		if inner.ChannelType == "im" {
			// Support optional agent prefix: "opencode: fix the bug" or "exec: ls -la"
			text := inner.Text
			agent := ""
			if idx := strings.Index(text, ":"); idx > 0 && idx < 20 {
				prefix := strings.TrimSpace(text[:idx])
				if prefix == "opencode" || prefix == "exec" {
					agent = prefix
					text = strings.TrimSpace(text[idx+1:])
				}
			}

			msg := schemas.UserMessage{
				Plugin:  "slack",
				UserID:  inner.User,
				Channel: inner.Channel,
				Text:    text,
				Agent:   agent,
			}
			p.publishUserMessage(ctx, msg)
		}
	}
}

func (p *Plugin) publishUserMessage(ctx context.Context, msg schemas.UserMessage) {
	evt := schemas.Event{
		ID:        fmt.Sprintf("slack-%s-%d", msg.Channel, time.Now().UnixNano()),
		Type:      schemas.EvUserMessage,
		SessionID: "",
		Timestamp: time.Now(),
		Payload:   msg,
	}
	if err := p.bus.Publish(ctx, schemas.Subject("plugin", "slack", "message"), evt); err != nil {
		log.Printf("slack: publish user message: %v", err)
	}
}

// ack acknowledges a Socket Mode event so Slack doesn't retry.
// If the event has no request envelope, the ack is a no-op.
func (p *Plugin) ack(evt socketmode.Event) {
	if evt.Request == nil || evt.Request.EnvelopeID == "" {
		return
	}
	_ = p.sm.AckCtx(context.Background(), evt.Request.EnvelopeID, nil)
}

// toSlackBlock converts a schemas.Block to a slack.Block.
func toSlackBlock(b schemas.Block) slack.Block {
	switch b.Type {
	case "section":
		var text *slack.TextBlockObject
		if txt, ok := b.Fields["text"].(string); ok {
			text = slack.NewTextBlockObject("mrkdwn", txt, false, false)
		}
		return slack.NewSectionBlock(text, nil, nil)
	case "actions":
		var elements []slack.BlockElement
		for _, e := range b.Elements {
			if btn, ok := e["button"].(map[string]any); ok {
				elements = append(elements, slack.NewButtonBlockElement(
					toString(btn["action_id"]),
					toString(btn["value"]),
					slack.NewTextBlockObject("plain_text", toString(btn["text"]), false, false),
				))
			}
		}
		return slack.NewActionBlock("", elements...)
	case "context":
		var elements []slack.MixedElement
		if txt, ok := b.Fields["text"].(string); ok {
			elements = append(elements, slack.NewTextBlockObject("mrkdwn", txt, false, false))
		}
		return slack.NewContextBlock("", elements...)
	case "divider":
		return slack.NewDividerBlock()
	default:
		return slack.NewSectionBlock(
			slack.NewTextBlockObject("mrkdwn", fmt.Sprintf("unknown block type: %s", b.Type), false, false),
			nil, nil,
		)
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}
