// Mock LLM server for E2E tests.
//
// Implements the OpenAI /v1/chat/completions endpoint (streaming) so that agent
// adapters (opencode, claude-code, etc.) have a real HTTP endpoint to talk to
// without needing actual API keys. All responses are canned -- no real LLM call.
//
// Usage in compose stacks:
//
//	services:
//	  mockllm:
//	    image: foreman:e2e-mockllm
//	    ports:
//	      - "9999"
//	  foreman:
//	    environment:
//	      - OPENAI_BASE_URL=http://mockllm:9999/v1
//	      - OPENAI_API_KEY=fake-key
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

// chatRequest models the OpenAI /v1/chat/completions request body.
type chatRequest struct {
	Model    string        `json:"model"`
	Messages []chatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// writeSSE writes a server-sent event data frame.
func writeSSE(w http.ResponseWriter, data string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

// writeDone sends the SSE termination signal.
func writeDone(w http.ResponseWriter) {
	_, _ = fmt.Fprintf(w, "data: [DONE]\n\n")
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req chatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, fmt.Sprintf("bad request: %v", err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	if !req.Stream {
		// Non-streaming response (rare for adapters but handle gracefully).
		resp := map[string]any{
			"choices": []map[string]any{
				{
					"index":         0,
					"finish_reason": "stop",
					"message": map[string]string{
						"role":    "assistant",
						"content": "Hello from mock LLM. Task completed successfully.",
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
		return
	}

	// Streaming response. Opencode consumes SSE chunks and assembles them
	// into the final assistant message.
	// 1) Role announcement
	writeSSE(w, `{"choices":[{"delta":{"role":"assistant"},"finish_reason":null,"index":0}]}`)

	// Small delay to simulate realistic streaming
	time.Sleep(50 * time.Millisecond)

	// 2) Content (the canned response)
	writeSSE(w, `{"choices":[{"delta":{"content":"Hello from mock LLM. Task completed successfully."},"finish_reason":null,"index":0}]}`)

	time.Sleep(50 * time.Millisecond)

	// 3) Stop signal
	writeSSE(w, `{"choices":[{"delta":{},"finish_reason":"stop","index":0}]}`)

	// 4) Done
	writeDone(w)
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handleChat)
	// Health endpoint for compose health checks
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := ":9999"
	log.Printf("mock LLM server listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
