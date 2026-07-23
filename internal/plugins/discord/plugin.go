// Package discord implements a Foreman communication plugin for Discord
// using the Gateway API with slash commands and interactive components.
package discord

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

// Config holds the Discord bot credentials.
type Config struct {
	BotToken string // Bot token from Discord Developer Portal
}

// Plugin connects to Discord via the Gateway API.
type Plugin struct {
	cfg  Config
	name string
	bus  eventbus.EventBus

	mu     sync.Mutex
	sess   *discordgo.Session
	cancel context.CancelFunc
	done   chan struct{}
}

// New creates a Discord plugin.
func New(cfg Config) *Plugin {
	return &Plugin{
		cfg:  cfg,
		name: "discord",
		done: make(chan struct{}),
	}
}

// Name returns "discord".
func (p *Plugin) Name() string { return p.name }

// Start connects to Discord and registers the slash command handler.
func (p *Plugin) Start(ctx context.Context, bus eventbus.EventBus) error {
	p.bus = bus

	if p.cfg.BotToken == "" {
		return fmt.Errorf("discord: bot_token is required")
	}

	sess, err := discordgo.New("Bot " + p.cfg.BotToken)
	if err != nil {
		return fmt.Errorf("discord: create session: %w", err)
	}

	sess.AddHandler(func(s *discordgo.Session, r *discordgo.Ready) {
		log.Printf("discord: logged in as %s#%s", r.User.Username, r.User.Discriminator)
	})

	sess.AddHandler(p.handleInteraction)
	sess.AddHandler(p.handleMessageCreate)
	sess.Identify.Intents = discordgo.IntentsGuildMessages | discordgo.IntentsMessageContent | discordgo.IntentsDirectMessages

	if err := sess.Open(); err != nil {
		return fmt.Errorf("discord: open gateway: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	p.mu.Lock()
	p.sess = sess
	p.cancel = cancel
	p.mu.Unlock()

	go p.registerSlashCommands(runCtx, sess)
	go p.watchDisconnect(runCtx, sess)

	return nil
}

// registerSlashCommands creates the /foreman slash command in all guilds.
// It waits up to 30s for guild state to be populated after connecting.
func (p *Plugin) registerSlashCommands(ctx context.Context, sess *discordgo.Session) {
	cmd := &discordgo.ApplicationCommand{
		Name:        "foreman",
		Description: "Submit a task to Foreman",
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "task",
				Description: "Describe what you want done",
				Required:    true,
			},
		},
	}

	// Wait for guild state to be populated (GUILD_CREATE events arrive after Ready).
	for deadline := time.Now().Add(30 * time.Second); len(sess.State.Guilds) == 0 && time.Now().Before(deadline); {
		select {
		case <-ctx.Done():
			log.Printf("discord: context cancelled while waiting for guilds")
			return
		case <-time.After(500 * time.Millisecond):
		}
	}

	if len(sess.State.Guilds) == 0 {
		log.Printf("discord: no guilds found after 30s, skipping command registration")
		return
	}

	for _, guild := range sess.State.Guilds {
		existing, err := sess.ApplicationCommandCreate(sess.State.User.ID, guild.ID, cmd)
		if err != nil {
			log.Printf("discord: create command for guild %s: %v", guild.ID, err)
		} else {
			log.Printf("discord: registered /foreman in guild %s (id=%s)", guild.ID, existing.ID)
		}
	}
}

// watchDisconnect monitors the gateway connection and attempts reconnection.
func (p *Plugin) watchDisconnect(ctx context.Context, sess *discordgo.Session) {
	defer close(p.done)

	<-ctx.Done()

	p.mu.Lock()
	s := p.sess
	p.mu.Unlock()
	if s != nil {
		_ = s.Close()
	}
}

// Stop gracefully disconnects from Discord.
func (p *Plugin) Stop(ctx context.Context) error {
	p.mu.Lock()
	cancel := p.cancel
	p.mu.Unlock()

	if cancel == nil {
		return nil // never started
	}
	cancel()

	select {
	case <-p.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	return nil
}

// SendMessage posts a simple text message to a Discord channel.
func (p *Plugin) SendMessage(ctx context.Context, channel string, msg schemas.Message) error {
	p.mu.Lock()
	sess := p.sess
	p.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("discord: not connected (call Start first)")
	}

	var params discordgo.MessageSend
	params.Content = msg.Text
	if msg.Thread != "" {
		params.Reference = &discordgo.MessageReference{MessageID: msg.Thread}
		// Discord thread replies use the thread ID as the channel
		sendCh := msg.Thread
		_, err := sess.ChannelMessageSendComplex(sendCh, &params)
		return err
	}
	_, err := sess.ChannelMessageSendComplex(channel, &params)
	return err
}

