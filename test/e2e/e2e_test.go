// Package e2e tests the full Foreman pipeline as a user would use it:
// start the service with Docker Compose, submit a task via the HTTP API,
// poll until completion, and verify the result.
//
// Prerequisites: Docker, Docker Compose, FOREMAN_SIGNING_KEY in environment.
package e2e

import (
	"bufio"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func dockerComposeAvailable() bool {
	return exec.Command("docker", "compose", "version").Run() == nil
}

// ensureSigningKey ensures FOREMAN_SIGNING_KEY is set.
// Tries .env file first, then generates a fresh RSA key.
func ensureSigningKey(t *testing.T) {
	t.Helper()
	if os.Getenv("FOREMAN_SIGNING_KEY") != "" {
		return
	}
	// Try .env file
	data, err := os.ReadFile("../../.env")
	if err == nil {
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if strings.HasPrefix(line, "FOREMAN_SIGNING_KEY=") {
				val := strings.TrimPrefix(line, "FOREMAN_SIGNING_KEY=")
				_ = os.Setenv("FOREMAN_SIGNING_KEY", val)
				return
			}
		}
	}
	// Generate a fresh RSA key for CI / headless runs
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	pemBlock := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	_ = os.Setenv("FOREMAN_SIGNING_KEY", string(pem.EncodeToMemory(pemBlock)))
}

func TestE2E_ForemanStartTaskComplete(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}
	ensureSigningKey(t)

	projectName := fmt.Sprintf("foreman-e2e-%d", time.Now().UnixNano())
	composeDir := "../../deploy"
	composeArgs := []string{"compose", "-p", projectName, "-f", "docker-compose.yml", "--profile", "service"}

	// --- Build and start Foreman ---
	t.Log("Building Foreman image...")
	cmd := exec.Command("docker", append(composeArgs, "build")...)
	cmd.Dir = composeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker compose build: %v\n%s", err, out)
	}

	t.Log("Starting Foreman + Postgres...")
	cmd = exec.Command("docker", append(composeArgs, "up", "-d")...)
	cmd.Dir = composeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("docker compose up: %v\n%s", err, out)
	}

	// Use a deferred cleanup that dumps logs on failure
	cleanedUp := false
	defer func() {
		if cleanedUp {
			return
		}
		// Dump logs before tearing down
		logCmd := exec.Command("docker", append(composeArgs, "logs", "foreman")...)
		logCmd.Dir = composeDir
		logs, _ := logCmd.CombinedOutput()
		t.Logf("Foreman logs before teardown:\n%s", logs)

		downCmd := exec.Command("docker", append(composeArgs, "down", "-t", "5")...)
		downCmd.Dir = composeDir
		out, _ := downCmd.CombinedOutput()
		t.Logf("docker compose down:\n%s", out)
		cleanedUp = true
	}()

	// --- Wait for Foreman health endpoint ---
	t.Log("Waiting for Foreman to be ready...")
	ready := false
	for i := 0; i < 30; i++ {
		resp, err := http.Get("http://localhost:8080/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(1 * time.Second)
	}
	if !ready {
		t.Fatal("Foreman did not become ready within 30s")
	}
	t.Log("Foreman is ready.")

	// --- Submit a task ---
	taskID := "e2e_" + time.Now().Format("150405")
	submitURL := "http://localhost:8080/api/v1/tasks"
	payload := fmt.Sprintf(`{"task_id":"%s","description":"echo hello from foreman e2e"}`, taskID)

	resp, err := http.Post(submitURL, "application/json", strings.NewReader(payload))
	if err != nil {
		t.Fatalf("POST /api/v1/tasks: %v", err)
	}
	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		t.Fatalf("expected 202, got %d: %s", resp.StatusCode, body)
	}

	var submitResult struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&submitResult); err != nil {
		_ = resp.Body.Close()
		t.Fatalf("decode submit response: %v", err)
	}
	_ = resp.Body.Close()
	t.Logf("Task submitted, session_id: %s", submitResult.SessionID)

	// --- Poll until COMPLETED or FAILED ---
	deadline := time.Now().Add(90 * time.Second)
	var finalStatus string
	for time.Now().Before(deadline) {
		getResp, err := http.Get("http://localhost:8080/api/v1/sessions/" + submitResult.SessionID)
		if err != nil {
			t.Fatalf("GET /api/v1/sessions/%s: %v", submitResult.SessionID, err)
		}

		var session struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.NewDecoder(getResp.Body).Decode(&session); err != nil {
			_ = getResp.Body.Close()
			t.Fatalf("decode session response: %v", err)
		}
		_ = getResp.Body.Close()

		t.Logf("Session status: %s", session.Status)
		if session.Status == "COMPLETED" || session.Status == "FAILED" {
			finalStatus = session.Status
			break
		}
		time.Sleep(1 * time.Second)
	}

	if finalStatus == "" {
		t.Fatalf("session did not reach terminal state within 90s")
	}
	if finalStatus != "COMPLETED" {
		t.Fatalf("expected COMPLETED, got %s", finalStatus)
	}

	t.Log("SUCCESS: Full E2E pipeline validated.")
	cleanedUp = true

	// Clean teardown
	downCmd := exec.Command("docker", append(composeArgs, "down", "-t", "5")...)
	downCmd.Dir = composeDir
	_ = downCmd.Run()
}
