package githubapp

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/foreman/foreman/internal/identity"
)

// WebhookHandler processes GitHub App webhook events.
type WebhookHandler struct {
	client *Client
	store  InstallationStore
	secret string
}

// NewWebhookHandler creates a new GitHub webhook handler.
func NewWebhookHandler(cfg *identity.GitHubAppConfig, client *Client, store InstallationStore) *WebhookHandler {
	return &WebhookHandler{
		client: client,
		store:  store,
		secret: cfg.WebhookSecret,
	}
}

// ServeHTTP handles incoming GitHub webhook requests.
// Always returns 200 to acknowledge receipt (GitHub retries on 5xx).
func (h *WebhookHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("githubapp: read webhook body: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	// Validate signature
	if !h.validateSignature(r.Header.Get("X-Hub-Signature-256"), body) {
		log.Printf("githubapp: invalid webhook signature")
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	eventType := r.Header.Get("X-GitHub-Event")

	var event struct {
		Action       string `json:"action"`
		Installation struct {
			ID      int64 `json:"id"`
			Account struct {
				ID    int64  `json:"id"`
				Login string `json:"login"`
				Type  string `json:"type"`
			} `json:"account"`
		} `json:"installation"`
	}

	if err := json.Unmarshal(body, &event); err != nil {
		log.Printf("githubapp: parse webhook payload: %v", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	ctx := r.Context()

	switch {
	case eventType == "installation" && event.Action == "created":
		h.handleInstallationCreated(ctx, &event)
	case eventType == "installation" && event.Action == "deleted":
		h.handleInstallationDeleted(ctx, &event)
	case eventType == "installation" && event.Action == "suspend":
		h.handleInstallationSuspended(ctx, &event)
	case eventType == "installation" && event.Action == "unsuspend":
		h.handleInstallationUnsuspended(ctx, &event)
	default:
		log.Printf("githubapp: unhandled webhook event=%s action=%s", eventType, event.Action)
	}

	w.WriteHeader(http.StatusOK)
}

func (h *WebhookHandler) validateSignature(sigHeader string, body []byte) bool {
	if h.secret == "" || sigHeader == "" {
		return true // no secret configured = skip validation (dev mode)
	}

	mac := hmac.New(sha256.New, []byte(h.secret))
	mac.Write(body)
	expected := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	return hmac.Equal([]byte(expected), []byte(sigHeader))
}

func (h *WebhookHandler) handleInstallationCreated(ctx context.Context, event *struct {
	Action       string `json:"action"`
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	} `json:"installation"`
}) {
	inst := &identity.Installation{
		Platform:          "github",
		PlatformInstallID: event.Installation.ID,
		AccountLogin:      event.Installation.Account.Login,
		AccountType:       event.Installation.Account.Type,
		AccountID:         event.Installation.Account.ID,
		State:             identity.InstallationActive,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	if err := h.store.Create(ctx, inst); err != nil {
		log.Printf("githubapp: store installation created: %v", err)
		return
	}

	log.Printf("githubapp: installation created: id=%d account=%s type=%s",
		inst.PlatformInstallID, inst.AccountLogin, inst.AccountType)
}

func (h *WebhookHandler) handleInstallationDeleted(ctx context.Context, event *struct {
	Action       string `json:"action"`
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	} `json:"installation"`
}) {
	if err := h.store.UpdateState(ctx, event.Installation.ID, identity.InstallationDeleted); err != nil {
		log.Printf("githubapp: store installation deleted: %v", err)
		return
	}

	log.Printf("githubapp: installation deleted: id=%d account=%s",
		event.Installation.ID, event.Installation.Account.Login)
}

func (h *WebhookHandler) handleInstallationSuspended(ctx context.Context, event *struct {
	Action       string `json:"action"`
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	} `json:"installation"`
}) {
	if err := h.store.UpdateState(ctx, event.Installation.ID, identity.InstallationSuspended); err != nil {
		log.Printf("githubapp: store installation suspended: %v", err)
		return
	}
	log.Printf("githubapp: installation suspended: id=%d", event.Installation.ID)
}

func (h *WebhookHandler) handleInstallationUnsuspended(ctx context.Context, event *struct {
	Action       string `json:"action"`
	Installation struct {
		ID      int64 `json:"id"`
		Account struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
	} `json:"installation"`
}) {
	if err := h.store.UpdateState(ctx, event.Installation.ID, identity.InstallationActive); err != nil {
		log.Printf("githubapp: store installation unsuspended: %v", err)
		return
	}
	log.Printf("githubapp: installation unsuspended: id=%d", event.Installation.ID)
}
