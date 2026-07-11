package eventbus

import (
	"context"
	"log"
	"sync"

	"github.com/foreman/foreman/internal/schemas"
)

type MemoryBus struct {
	mu   sync.RWMutex
	subs map[string][]EventHandler
}

func NewMemoryBus() *MemoryBus {
	return &MemoryBus{
		subs: make(map[string][]EventHandler),
	}
}

func (b *MemoryBus) Publish(ctx context.Context, subject string, evt schemas.Event) error {
	b.mu.RLock()
	handlers := b.subs[subject]
	b.mu.RUnlock()
	for _, h := range handlers {
		if err := h(ctx, evt); err != nil {
			log.Printf("eventbus: handler error on %s: %v", subject, err)
		}
	}
	return nil
}

func (b *MemoryBus) Subscribe(ctx context.Context, subject string, handler EventHandler) (func(), error) {
	b.mu.Lock()
	id := len(b.subs[subject])
	b.subs[subject] = append(b.subs[subject], handler)
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		handlers := b.subs[subject]
		if id < len(handlers) {
			b.subs[subject] = append(handlers[:id], handlers[id+1:]...)
		}
	}, nil
}

func (b *MemoryBus) Close() error {
	return nil
}
