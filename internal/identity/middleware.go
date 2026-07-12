package identity

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

type contextKey string

const subjectKey contextKey = "foreman_subject"

// SubjectFromContext extracts the authenticated Subject from the request context.
// Returns nil if no authenticated subject is present.
func SubjectFromContext(ctx context.Context) *Subject {
	s, ok := ctx.Value(subjectKey).(*Subject)
	if !ok {
		return nil
	}
	return s
}

func setSubject(ctx context.Context, sub *Subject) context.Context {
	return context.WithValue(ctx, subjectKey, sub)
}

// AuthMiddleware returns middleware that validates a Bearer token from the
// Authorization header using the Issuer. On success, the authenticated Subject
// is placed in the request context (retrievable via SubjectFromContext). On
// failure, a 401 or 403 response is written and the request is not passed on.
func AuthMiddleware(issuer *Issuer) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			token, err := extractBearerToken(r)
			if err != nil {
				writeAuthError(w, http.StatusUnauthorized, err.Error())
				return
			}

			sub, err := issuer.ValidateToken(r.Context(), token)
			if err != nil {
				status := http.StatusUnauthorized
				if strings.Contains(err.Error(), "expired") {
					status = http.StatusForbidden
				}
				writeAuthError(w, status, err.Error())
				return
			}

			next.ServeHTTP(w, r.WithContext(setSubject(r.Context(), sub)))
		})
	}
}

// RequireAuth returns middleware that rejects requests without a valid
// authenticated Subject in the context (use after AuthMiddleware).
func RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if SubjectFromContext(r.Context()) == nil {
			writeAuthError(w, http.StatusUnauthorized, "authentication required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

// OptionalAuth returns middleware that continues even if no authenticated
// Subject is present. Use when authentication is optional.
func OptionalAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
	})
}

// extractBearerToken parses the Authorization header and returns the Bearer
// token value. Returns an error if the header is missing or malformed.
func extractBearerToken(r *http.Request) (string, error) {
	auth := r.Header.Get("Authorization")
	if auth == "" {
		return "", errorMissingAuth
	}
	parts := strings.SplitN(auth, " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", errorMalformedAuth
	}
	return parts[1], nil
}

func writeAuthError(w http.ResponseWriter, status int, detail string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error":  http.StatusText(status),
		"detail": detail,
	})
}

var (
	errorMissingAuth   = &authError{"missing Authorization header"}
	errorMalformedAuth = &authError{"malformed Authorization header, expected 'Bearer <token>'"}
)

type authError struct{ msg string }

func (e *authError) Error() string { return e.msg }
