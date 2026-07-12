package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	jwt "github.com/golang-jwt/jwt/v5"
)

func setupIssuer(t *testing.T) (*Issuer, string) {
	t.Helper()
	key, err := GenerateKeyPair()
	if err != nil {
		t.Fatalf("GenerateKeyPair: %v", err)
	}
	pemData := pemEncodePrivateKey(key)
	_ = os.Setenv("FOREMAN_TEST_ISSUER_KEY", string(pemData))
	t.Cleanup(func() { _ = os.Unsetenv("FOREMAN_TEST_ISSUER_KEY") })

	mgr := NewEnvKeyManager("test-key-1", "FOREMAN_TEST_ISSUER_KEY")
	iss := NewIssuer(mgr, "foreman")
	return iss, string(pemData)
}

func TestIssuer_IssueAndValidateAgentToken(t *testing.T) {
	iss, _ := setupIssuer(t)
	ctx := context.Background()

	agent := &Agent{ID: "agent-1", Name: "opencode", SessionID: "ses-1", AssignedUserID: "user-1"}
	token, err := iss.IssueAgentToken(ctx, agent, "sbox-1", 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	subj, err := iss.ValidateToken(ctx, token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if subj.Type != IdentityAgent {
		t.Fatalf("expected agent identity, got %s", subj.Type)
	}
	if subj.ID != "agent-1" {
		t.Fatalf("expected agent ID 'agent-1', got %q", subj.ID)
	}
	if subj.Metadata["session_id"] != "ses-1" {
		t.Fatalf("expected session_id 'ses-1', got %q", subj.Metadata["session_id"])
	}
	if subj.Metadata["sandbox_id"] != "sbox-1" {
		t.Fatalf("expected sandbox_id 'sbox-1', got %q", subj.Metadata["sandbox_id"])
	}
}

func TestIssuer_IssueAndValidateServiceAccountToken(t *testing.T) {
	iss, _ := setupIssuer(t)
	ctx := context.Background()

	sa := &ServiceAccount{ID: "sa-1", Name: "deploy-bot"}
	token, err := iss.IssueServiceAccountToken(ctx, sa, 10*time.Minute)
	if err != nil {
		t.Fatalf("IssueServiceAccountToken: %v", err)
	}
	if token == "" {
		t.Fatal("expected non-empty token")
	}

	subj, err := iss.ValidateToken(ctx, token)
	if err != nil {
		t.Fatalf("ValidateToken: %v", err)
	}
	if subj.Type != IdentityServiceAccount {
		t.Fatalf("expected service account identity, got %s", subj.Type)
	}
	if subj.ID != "sa-1" {
		t.Fatalf("expected service account ID 'sa-1', got %q", subj.ID)
	}
}

func TestIssuer_ExpiredToken(t *testing.T) {
	iss, _ := setupIssuer(t)
	ctx := context.Background()

	agent := &Agent{ID: "agent-1", Name: "test", SessionID: "ses-1"}
	token, err := iss.IssueAgentToken(ctx, agent, "sbox-1", -1*time.Minute)
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}

	_, err = iss.ValidateToken(ctx, token)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
}

func TestIssuer_TamperedToken(t *testing.T) {
	iss, _ := setupIssuer(t)
	ctx := context.Background()

	agent := &Agent{ID: "agent-1", Name: "test", SessionID: "ses-1"}
	token, err := iss.IssueAgentToken(ctx, agent, "sbox-1", 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}

	// Tamper the payload
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatal("expected 3-part JWT")
	}
	tampered := parts[0] + "." + parts[1] + ".tampered"

	_, err = iss.ValidateToken(ctx, tampered)
	if err == nil {
		t.Fatal("expected error for tampered token")
	}
}

func TestIssuer_WrongSigningMethod(t *testing.T) {
	iss, _ := setupIssuer(t)
	ctx := context.Background()

	// Create a token signed with HMAC instead of RSA
	claims := &AgentClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "foreman",
			Subject:   "agent-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
		},
		AgentID: "agent-1",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = "test-key-1"
	signed, err := token.SignedString([]byte("fake-hmac-secret"))
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = iss.ValidateToken(ctx, signed)
	if err == nil {
		t.Fatal("expected error for wrong signing method")
	}
}

func TestIssuer_InvalidIssuer(t *testing.T) {
	iss, _ := setupIssuer(t)
	ctx := context.Background()

	// Create a token with wrong issuer using RSA
	key, _ := GenerateKeyPair()
	claims := &AgentClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "evil-foreman",
			Subject:   "agent-1",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(5 * time.Minute)),
		},
		AgentID: "agent-1",
	}
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = "test-key-1"
	signed, err := token.SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}

	_, err = iss.ValidateToken(ctx, signed)
	if err == nil {
		t.Fatal("expected error for wrong issuer")
	}
}

func TestIssuer_JWKSHandler(t *testing.T) {
	iss, _ := setupIssuer(t)
	handler := iss.JWKSHandler()

	req := httptest.NewRequest("GET", "/.well-known/jwks.json", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	resp := w.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected application/json, got %q", ct)
	}

	var keySet struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&keySet); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(keySet.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(keySet.Keys))
	}
}

func TestIssuer_JWKSKeyCanVerifyToken(t *testing.T) {
	iss, pemData := setupIssuer(t)
	ctx := context.Background()

	// Issue a token
	agent := &Agent{ID: "agent-1", Name: "test", SessionID: "ses-1"}
	token, err := iss.IssueAgentToken(ctx, agent, "sbox-1", 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}

	// Now create a NEW issuer from the same key material and verify the token
	_ = os.Setenv("FOREMAN_TEST_VERIFY_KEY", pemData)
	t.Cleanup(func() { _ = os.Unsetenv("FOREMAN_TEST_VERIFY_KEY") })
	mgr2 := NewEnvKeyManager("test-key-1", "FOREMAN_TEST_VERIFY_KEY")
	iss2 := NewIssuer(mgr2, "foreman")

	subj, err := iss2.ValidateToken(ctx, token)
	if err != nil {
		t.Fatalf("issuer2 ValidateToken: %v", err)
	}
	if subj.ID != "agent-1" {
		t.Fatalf("expected agent-1, got %q", subj.ID)
	}
}

func TestIssuer_OIDCConfiguration(t *testing.T) {
	iss, _ := setupIssuer(t)
	handler := iss.OIDCConfigurationHandler("http://localhost:8080")

	req := httptest.NewRequest("GET", "/.well-known/openid-configuration", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var cfg map[string]interface{}
	if err := json.NewDecoder(w.Body).Decode(&cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if cfg["issuer"] != "http://localhost:8080" {
		t.Fatalf("expected issuer http://localhost:8080, got %v", cfg["issuer"])
	}
	if cfg["jwks_uri"] != "http://localhost:8080/.well-known/jwks.json" {
		t.Fatalf("unexpected jwks_uri: %v", cfg["jwks_uri"])
	}
}
