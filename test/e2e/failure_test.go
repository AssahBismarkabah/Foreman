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

	// Use proper JSON encoding to handle descriptions containing special characters
	reqBody := struct {
		TaskID      string `json:"task_id"`
		Description string `json:"description"`
	}{
		TaskID:      taskID,
		Description: description,
	}
	payload, err := json.Marshal(reqBody)
	if err != nil {
		s.t.Fatalf("marshal submit body: %v", err)
	}

	resp, err := http.Post("http://localhost:8080/api/v1/tasks", "application/json",
		strings.NewReader(string(payload)))
	if err != nil {
		s.t.Fatalf("POST /api/v1/tasks: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusAccepted {
		respBody, _ := io.ReadAll(resp.Body)
		s.t.Fatalf("expected 202, got %d: %s", resp.StatusCode, respBody)
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

// restartForeman stops and restarts the Foreman container, simulating a crash + restart cycle.
func (s *composeStack) restartForeman() {
	s.t.Log("Restarting Foreman (simulating crash + restart)...")
	cmd := exec.Command("docker", append(s.composeArgs, "restart", "foreman")...)
	cmd.Dir = s.composeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		s.t.Fatalf("docker compose restart foreman: %v\n%s", err, out)
	}
}

// teardown stops and removes the compose stack.
func (s *composeStack) teardown() {
	if s.cleanedUp {
		return
	}
	s.cleanedUp = true

	// Dump logs before tearing down
	s.dumpLogs()

	// Remove any leftover sandbox containers from this test run.
	// These are created by Foreman at runtime, are not part of the compose stack,
	// and accumulate across test runs interfering with subsequent tests.
	cmd := exec.Command("sh", "-c", "docker rm -f $(docker ps -aq --filter name=foreman-sbox-) 2>/dev/null || true")
	_ = cmd.Run()

	// Remove volumes (-v) so each test starts with a clean Postgres database.
	// Without this, stale session data from previous test runs causes
	// duplicate key errors for hardcoded session IDs.
	cmd2 := exec.Command("docker", append(s.composeArgs, "down", "-t", "5", "-v")...)
	cmd2.Dir = s.composeDir
	out, _ := cmd2.CombinedOutput()
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
func TestE2E_SessionRecoveryOnRestart(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	// Use a longer heartbeat_timeout so the restart downtime doesn't cause the
	// recovered session to be marked as stale and failed during bootstrap.
	recoveryCfg := `subsystems:
  coordinator:
    max_concurrent: 5
    heartbeat_interval: 5s
    heartbeat_timeout: 120s
  sandbox:
    kind: docker
    image: ubuntu:22.04
  eventbus:
    kind: memory
  statestore:
    kind: postgres
    dsn: postgres://foreman:foreman@localhost:5432/foreman?sslmode=disable
    max_connections: 25
    min_connections: 5
  agents:
    - name: exec
      kind: exec
      cmd: sh
      cwd: /workspace
      heartbeat_timeout: 60s
  mcphub:
    servers:
      - name: filesystem
        transport: stdio
        command: npx
        args:
          - "@modelcontextprotocol/server-filesystem"
          - /tmp
  identity:
    api:
      listen_addr: ":8080"
    signing_key:
      source: env
      env_var_name: FOREMAN_SIGNING_KEY
      key_id: foreman-1
`

	// 1. Start compose with custom config
	stack := composeStackWithConfig(t, recoveryCfg)
	defer stack.teardown()

	// 2. Submit a task that runs long enough for a restart in the middle.
	// Use a unique task_id so old DB state from previous runs doesn't conflict.
	taskID := fmt.Sprintf("recovery-%d", time.Now().UnixNano())
	sid := stack.submitTask(taskID, "sleep 20")

	// 3. Wait for RUNNING (the sleep has started)
	stack.waitForStatus(sid, 30*time.Second, "RUNNING", "FAILED")

	// 4. Restart Foreman to simulate a crash. The sandbox keeps running but
	//    Foreman loses its in-memory state. On restart Bootstrap recovers the
	//    non-terminal session from PostgreSQL, provisions a new sandbox, and
	//    re-runs the agent.
	stack.restartForeman()

	// 5. Wait for health (Foreman is back + recovery is in progress)
	stack.waitForHealth()

	// 6. Wait for the session to complete. The recovered session goes
	//    ALLOCATING -> RUNNING -> COMPLETED. The task runs to completion
	//    in the new sandbox.
	status := stack.waitForStatus(sid, 90*time.Second, "COMPLETED", "FAILED")

	// 7. Assert COMPLETED -- FAILED means recovery or agent execution broke
	if status != "COMPLETED" {
		t.Fatalf("expected COMPLETED after recovery, got %s", status)
	}
}

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

	// Find and kill the sandbox container.
	// Take the last (most recently created) container to avoid picking up
	// orphaned sandbox containers from previous test runs.
	out, err := stack.runDocker("ps", "--filter", "name=foreman-sbox-", "--format", "{{.ID}}")
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	containerIDs := strings.Fields(string(out))
	if len(containerIDs) == 0 {
		t.Fatal("no sandbox container found")
	}
	containerID := containerIDs[len(containerIDs)-1]
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

func TestE2E_ApprovalGateFlow(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	// Config with exec adapter and a policy that requires approval for "write" tool.
	// The policy timeout is short (60s) to keep the test fast.
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
    heartbeat_interval: 5s
    heartbeat_timeout: 15s
    policies:
      - name: require-approval-for-write
        match:
          tool: write
        action: require_approval
        timeout: 60s
  agents:
    - name: exec
      kind: exec
      cmd: /bin/sh
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

	// Submit a task whose stdout contains a tool_use JSON line.
	// The exec adapter's ParseEvent extracts this as a tool_use event,
	// the policy matches on tool "write", and the session enters APPROVAL.
	sid := stack.submitTask("approval_gate",
		`echo '{"type":"tool_use","name":"write","part":{"tool":"write","state":{}}}'`)

	// Wait for APPROVAL status (session blocked at approval gate)
	status := stack.waitForStatus(sid, 30*time.Second, "APPROVAL", "FAILED", "COMPLETED")
	if status == "FAILED" || status == "COMPLETED" {
		t.Fatalf("expected APPROVAL, got %s", status)
	}
	t.Logf("Session reached APPROVAL as expected")

	// Approve the session via API
	t.Log("Approving session via API...")
	approveURL := "http://localhost:8080/api/v1/sessions/" + sid + "/approve"
	resp, err := http.Post(approveURL, "application/json", nil)
	if err != nil {
		t.Fatalf("POST %s: %v", approveURL, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected 200 on approve, got %d: %s", resp.StatusCode, body)
	}
	t.Log("Session approved, waiting for COMPLETED...")

	// Wait for COMPLETED
	status = stack.waitForStatus(sid, 30*time.Second, "COMPLETED", "FAILED")
	if status != "COMPLETED" {
		t.Fatalf("expected COMPLETED after approval, got %s", status)
	}
	t.Log("SUCCESS: Approval gate flow validated.")
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
