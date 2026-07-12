package identity

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestExtractBearerToken(t *testing.T) {
	tests := []struct {
		name    string
		header  string
		want    string
		wantErr bool
	}{
		{"valid bearer", "Bearer mytoken123", "mytoken123", false},
		{"valid bearer lowercase", "bearer mytoken123", "mytoken123", false},
		{"no header", "", "", true},
		{"wrong scheme", "Basic dXNlcjpwYXNz", "", true},
		{"empty value", "Bearer ", "", true},
		{"malformed - no space", "Bearermytoken", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.header != "" {
				req.Header.Set("Authorization", tt.header)
			}
			got, err := extractBearerToken(req)
			if (err != nil) != tt.wantErr {
				t.Errorf("extractBearerToken() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("extractBearerToken() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	iss, _ := setupIssuer(t)

	agent := &Agent{ID: "agent-1"}
	token, err := iss.IssueAgentToken(context.Background(), agent, "sbox-1", 5*time.Minute)
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}

	handler := AuthMiddleware(iss)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := SubjectFromContext(r.Context())
		if got == nil {
			t.Fatal("expected Subject in context")
		}
		if got.ID != "agent-1" {
			t.Errorf("expected ID %q, got %q", "agent-1", got.ID)
		}
		if got.Type != IdentityAgent {
			t.Errorf("expected Type %v, got %v", IdentityAgent, got.Type)
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestAuthMiddleware_MissingHeader(t *testing.T) {
	iss, _ := setupIssuer(t)

	handler := AuthMiddleware(iss)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	iss, _ := setupIssuer(t)

	handler := AuthMiddleware(iss)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer invalidtoken")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}
}

func TestAuthMiddleware_ExpiredToken(t *testing.T) {
	iss, _ := setupIssuer(t)

	agent := &Agent{ID: "agent-1"}
	token, err := iss.IssueAgentToken(context.Background(), agent, "sbox-1", -1*time.Minute)
	if err != nil {
		t.Fatalf("IssueAgentToken: %v", err)
	}

	handler := AuthMiddleware(iss)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called")
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusForbidden {
		t.Errorf("expected 403 for expired token, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestRequireAuth(t *testing.T) {
	t.Run("with subject", func(t *testing.T) {
		handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req = req.WithContext(setSubject(req.Context(), &Subject{Type: IdentityAgent, ID: "a-1"}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200, got %d", rec.Code)
		}
	})

	t.Run("without subject", func(t *testing.T) {
		handler := RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t.Fatal("handler should not be called")
		}))

		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401, got %d", rec.Code)
		}
	})
}

func TestOptionalAuth(t *testing.T) {
	handler := OptionalAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sub := SubjectFromContext(r.Context())
		if sub != nil {
			t.Error("expected nil Subject with OptionalAuth")
		}
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}

func TestSubjectFromContext(t *testing.T) {
	t.Run("nil when not set", func(t *testing.T) {
		ctx := context.Background()
		if sub := SubjectFromContext(ctx); sub != nil {
			t.Errorf("expected nil, got %v", sub)
		}
	})

	t.Run("returns subject when set", func(t *testing.T) {
		expected := &Subject{Type: IdentityAgent, ID: "a-1"}
		ctx := setSubject(context.Background(), expected)
		got := SubjectFromContext(ctx)
		if got == nil {
			t.Fatal("expected non-nil Subject")
		}
		if got.ID != expected.ID {
			t.Errorf("expected ID %q, got %q", expected.ID, got.ID)
		}
	})
}