// SendBlockMessage posts a rich message with embeds and components.
func (p *Plugin) SendBlockMessage(ctx context.Context, channel string, blocks []schemas.Block) error {
	p.mu.Lock()
	sess := p.sess
	p.mu.Unlock()
	if sess == nil {
		return fmt.Errorf("discord: not connected (call Start first)")
	}

	var params discordgo.MessageSend
	for _, b := range blocks {
		switch b.Type {
		case "section":
			embed := &discordgo.MessageEmbed{}
			if title, ok := b.Fields["title"].(string); ok {
				embed.Title = title
			}
			if text, ok := b.Fields["text"].(string); ok {
				embed.Description = text
			}
			if color, ok := b.Fields["color"].(int); ok {
				embed.Color = color
			}
			params.Embeds = append(params.Embeds, embed)
		case "actions":
			row := discordgo.ActionsRow{}
			for _, e := range b.Elements {
				if btn, ok := e["button"].(map[string]any); ok {
					row.Components = append(row.Components, discordgo.Button{
						Label:    toString(btn["text"]),
						CustomID: toString(btn["action_id"]),
						Style:    buttonStyle(toString(btn["style"])),
					})
				}
			}
			if len(row.Components) > 0 {
				params.Components = append(params.Components, row)
			}
		case "context":
			if text, ok := b.Fields["text"].(string); ok {
				if len(params.Embeds) > 0 {
					params.Embeds[len(params.Embeds)-1].Footer = &discordgo.MessageEmbedFooter{
						Text: text,
					}
				}
			}
		case "divider":
			// Discord has no divider concept; skip
		}
	}

	_, err := sess.ChannelMessageSendComplex(channel, &params)
	return err
}

// handleInteraction processes slash commands and component interactions.
func (p *Plugin) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	switch i.Type {
	case discordgo.InteractionApplicationCommand:
		p.handleSlashCommand(s, i)
	case discordgo.InteractionMessageComponent:
		p.handleComponent(s, i)
	}
}

func (p *Plugin) handleSlashCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.ApplicationCommandData().Name != "foreman" {
		return
	}

	// Defer the response to give us time to process.
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("discord: defer response: %v", err)
		return
	}

	// Extract the task text from options.
	task := ""
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "task" {
			task = opt.StringValue()
		}
	}

	// Publish user.message event for the control plane.
	msg := schemas.UserMessage{
		Plugin:  "discord",
		UserID:  i.Member.User.ID,
		Channel: i.ChannelID,
		Text:    task,
	}
	p.publishUserMessage(i.ChannelID, msg)
}

// handleComponent processes button clicks (approve/deny).
func (p *Plugin) handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	customID := i.MessageComponentData().CustomID

	// Acknowledge the interaction.
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredMessageUpdate,
	}); err != nil {
		log.Printf("discord: defer component: %v", err)
		return
	}

	// Custom ID format: "approve:<sessionID>" or "deny:<sessionID>"
	var action, sessionID string
	if idx := strings.Index(customID, ":"); idx > 0 {
		action = customID[:idx]
		sessionID = customID[idx+1:]
	}
	if sessionID == "" {
		log.Printf("discord: approval button without sessionID in custom_id: %q", customID)
		return
	}

	switch action {
	case "approve", "deny":
		evtLabel := "granted"
		evtType := schemas.EvApprovalGranted
		if action == "deny" {
			evtLabel = "denied"
			evtType = schemas.EvApprovalDenied
		}
		if err := p.bus.Publish(context.Background(),
			schemas.Subject("approval", sessionID, evtLabel),
			schemas.Event{
				ID:        fmt.Sprintf("approval-%s-%d", sessionID, time.Now().UnixNano()),
				Type:      evtType,
				SessionID: sessionID,
				Timestamp: time.Now(),
				Payload: map[string]string{
					"user_id":    i.Member.User.ID,
					"channel_id": i.ChannelID,
					"action":     action,
				},
			}); err != nil {
			log.Printf("discord: publish approval event: %v", err)
		}

		// Update the original message to show the decision.
		content := "Approved :white_check_mark:"
		if action == "deny" {
			content = "Denied :no_entry_sign:"
		}
		_, _ = s.ChannelMessageEdit(i.ChannelID, i.Message.ID, content)
	}
}

// handleMessageCreate processes direct messages to the bot.
func (p *Plugin) handleMessageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {
	if m.Author.ID == s.State.User.ID {
		return // ignore own messages
	}

	// Only handle DMs (channel type 1 = DM)
	ch, err := s.Channel(m.ChannelID)
	if err != nil || ch.Type != discordgo.ChannelTypeDM {
		return
	}

	msg := schemas.UserMessage{
		Plugin:  "discord",
		UserID:  m.Author.ID,
		Channel: m.ChannelID,
		Text:    m.Content,
	}
	p.publishUserMessage(m.ChannelID, msg)
}

func (p *Plugin) publishUserMessage(channelID string, msg schemas.UserMessage) {
	if p.bus == nil {
		return
	}
	evt := schemas.Event{
		ID:        fmt.Sprintf("discord-%s-%d", channelID, time.Now().UnixNano()),
		Type:      schemas.EvUserMessage,
		SessionID: "",
		Timestamp: time.Now(),
		Payload:   msg,
	}
	if err := p.bus.Publish(context.Background(), schemas.Subject("plugin", "discord", "message"), evt); err != nil {
		log.Printf("discord: publish user message: %v", err)
	}
}

func toString(v any) string {
	s, _ := v.(string)
	return s
}

func buttonStyle(style string) discordgo.ButtonStyle {
	switch style {
	case "primary":
		return discordgo.PrimaryButton
	case "secondary":
		return discordgo.SecondaryButton
	case "success":
		return discordgo.SuccessButton
	case "danger":
		return discordgo.DangerButton
	default:
		return discordgo.PrimaryButton
	}
}
