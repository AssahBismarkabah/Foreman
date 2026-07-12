package githubapp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/foreman/foreman/internal/identity"
)

type recordingStore struct {
	created []*identity.Installation
	states  []struct {
		ID    int64
		State identity.InstallationState
	}
}

func (s *recordingStore) Create(_ context.Context, inst *identity.Installation) error {
	s.created = append(s.created, inst)
	return nil
}

func (s *recordingStore) GetByPlatformID(_ context.Context, _ int64) (*identity.Installation, error) {
	return nil, nil
}

func (s *recordingStore) UpdateState(_ context.Context, platformInstallID int64, state identity.InstallationState) error {
	s.states = append(s.states, struct {
		ID    int64
		State identity.InstallationState
	}{platformInstallID, state})
	return nil
}

func (s *recordingStore) Delete(_ context.Context, _ int64) error { return nil }
func (s *recordingStore) ListByAccount(_ context.Context, _ int64) ([]identity.Installation, error) {
	return nil, nil
}

func TestWebhookHandler_InstallationCreated(t *testing.T) {
	store := &recordingStore{}
	cfg := &identity.GitHubAppConfig{}
	client := NewClient(cfg, store)
	handler := NewWebhookHandler(cfg, client, store)

	payload := map[string]any{
		"action": "created",
		"installation": map[string]any{
			"id": int64(42),
			"account": map[string]any{
				"id":    int64(100),
				"login": "test-org",
				"type":  "Organization",
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "installation")
	req.Header.Set("X-Hub-Signature-256", "sha256=test")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(store.created) != 1 {
		t.Fatalf("expected 1 created installation, got %d", len(store.created))
	}
	if store.created[0].PlatformInstallID != 42 {
		t.Fatalf("expected install ID 42, got %d", store.created[0].PlatformInstallID)
	}
	if store.created[0].AccountLogin != "test-org" {
		t.Fatalf("expected account 'test-org', got %q", store.created[0].AccountLogin)
	}
	if store.created[0].State != identity.InstallationActive {
		t.Fatalf("expected active state, got %v", store.created[0].State)
	}
}

func TestWebhookHandler_InstallationDeleted(t *testing.T) {
	store := &recordingStore{}
	cfg := &identity.GitHubAppConfig{}
	client := NewClient(cfg, store)
	handler := NewWebhookHandler(cfg, client, store)

	payload := map[string]any{
		"action": "deleted",
		"installation": map[string]any{
			"id": int64(99),
			"account": map[string]any{
				"id":    int64(200),
				"login": "user-a",
				"type":  "User",
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "installation")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if len(store.states) != 1 {
		t.Fatalf("expected 1 state update, got %d", len(store.states))
	}
	if store.states[0].ID != 99 {
		t.Fatalf("expected install ID 99, got %d", store.states[0].ID)
	}
	if store.states[0].State != identity.InstallationDeleted {
		t.Fatalf("expected deleted state, got %v", store.states[0].State)
	}
}

func TestWebhookHandler_InvalidSignature(t *testing.T) {
	store := &recordingStore{}
	cfg := &identity.GitHubAppConfig{WebhookSecret: "mysecret"}
	client := NewClient(cfg, store)
	handler := NewWebhookHandler(cfg, client, store)

	payload := map[string]any{
		"action": "created",
		"installation": map[string]any{
			"id": int64(1),
			"account": map[string]any{
				"id":    int64(1),
				"login": "test",
				"type":  "User",
			},
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "installation")
	req.Header.Set("X-Hub-Signature-256", "sha256=wrongsignature")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", w.Code)
	}
	if len(store.created) != 0 {
		t.Fatalf("expected no installations created after invalid signature")
	}
}

func TestWebhookHandler_MalformedPayload(t *testing.T) {
	store := &recordingStore{}
	cfg := &identity.GitHubAppConfig{}
	client := NewClient(cfg, store)
	handler := NewWebhookHandler(cfg, client, store)

	body := []byte("{invalid json")

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "installation")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (ack), got %d", w.Code)
	}
}

func TestWebhookHandler_UnknownEventType(t *testing.T) {
	store := &recordingStore{}
	cfg := &identity.GitHubAppConfig{}
	client := NewClient(cfg, store)
	handler := NewWebhookHandler(cfg, client, store)

	payload := map[string]any{
		"action": "opened",
		"issue": map[string]any{
			"id":    int64(1),
			"title": "test",
		},
	}
	body, _ := json.Marshal(payload)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (ack), got %d", w.Code)
	}
}

func TestWebhookHandler_NewWebhookHandler(t *testing.T) {
	cfg := &identity.GitHubAppConfig{WebhookSecret: "test"}
	client := NewClient(cfg, &mockInstallationStore{})
	handler := NewWebhookHandler(cfg, client, &mockInstallationStore{})
	if handler == nil {
		t.Fatal("expected non-nil handler")
	}
}
