package githubapp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/foreman/foreman/internal/identity"
)

// mockInstallationStore satisfies InstallationStore for tests.
type mockInstallationStore struct{}

func (m *mockInstallationStore) Create(_ context.Context, _ *identity.Installation) error { return nil }
func (m *mockInstallationStore) GetByPlatformID(_ context.Context, _ int64) (*identity.Installation, error) {
	return nil, nil
}
func (m *mockInstallationStore) UpdateState(_ context.Context, _ int64, _ identity.InstallationState) error {
	return nil
}
func (m *mockInstallationStore) Delete(_ context.Context, _ int64) error { return nil }
func (m *mockInstallationStore) ListByAccount(_ context.Context, _ int64) ([]identity.Installation, error) {
	return nil, nil
}

func TestClient_GetInstallationToken_CacheHit(t *testing.T) {
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	cfg := &identity.GitHubAppConfig{
		AppID:   12345,
		AppSlug: "test-app",
	}
	client := NewClient(cfg, &mockInstallationStore{})
	client.privateKey = key

	// Seed the cache with a valid token
	client.tokenCache[42] = &tokenCacheEntry{
		token:     "cached_token_123",
		expiresAt: time.Now().Add(30 * time.Minute),
	}

	token, expiresAt, err := client.GetInstallationToken(context.Background(), 42)
	if err != nil {
		t.Fatalf("GetInstallationToken: %v", err)
	}
	if token != "cached_token_123" {
		t.Fatalf("expected cached token, got %q", token)
	}
	if expiresAt.Before(time.Now()) {
		t.Fatal("expected future expiry from cache")
	}
}

func TestClient_GetInstallationToken_CacheMiss(t *testing.T) {
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token": "ghs_fresh_token", "expires_at": "2026-07-13T00:00:00Z"}`)
	}))
	defer ts.Close()

	cfg := &identity.GitHubAppConfig{
		AppID:   12345,
		AppSlug: "test-app",
	}
	client := NewClient(cfg, &mockInstallationStore{})
	client.privateKey = key
	client.apiBase = ts.URL
	client.httpClient = ts.Client()

	token, _, err := client.GetInstallationToken(context.Background(), 99)
	if err != nil {
		t.Fatalf("GetInstallationToken: %v", err)
	}
	if token != "ghs_fresh_token" {
		t.Fatalf("expected fresh token, got %q", token)
	}

	// Verify cache was populated
	entry, ok := client.tokenCache[99]
	if !ok {
		t.Fatal("expected token cache entry after fetch")
	}
	if entry.token != "ghs_fresh_token" {
		t.Fatalf("expected cached token 'ghs_fresh_token', got %q", entry.token)
	}
}

func TestClient_ListInstallations(t *testing.T) {
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("expected GET, got %s", r.Method)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `[
			{"id": 1, "account": {"id": 100, "login": "org-a", "type": "Organization"}, "suspended_at": null},
			{"id": 2, "account": {"id": 200, "login": "user-b", "type": "User"}, "suspended_at": "2026-01-01T00:00:00Z"}
		]`)
	}))
	defer ts.Close()

	cfg := &identity.GitHubAppConfig{
		AppID:   12345,
		AppSlug: "test-app",
	}
	client := NewClient(cfg, &mockInstallationStore{})
	client.privateKey = key
	client.apiBase = ts.URL
	client.httpClient = ts.Client()

	installs, err := client.ListInstallations(context.Background())
	if err != nil {
		t.Fatalf("ListInstallations: %v", err)
	}
	if len(installs) != 2 {
		t.Fatalf("expected 2 installations, got %d", len(installs))
	}
	if installs[0].PlatformInstallID != 1 {
		t.Fatalf("expected install ID 1, got %d", installs[0].PlatformInstallID)
	}
	if installs[0].AccountLogin != "org-a" {
		t.Fatalf("expected account 'org-a', got %q", installs[0].AccountLogin)
	}
	if installs[0].State != identity.InstallationActive {
		t.Fatalf("expected active state, got %v", installs[0].State)
	}
	if installs[1].State != identity.InstallationSuspended {
		t.Fatalf("expected suspended state, got %v", installs[1].State)
	}
}

func TestClient_GetInstallationToken_HTTPError(t *testing.T) {
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"message": "Not found"}`)
	}))
	defer ts.Close()

	cfg := &identity.GitHubAppConfig{
		AppID:   12345,
		AppSlug: "test-app",
	}
	client := NewClient(cfg, &mockInstallationStore{})
	client.privateKey = key
	client.apiBase = ts.URL
	client.httpClient = ts.Client()

	_, _, err = client.GetInstallationToken(context.Background(), 999)
	if err == nil {
		t.Fatal("expected error for HTTP 404")
	}
}

func TestClient_ConcurrentCacheAccess(t *testing.T) {
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	cfg := &identity.GitHubAppConfig{
		AppID:   12345,
		AppSlug: "test-app",
	}
	client := NewClient(cfg, &mockInstallationStore{})
	client.privateKey = key

	// Seed the cache
	client.tokenCache[1] = &tokenCacheEntry{
		token:     "concurrent_test_token",
		expiresAt: time.Now().Add(30 * time.Minute),
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ctx := context.Background()
			token, _, err := client.GetInstallationToken(ctx, 1)
			if err != nil || token != "concurrent_test_token" {
				t.Errorf("concurrent access failed: err=%v token=%q", err, token)
			}
		}()
	}
	wg.Wait()
}

func TestClient_GetInstallationToken_ExpiredCache(t *testing.T) {
	key, err := identity.GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}

	callCount := 0
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		fmt.Fprint(w, `{"token": "ghs_refreshed", "expires_at": "2026-07-14T00:00:00Z"}`)
	}))
	defer ts.Close()

	cfg := &identity.GitHubAppConfig{
		AppID:   12345,
		AppSlug: "test-app",
	}
	client := NewClient(cfg, &mockInstallationStore{})
	client.privateKey = key
	client.apiBase = ts.URL
	client.httpClient = ts.Client()

	// Seed cache with expired token
	client.tokenCache[1] = &tokenCacheEntry{
		token:     "expired_token",
		expiresAt: time.Now().Add(-10 * time.Minute),
	}

	token, _, err := client.GetInstallationToken(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetInstallationToken: %v", err)
	}
	if token != "ghs_refreshed" {
		t.Fatalf("expected refreshed token, got %q", token)
	}
	if callCount != 1 {
		t.Fatalf("expected 1 API call, got %d", callCount)
	}
}

func TestClient_NewClient(t *testing.T) {
	cfg := &identity.GitHubAppConfig{AppID: 1}
	client := NewClient(cfg, &mockInstallationStore{})
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.apiBase != DefaultGitHubAPI {
		t.Fatalf("expected default API base, got %q", client.apiBase)
	}
}
