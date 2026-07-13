package e2e

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// composeStack manages a Docker Compose instance for a single E2E test.
// Each test creates its own stack so different configs can be used.
// The Docker image is built only once (shared via build cache).
type composeStack struct {
	composeDir  string
	composeArgs []string
	t           *testing.T
	cleanedUp   bool
}

func newComposeStack(t *testing.T) *composeStack {
	t.Helper()
	ensureSigningKey(t)

	composeDir := "../../deploy"
	args := []string{"compose", "-f", "docker-compose.yml", "--profile", "service"}

	s := &composeStack{
		composeDir:  composeDir,
		composeArgs: args,
		t:           t,
	}

	s.build()
	s.up()
	s.waitForHealth()
	return s
}

func (s *composeStack) build() {
	s.t.Log("Building Foreman image...")
	cmd := exec.Command("docker", append(s.composeArgs, "build")...)
	cmd.Dir = s.composeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		s.t.Fatalf("docker compose build: %v\n%s", err, out)
	}
}

func (s *composeStack) up() {
	s.t.Log("Starting Foreman + Postgres...")
	cmd := exec.Command("docker", append(s.composeArgs, "up", "-d")...)
	cmd.Dir = s.composeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		s.t.Fatalf("docker compose up: %v\n%s", err, out)
	}
}

func (s *composeStack) waitForHealth() {
	s.t.Log("Waiting for Foreman to be ready...")
	for i := 0; i < 30; i++ {
		resp, err := http.Get("http://localhost:8080/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				s.t.Log("Foreman is ready.")
				return
			}
		}
		time.Sleep(1 * time.Second)
	}
	s.dumpLogs()
	s.t.Fatal("Foreman did not become ready within 30s")
}

func (s *composeStack) dumpLogs() {
	cmd := exec.Command("docker", append(s.composeArgs, "logs", "foreman")...)
	cmd.Dir = s.composeDir
	logs, _ := cmd.CombinedOutput()
	s.t.Logf("Foreman logs:\n%s", logs)
}

func (s *composeStack) submitTask(taskID, description string) string {
	s.t.Helper()
	payload := fmt.Sprintf(`{"task_id":"%s","description":"%s"}`, taskID, description)
	resp, err := http.Post("http://localhost:8080/api/v1/tasks", "application/json",
		strings.NewReader(payload))
	if err != nil {
		s.t.Fatalf("POST /api/v1/tasks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		body, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("expected 202, got %d: %s", resp.StatusCode, body)
	}

	var result struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		s.t.Fatalf("decode submit response: %v", err)
	}
	s.t.Logf("Task submitted, session_id: %s", result.SessionID)
	return result.SessionID
}

// waitForStatus polls the session endpoint until the session reaches one of the given statuses.
// Returns the terminal status.
func (s *composeStack) waitForStatus(sessionID string, timeout time.Duration, statuses ...string) string {
	s.t.Helper()
	statusMap := make(map[string]bool, len(statuses))
	for _, st := range statuses {
		statusMap[st] = true
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get("http://localhost:8080/api/v1/sessions/" + sessionID)
		if err != nil {
			s.t.Fatalf("GET /api/v1/sessions/%s: %v", sessionID, err)
		}

		var session struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
			_ = resp.Body.Close()
			s.t.Fatalf("decode session response: %v", err)
		}
		_ = resp.Body.Close()

		s.t.Logf("Session %s status: %s", sessionID, session.Status)
		if statusMap[session.Status] {
			return session.Status
		}
		time.Sleep(1 * time.Second)
	}

	s.dumpLogs()
	s.t.Fatalf("session %s did not reach terminal state within %v", sessionID, timeout)
	return ""
}

// teardown stops and removes the compose stack.
func (s *composeStack) teardown() {
	if s.cleanedUp {
		return
	}
	s.cleanedUp = true

	// Dump logs before tearing down
	s.dumpLogs()

	cmd := exec.Command("docker", append(s.composeArgs, "down", "-t", "5")...)
	cmd.Dir = s.composeDir
	out, _ := cmd.CombinedOutput()
	s.t.Logf("docker compose down:\n%s", out)
}

// runDocker executes a docker command in the compose project context.
func (s *composeStack) runDocker(args ...string) ([]byte, error) {
	cmd := exec.Command("docker", args...)
	cmd.Dir = s.composeDir
	return cmd.CombinedOutput()
}

