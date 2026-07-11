package eventbus

import (
	"context"

	"github.com/foreman/foreman/internal/schemas"
)

type EventHandler func(ctx context.Context, evt schemas.Event) error

type EventBus interface {
	Publish(ctx context.Context, subject string, evt schemas.Event) error
	Subscribe(ctx context.Context, subject string, handler EventHandler) (func(), error)
	Close() error
}
