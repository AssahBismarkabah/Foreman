package githubapp

import (
	"bytes"
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/foreman/foreman/internal/identity"
)

// DefaultGitHubAPI is the base URL for the GitHub REST API.
const DefaultGitHubAPI = "https://api.github.com"

// tokenCacheEntry holds a cached installation access token with expiry.
type tokenCacheEntry struct {
	token     string
	expiresAt time.Time
}

// Client handles GitHub App authentication and API operations.
type Client struct {
	cfg        *identity.GitHubAppConfig
	store      InstallationStore
	httpClient *http.Client
	apiBase    string

	mu         sync.RWMutex
	privateKey *rsa.PrivateKey
	tokenCache map[int64]*tokenCacheEntry
}

// NewClient creates a new GitHub App client.
func NewClient(cfg *identity.GitHubAppConfig, store InstallationStore) *Client {
	return &Client{
		cfg:        cfg,
		store:      store,
		httpClient: &http.Client{Timeout: 30 * time.Second},
		apiBase:    DefaultGitHubAPI,
		tokenCache: make(map[int64]*tokenCacheEntry),
	}
}

// loadPrivateKey reads and caches the app private key from disk.
func (c *Client) loadPrivateKey() (*rsa.PrivateKey, error) {
	c.mu.RLock()
	if c.privateKey != nil {
		defer c.mu.RUnlock()
		return c.privateKey, nil
	}
	c.mu.RUnlock()

	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check after acquiring write lock
	if c.privateKey != nil {
		return c.privateKey, nil
	}

	pemData, err := os.ReadFile(c.cfg.PrivateKeyPath)
	if err != nil {
		return nil, fmt.Errorf("read private key: %w", err)
	}

	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("no PEM data found")
	}

	key, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		// Try PKCS8
		parsed, pkcs8Err := x509.ParsePKCS8PrivateKey(block.Bytes)
		if pkcs8Err != nil {
			return nil, fmt.Errorf("parse private key: PKCS1: %w; PKCS8: %w", err, pkcs8Err)
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, errors.New("private key is not RSA")
		}
		c.privateKey = rsaKey
		return rsaKey, nil
	}

	c.privateKey = key
	return key, nil
}

// generateAppJWT creates a JWT signed with the app private key.
// GitHub App JWTs expire in 10 minutes max.
func (c *Client) generateAppJWT(ctx context.Context) (string, error) {
	key, err := c.loadPrivateKey()
	if err != nil {
		return "", fmt.Errorf("load key: %w", err)
	}

	now := time.Now()
	claims := &jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(10 * time.Minute)),
		Issuer:    fmt.Sprintf("%d", c.cfg.AppID),
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = c.cfg.AppSlug

	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("sign JWT: %w", err)
	}
	return signed, nil
}

// GetInstallationToken retrieves an installation access token.
// Returns cached token if still valid (with 5-minute buffer).
func (c *Client) GetInstallationToken(ctx context.Context, installID int64) (string, time.Time, error) {
	// Check cache
	c.mu.RLock()
	if entry, ok := c.tokenCache[installID]; ok && time.Now().Add(5*time.Minute).Before(entry.expiresAt) {
		defer c.mu.RUnlock()
		return entry.token, entry.expiresAt, nil
	}
	c.mu.RUnlock()

	// Generate app JWT
	appJWT, err := c.generateAppJWT(ctx)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("app JWT: %w", err)
	}

	// POST to GitHub API
	url := fmt.Sprintf("%s/app/installations/%d/access_tokens", c.apiBase, installID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(nil))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("http POST: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated {
		return "", time.Time{}, fmt.Errorf("GitHub API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", time.Time{}, fmt.Errorf("parse response: %w", err)
	}

	// Update cache
	c.mu.Lock()
	c.tokenCache[installID] = &tokenCacheEntry{
		token:     result.Token,
		expiresAt: result.ExpiresAt,
	}
	c.mu.Unlock()

	return result.Token, result.ExpiresAt, nil
}

// ListInstallations retrieves all installations of this GitHub App.
// Uses app JWT authentication (not installation token).
func (c *Client) ListInstallations(ctx context.Context) ([]identity.Installation, error) {
	appJWT, err := c.generateAppJWT(ctx)
	if err != nil {
		return nil, fmt.Errorf("app JWT: %w", err)
	}

	url := fmt.Sprintf("%s/app/installations", c.apiBase)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+appJWT)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http GET: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub API error (HTTP %d): %s", resp.StatusCode, string(body))
	}

	var ghInstalls []struct {
		ID      int64 `json:"id"`
		Account struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"account"`
		SuspendedAt *time.Time `json:"suspended_at"`
	}
	if err := json.Unmarshal(body, &ghInstalls); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}

	installs := make([]identity.Installation, 0, len(ghInstalls))
	for _, gi := range ghInstalls {
		state := identity.InstallationActive
		if gi.SuspendedAt != nil {
			state = identity.InstallationSuspended
		}
		installs = append(installs, identity.Installation{
			Platform:          "github",
			PlatformInstallID: gi.ID,
			AccountLogin:      gi.Account.Login,
			AccountType:       gi.Account.Type,
			AccountID:         gi.Account.ID,
			State:             state,
		})
	}

	return installs, nil
}
