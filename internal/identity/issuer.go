package identity

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-jose/go-jose/v4"
	jwt "github.com/golang-jwt/jwt/v5"
)

// AgentClaims represents the claims in an agent JWT token.
type AgentClaims struct {
	jwt.RegisteredClaims
	AgentID   string      `json:"aid"`
	SandboxID string      `json:"sid"`
	SessionID string      `json:"ses"`
	Identity  string      `json:"identity,omitempty"`
	Scope     *AgentScope `json:"scope,omitempty"`
}

// ServiceAccountClaims represents the claims in a service account JWT token.
type ServiceAccountClaims struct {
	jwt.RegisteredClaims
	ServiceAccountID string   `json:"said"`
	Roles            []string `json:"roles,omitempty"`
}

// Issuer handles JWT token creation, validation, and JWKS serving.
type Issuer struct {
	km       SigningKeyManager
	issuerID string
}

// NewIssuer creates a new Issuer with the given signing key manager and issuer identifier.
func NewIssuer(km SigningKeyManager, issuerID string) *Issuer {
	return &Issuer{km: km, issuerID: issuerID}
}

// IssueAgentToken creates a signed JWT for an agent running in a sandbox.
func (iss *Issuer) IssueAgentToken(ctx context.Context, agent *Agent, sandboxID string, ttl time.Duration) (string, error) {
	now := time.Now()
	kid, err := iss.km.KeyID(ctx)
	if err != nil {
		return "", fmt.Errorf("issuer: get key ID: %w", err)
	}
	key, err := iss.km.SigningKey(ctx)
	if err != nil {
		return "", fmt.Errorf("issuer: get signing key: %w", err)
	}

	claims := &AgentClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    iss.issuerID,
			Subject:   agent.ID,
			Audience:  jwt.ClaimStrings{"foreman"},
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        agent.SessionID,
		},
		AgentID:   agent.ID,
		SandboxID: sandboxID,
		SessionID: agent.SessionID,
		Identity:  agent.AssignedUserID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid

	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("issuer: sign token: %w", err)
	}
	return signed, nil
}

// IssueScopedAgentToken creates a signed JWT for an agent with a structured
// scope that limits its permissions (repos, actions, branches, etc.).
// See architecture.md section 5.2.
func (iss *Issuer) IssueScopedAgentToken(ctx context.Context, sessionID, userID string, ttl time.Duration, scope *AgentScope) (string, error) {
	now := time.Now()
	kid, err := iss.km.KeyID(ctx)
	if err != nil {
		return "", fmt.Errorf("issuer: get key ID: %w", err)
	}
	key, err := iss.km.SigningKey(ctx)
	if err != nil {
		return "", fmt.Errorf("issuer: get signing key: %w", err)
	}

	claims := &AgentClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    iss.issuerID,
			Subject:   fmt.Sprintf("agent-%s", sessionID),
			Audience:  jwt.ClaimStrings{"github"},
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        sessionID,
		},
		AgentID:   fmt.Sprintf("agent-%s", sessionID),
		SessionID: sessionID,
		Identity:  userID,
		Scope:     scope,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid

	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("issuer: sign scoped token: %w", err)
	}
	return signed, nil
}

// IssueServiceAccountToken creates a signed JWT for a service account.
func (iss *Issuer) IssueServiceAccountToken(ctx context.Context, sa *ServiceAccount, ttl time.Duration) (string, error) {
	now := time.Now()
	kid, err := iss.km.KeyID(ctx)
	if err != nil {
		return "", fmt.Errorf("issuer: get key ID: %w", err)
	}
	key, err := iss.km.SigningKey(ctx)
	if err != nil {
		return "", fmt.Errorf("issuer: get signing key: %w", err)
	}

	claims := &ServiceAccountClaims{
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    iss.issuerID,
			Subject:   sa.ID,
			Audience:  jwt.ClaimStrings{"foreman"},
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			IssuedAt:  jwt.NewNumericDate(now),
		},
		ServiceAccountID: sa.ID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = kid

	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("issuer: sign token: %w", err)
	}
	return signed, nil
}

// combinedClaims captures fields from both agent and service account tokens
// so we can parse once and determine the type after validation.
type combinedClaims struct {
	jwt.RegisteredClaims
	AgentID          string      `json:"aid"`
	SandboxID        string      `json:"sid"`
	SessionID        string      `json:"ses"`
	Identity         string      `json:"identity,omitempty"`
	Scope            *AgentScope `json:"scope,omitempty"`
	ServiceAccountID string      `json:"said,omitempty"`
	Roles            []string    `json:"roles,omitempty"`
}

// ValidateToken parses and validates a JWT token, returning the Subject if valid.
func (iss *Issuer) ValidateToken(ctx context.Context, tokenString string) (*Subject, error) {
	keyFunc := func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return iss.km.VerificationKey(ctx)
	}

	parsed, err := jwt.ParseWithClaims(tokenString, &combinedClaims{}, keyFunc,
		jwt.WithIssuer(iss.issuerID),
		jwt.WithAudience("foreman"),
		jwt.WithValidMethods([]string{"RS256"}),
	)
	if err != nil {
		return nil, fmt.Errorf("validate token: %w", err)
	}

	claims := parsed.Claims.(*combinedClaims)
	switch {
	case claims.AgentID != "":
		return &Subject{
			Type: IdentityAgent,
			ID:   claims.AgentID,
			Metadata: map[string]string{
				"session_id": claims.SessionID,
				"sandbox_id": claims.SandboxID,
			},
		}, nil
	case claims.ServiceAccountID != "":
		return &Subject{
			Type: IdentityServiceAccount,
			ID:   claims.ServiceAccountID,
		}, nil
	default:
		return nil, ErrInvalidToken
	}
}

// JWKSHandler returns an HTTP handler that serves the current public key in JWK format.
func (iss *Issuer) JWKSHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		pub, err := iss.km.VerificationKey(ctx)
		if err != nil {
			http.Error(w, `{"error":"key unavailable"}`, http.StatusInternalServerError)
			return
		}
		kid, err := iss.km.KeyID(ctx)
		if err != nil {
			http.Error(w, `{"error":"key ID unavailable"}`, http.StatusInternalServerError)
			return
		}

		rsaPub, ok := pub.(*rsa.PublicKey)
		if !ok {
			http.Error(w, `{"error":"invalid key type"}`, http.StatusInternalServerError)
			return
		}

		jwk := jose.JSONWebKey{
			Key:       rsaPub,
			KeyID:     kid,
			Algorithm: "RS256",
			Use:       "sig",
		}

		keySet := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{jwk}}

		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(keySet); err != nil {
			http.Error(w, `{"error":"encode failed"}`, http.StatusInternalServerError)
		}
	}
}

// OIDCConfigurationHandler returns an HTTP handler that serves minimal OIDC discovery info.
func (iss *Issuer) OIDCConfigurationHandler(baseURL string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := map[string]interface{}{
			"issuer":                                baseURL,
			"jwks_uri":                              baseURL + "/.well-known/jwks.json",
			"token_endpoint_auth_methods_supported": []string{"none"},
			"response_types_supported":              []string{"id_token"},
			"subject_types_supported":               []string{"public"},
			"id_token_signing_alg_values_supported": []string{"RS256"},
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(cfg); err != nil {
			http.Error(w, `{"error":"encode failed"}`, http.StatusInternalServerError)
		}
	}
}

// ErrExpiredToken is returned when a token has expired.
var ErrExpiredToken = errors.New("token expired")

// ErrInvalidToken is returned when a token is malformed or invalid.
var ErrInvalidToken = errors.New("invalid token")
