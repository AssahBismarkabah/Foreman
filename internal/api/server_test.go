package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewServer(t *testing.T) {
	s := New(DefaultConfig())
	if s == nil {
		t.Fatal("expected non-nil Server")
	}
	if s.srv == nil {
		t.Fatal("expected non-nil http.Server")
	}
	if s.mux == nil {
		t.Fatal("expected non-nil ServeMux")
	}
	if s.cfg.ListenAddr != ":8080" {
		t.Errorf("expected :8080, got %q", s.cfg.ListenAddr)
	}
}

func TestRegisterRoute(t *testing.T) {
	s := New(Config{ListenAddr: ":0"})
	called := false
	s.RegisterRoute("GET", "/test", func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if !called {
		t.Error("handler was not called")
	}
}

func TestRegisterRoute_WrongMethod(t *testing.T) {
	s := New(Config{ListenAddr: ":0"})
	s.RegisterRoute("POST", "/test", func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler should not be called for wrong method")
	})

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rec.Code)
	}
}

func TestHealthEndpoint(t *testing.T) {
	s := New(Config{ListenAddr: ":0"})
	s.RegisterRoute("GET", "/healthz", healthHandler)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}
}

func TestServer_StartShutdown(t *testing.T) {
	s := New(Config{ListenAddr: ":0"})
	s.RegisterRoute("GET", "/healthz", healthHandler)

	ctx, cancel := context.WithCancel(context.Background())

	// Start in goroutine
	errCh := make(chan error, 1)
	go func() {
		errCh <- s.Start(ctx)
	}()

	// Give it a moment to start
	time.Sleep(50 * time.Millisecond)

	// Trigger shutdown
	cancel()

	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Errorf("unexpected error from Start: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after cancel")
	}
}

func TestServer_Shutdown(t *testing.T) {
	s := New(Config{ListenAddr: ":0"})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		_ = s.Start(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer shutdownCancel()
	if err := s.Shutdown(shutdownCtx); err != nil {
		t.Errorf("unexpected error from Shutdown: %v", err)
	}
	cancel()
}

func TestUnknownRoute(t *testing.T) {
	s := New(Config{ListenAddr: ":0"})

	req := httptest.NewRequest(http.MethodGet, "/nonexistent", nil)
	rec := httptest.NewRecorder()
	s.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rec.Code)
	}
}

func TestWithLogging(t *testing.T) {
	handler := withLogging(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/logtest", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rec.Code)
	}
}
