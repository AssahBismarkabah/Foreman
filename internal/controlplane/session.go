package controlplane

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/foreman/foreman/internal/eventbus"
	"github.com/foreman/foreman/internal/schemas"
)

var validTransitions = map[schemas.SessionStatus][]schemas.SessionStatus{
	schemas.StatusCreated:    {schemas.StatusAllocating, schemas.StatusFailed},
	schemas.StatusAllocating: {schemas.StatusRunning, schemas.StatusFailed},
	schemas.StatusRunning:    {schemas.StatusApproval, schemas.StatusCompleted, schemas.StatusCancelling, schemas.StatusFailed},
	schemas.StatusApproval:   {schemas.StatusRunning, schemas.StatusCancelling, schemas.StatusFailed},
	schemas.StatusCancelling: {schemas.StatusFailed, schemas.StatusCompleted},
}

type Session struct {
	ID        string
	Status    schemas.SessionStatus
	TaskID    string
	CreatedAt time.Time
	UpdatedAt time.Time
	mu        sync.RWMutex
}

type ControlPlane struct {
	bus      eventbus.EventBus
	sessions map[string]*Session
	mu       sync.RWMutex
}

func New(bus eventbus.EventBus) *ControlPlane {
	return &ControlPlane{
		bus:      bus,
		sessions: make(map[string]*Session),
	}
}

func (cp *ControlPlane) CreateSession(ctx context.Context, sessionID, taskID string) error {
	s := &Session{
		ID:        sessionID,
		Status:    schemas.StatusCreated,
		TaskID:    taskID,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	cp.mu.Lock()
	cp.sessions[sessionID] = s
	cp.mu.Unlock()
	return cp.emit(ctx, s, schemas.EvSessionCreated)
}

func (cp *ControlPlane) Transition(ctx context.Context, sessionID string, to schemas.SessionStatus) error {
	cp.mu.Lock()
	s, ok := cp.sessions[sessionID]
	cp.mu.Unlock()
	if !ok {
		return fmt.Errorf("session %s not found", sessionID)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	allowed, ok := validTransitions[s.Status]
	if !ok {
		return fmt.Errorf("no transitions defined from %s", s.Status)
	}
	valid := false
	for _, t := range allowed {
		if t == to {
			valid = true
			break
		}
	}
	if !valid {
		return fmt.Errorf("invalid transition %s -> %s", s.Status, to)
	}
	s.Status = to
	s.UpdatedAt = time.Now()

	var evtType schemas.EventType
	switch to {
	case schemas.StatusAllocating:
		evtType = schemas.EvSessionAllocating
	case schemas.StatusRunning:
		evtType = schemas.EvSessionRunning
	case schemas.StatusApproval:
		evtType = schemas.EvSessionApproval
	case schemas.StatusCancelling:
		evtType = schemas.EvSessionCancelling
	case schemas.StatusCompleted:
		evtType = schemas.EvSessionCompleted
	case schemas.StatusFailed:
		evtType = schemas.EvSessionFailed
	default:
		return fmt.Errorf("unknown status: %s", to)
	}
	return cp.emit(ctx, s, evtType)
}

func (cp *ControlPlane) GetSession(sessionID string) (*Session, bool) {
	cp.mu.RLock()
	s, ok := cp.sessions[sessionID]
	cp.mu.RUnlock()
	return s, ok
}

func (cp *ControlPlane) emit(ctx context.Context, s *Session, evtType schemas.EventType) error {
	evt := schemas.Event{
		ID:        fmt.Sprintf("%s-%d", s.ID, time.Now().UnixNano()),
		Type:      evtType,
		SessionID: s.ID,
		Timestamp: time.Now(),
		Payload: schemas.SessionPayload{
			SessionID: s.ID,
			TaskID:    s.TaskID,
			Status:    string(s.Status),
		},
	}
	subject := schemas.SessionEventSubject(s.ID, evtType)
	return cp.bus.Publish(ctx, subject, evt)
}