// composeStackWithConfig builds and starts a compose stack using a custom foreman.yaml.
// Writes the config and a compose override to a temp directory.
func composeStackWithConfig(t *testing.T, customCfg string) *composeStack {
	t.Helper()
	ensureSigningKey(t)

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "foreman.yaml")
	overridePath := filepath.Join(tmpDir, "compose-override.yml")

	if err := os.WriteFile(cfgPath, []byte(customCfg), 0644); err != nil {
		t.Fatalf("write custom config: %v", err)
	}

	override := fmt.Sprintf(`services:
  foreman:
    volumes:
      - %s:/etc/foreman/foreman.yaml:ro
`, cfgPath)
	if err := os.WriteFile(overridePath, []byte(override), 0644); err != nil {
		t.Fatalf("write compose override: %v", err)
	}

	composeDir := "../../deploy"
	args := []string{"compose",
		"-f", "docker-compose.yml",
		"-f", overridePath,
		"--profile", "service",
	}

	s := &composeStack{
		composeDir:  composeDir,
		composeArgs: args,
		t:           t,
	}

	s.build()
	s.up()
	s.waitForHealth()
	return s
}

// --- Tests ---

func TestE2E_AgentNonZeroExit(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	stack := newComposeStack(t)
	defer stack.teardown()

	sid := stack.submitTask("exit1", "exit 1")
	status := stack.waitForStatus(sid, 90*time.Second, "FAILED", "COMPLETED")

	if status != "FAILED" {
		t.Fatalf("expected FAILED for non-zero exit, got %s", status)
	}
	t.Log("SUCCESS: Non-zero exit correctly produced FAILED session.")
}

func TestE2E_SandboxCrashDetection(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	stack := newComposeStack(t)
	defer stack.teardown()

	// Submit a long-running task so we have time to kill the sandbox
	sid := stack.submitTask("crash", "sleep 120")

	// Wait for the session to reach RUNNING (sandbox is provisioned)
	status := stack.waitForStatus(sid, 30*time.Second, "RUNNING", "FAILED", "COMPLETED")
	if status == "FAILED" {
		t.Fatal("session failed before sandbox could be killed")
	}
	if status == "COMPLETED" {
		t.Fatal("session completed before sandbox could be killed")
	}

	// Find and kill the sandbox container
	out, err := stack.runDocker("ps", "--filter", "name=foreman-sbox-", "--format", "{{.ID}}")
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	containerID := strings.TrimSpace(string(out))
	if containerID == "" {
		t.Fatal("no sandbox container found")
	}
	t.Logf("Killing sandbox container: %s", containerID)
	if _, err := stack.runDocker("kill", containerID); err != nil {
		t.Fatalf("docker kill: %v", err)
	}

	// Foreman heartbeat should detect the crash and transition to FAILED
	status = stack.waitForStatus(sid, 60*time.Second, "FAILED", "COMPLETED")
	if status != "FAILED" {
		t.Fatalf("expected FAILED after sandbox crash, got %s", status)
	}
	t.Log("SUCCESS: Sandbox crash correctly detected and session marked FAILED.")
}

func TestE2E_AdapterVerifyFailure(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	// Config with opencode (bad binary) as the FIRST agent.
	// coordinator picks adapterList[0] -> calls Verify ->
	// exec.LookPath("nonexistent-binary") fails -> session FAILED.
	customCfg := `
subsystems:
  eventbus:
    kind: memory
  statestore:
    kind: postgres
    dsn: "postgres://foreman:foreman@postgres:5432/foreman?sslmode=disable"
    max_connections: 25
    min_connections: 5
  sandbox:
    kind: docker
    image: alpine:latest
  coordinator:
    max_concurrent: 5
    default_timeout: 5m
  agents:
    - name: opencode
      kind: opencode
      cmd: nonexistent-binary
      cwd: /tmp
      heartbeat_interval: 30s
      heartbeat_timeout: 90s
  identity:
    api:
      listen_addr: ":8080"
      public_url: "http://localhost:8080"
    signing_key:
      source: env
      env_var_name: FOREMAN_SIGNING_KEY
      key_id: foreman-1
`
	stack := composeStackWithConfig(t, customCfg)
	defer stack.teardown()

	sid := stack.submitTask("verify_fail", "echo should not run")
	status := stack.waitForStatus(sid, 90*time.Second, "FAILED", "COMPLETED")

	if status != "FAILED" {
		t.Fatalf("expected FAILED for adapter verify failure, got %s", status)
	}
	t.Log("SUCCESS: Adapter verify failure correctly produced FAILED session.")
}
