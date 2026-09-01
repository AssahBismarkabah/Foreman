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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
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

	// Read and log the request body for debugging.
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("mockllm: failed to read request body: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	log.Printf("mockllm: received %s %s (body=%d bytes)", r.Method, r.URL.Path, len(bodyBytes))
	if len(bodyBytes) > 0 && len(bodyBytes) < 2000 {
		log.Printf("mockllm: request body: %s", string(bodyBytes))
	}

	var req chatRequest
	if err := json.NewDecoder(bytes.NewReader(bodyBytes)).Decode(&req); err != nil {
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

// loggingMiddleware logs all incoming requests and their duration.
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		log.Printf("mockllm: %s %s from %s", r.Method, r.URL.Path, r.RemoteAddr)
		next.ServeHTTP(w, r)
		log.Printf("mockllm: %s %s completed in %v", r.Method, r.URL.Path, time.Since(start))
	})
}

// handleResponses handles OpenAI's /v1/responses endpoint with SSE streaming.
func handleResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("mockllm: failed to read request body: %v", err)
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	r.Body = io.NopCloser(bytes.NewReader(bodyBytes))
	log.Printf("mockllm: received %s %s (body=%d bytes)", r.Method, r.URL.Path, len(bodyBytes))
	if len(bodyBytes) > 0 && len(bodyBytes) < 2000 {
		log.Printf("mockllm: request body: %s", string(bodyBytes))
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	// Streaming response for Responses API
	// Opencode validates these events strictly: it needs response.created,
	// a message output item with content-part metadata, output_text.delta/done,
	// output_item.done, and a final response.completed. Without the
	// item_id/output_index/content_index metadata, newer opencode versions
	// treat the turn as incomplete and re-request forever.

	// 1) response.created
	writeSSE(w, `{"type":"response.created","response":{"id":"resp_mock_1","object":"response","status":"in_progress","output":[]}}`)

	time.Sleep(50 * time.Millisecond)

	// 2) output item added (assistant message)
	writeSSE(w, `{"type":"response.output_item.added","output_index":0,"item":{"id":"msg_mock_1","type":"message","role":"assistant","status":"in_progress","content":[]}}`)

	time.Sleep(50 * time.Millisecond)

	// 3) content part added
	writeSSE(w, `{"type":"response.content_part.added","item_id":"msg_mock_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":""}}`)

	time.Sleep(50 * time.Millisecond)

	// 4) output text delta
	writeSSE(w, `{"type":"response.output_text.delta","item_id":"msg_mock_1","output_index":0,"content_index":0,"delta":"Hello from mock LLM. Task completed successfully."}`)

	time.Sleep(50 * time.Millisecond)

	// 5) output text done
	writeSSE(w, `{"type":"response.output_text.done","item_id":"msg_mock_1","output_index":0,"content_index":0,"text":"Hello from mock LLM. Task completed successfully."}`)

	time.Sleep(50 * time.Millisecond)

	// 6) content part done
	writeSSE(w, `{"type":"response.content_part.done","item_id":"msg_mock_1","output_index":0,"content_index":0,"part":{"type":"output_text","text":"Hello from mock LLM. Task completed successfully."}}`)

	time.Sleep(50 * time.Millisecond)

	// 7) output item done
	writeSSE(w, `{"type":"response.output_item.done","output_index":0,"item":{"id":"msg_mock_1","type":"message","role":"assistant","status":"completed","content":[{"type":"output_text","text":"Hello from mock LLM. Task completed successfully."}]}}`)

	time.Sleep(50 * time.Millisecond)

	// 8) response.completed
	writeSSE(w, `{"type":"response.completed","response":{"id":"resp_mock_1","object":"response","status":"completed","output":[{"id":"msg_mock_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"Hello from mock LLM. Task completed successfully."}]}]}}`)

	writeDone(w)
}

func main() {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/chat/completions", handleChat)
	mux.HandleFunc("/v1/responses", handleResponses)
	// Health endpoint for compose health checks
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	addr := ":9999"
	log.Printf("mock LLM server listening on %s", addr)
	log.Fatal(http.ListenAndServe(addr, loggingMiddleware(mux)))
}
