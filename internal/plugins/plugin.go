// Package plugins defines the communication plugin interface.
//
// Communication plugins connect Foreman to chat platforms (Slack, Discord).
// Each plugin is compiled into the binary and managed by the core lifecycle.
package plugins

import (
	"context"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

// Plugin is a chat-platform integration.
type Plugin interface {
	Name() string
	Start(ctx context.Context, bus eventbus.EventBus) error
	Stop(ctx context.Context) error
	SendMessage(ctx context.Context, channel string, msg schemas.Message) error
	SendBlockMessage(ctx context.Context, channel string, blocks []schemas.Block) error
}
