package eventbus

import (
	"context"
	"log"
	"strings"
	"sync"

	"github.com/foreman/foreman/internal/schemas"
)

// MemoryBus is an in-process pub/sub bus with NATS-style wildcard matching.
// Use for testing and single-process development.
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
	// Collect matching handlers for all subscriber patterns
	var handlers []EventHandler
	for pattern, hh := range b.subs {
		if matchSubject(pattern, subject) {
			handlers = append(handlers, hh...)
		}
	}
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

// matchSubject checks if a NATS-style pattern matches a subject.
//   - "*" matches exactly one dot-delimited token
//   - ">" matches one or more trailing tokens (must be last in the pattern)
func matchSubject(pattern, subject string) bool {
	if pattern == subject {
		return true
	}

	// Fast path: no wildcards in pattern
	if !strings.ContainsAny(pattern, "*>") {
		return false
	}

	patternParts := strings.Split(pattern, ".")
	subjectParts := strings.Split(subject, ".")

	for i, pp := range patternParts {
		// ">" matches the remainder of the subject (including zero tokens)
		if pp == ">" {
			return i <= len(subjectParts)
		}

		if i >= len(subjectParts) {
			return false
		}

		// "*" matches exactly one token
		if pp == "*" {
			continue
		}

		// Literal token match
		if pp != subjectParts[i] {
			return false
		}
	}

	// All pattern tokens consumed; subject must have the same number of tokens.
	return len(patternParts) == len(subjectParts)
}
