package api

import (
	"context"
	"log"
	"net/http"
	"time"
)

// Config holds HTTP server configuration.
type Config struct {
	// ListenAddr is the address to listen on (e.g. ":8080").
	ListenAddr string `yaml:"listen_addr"`
}

// DefaultConfig returns a Config with sensible defaults.
func DefaultConfig() Config {
	return Config{
		ListenAddr: ":8080",
	}
}

// Server is a thin HTTP server wrapper for Foreman's API and webhook endpoints.
// It uses Go 1.22+ http.ServeMux with method-based routing.
type Server struct {
	srv *http.Server
	mux *http.ServeMux
	cfg Config
}

// New creates a new Server with the given config. Call Start to begin serving.
func New(cfg Config) *Server {
	mux := http.NewServeMux()
	return &Server{
		mux: mux,
		srv: &http.Server{
			Addr:         cfg.ListenAddr,
			Handler:      withLogging(mux),
			ReadTimeout:  10 * time.Second,
			WriteTimeout: 30 * time.Second,
			IdleTimeout:  60 * time.Second,
		},
		cfg: cfg,
	}
}

// RegisterRoute registers an HTTP handler for the given method and path pattern.
// Uses Go 1.22+ method routing format: "METHOD /path".
func (s *Server) RegisterRoute(method, path string, handler http.HandlerFunc) {
	s.mux.HandleFunc(method+" "+path, handler)
}

// Start begins serving HTTP in a background goroutine and returns immediately.
// Graceful shutdown is handled by the caller via Shutdown.
func (s *Server) Start(ctx context.Context) error {
	go func() {
		log.Printf("api: listening on %s", s.cfg.ListenAddr)
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("api: server error: %v", err)
		}
	}()
	return nil
}

// Shutdown gracefully stops the HTTP server with the given timeout.
func (s *Server) Shutdown(ctx context.Context) error {
	return s.srv.Shutdown(ctx)
}

// Mux returns the underlying ServeMux for route registration.
func (s *Server) Mux() *http.ServeMux {
	return s.mux
}

// withLogging wraps a handler with basic request logging.
func withLogging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("api: %s %s %s", r.Method, r.URL.Path, time.Since(start))
	})
}

// healthHandler responds with 200 OK for health checks.
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}
