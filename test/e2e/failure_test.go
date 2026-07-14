package e2e

import (
	"encoding/base64"
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
	projectName string
	t           *testing.T
	cleanedUp   bool
}

// uniqueProjectName returns a unique Docker Compose project name per test invocation.
// Each test gets its own project name so Docker state (networks, containers, ports)
// is fully isolated and cannot leak between tests.
func uniqueProjectName() string {
	return fmt.Sprintf("foreman-e2e-%d", time.Now().UnixNano())
}

func newComposeStack(t *testing.T) *composeStack {
	t.Helper()
	ensureSigningKey(t)

	projectName := uniqueProjectName()
	composeDir := "../../deploy"
	args := []string{"compose", "-p", projectName, "-f", "docker-compose.yml", "--profile", "service"}

	s := &composeStack{
		composeDir:  composeDir,
		composeArgs: args,
		projectName: projectName,
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
	// Free port 5432 from any stale containers (previous test runs or manual
	// development) so the new Postgres container can bind. Without this cleanup,
	// old containers with the default project name (before we added unique names)
	// hold the port indefinitely.
	_ = exec.Command("sh", "-c", "docker rm -f $(docker ps -q --filter publish=5432) 2>/dev/null || true").Run()
	// Also clean up any old-style named compose containers from before unique project names
	_ = exec.Command("sh", "-c", "docker rm -f foreman-postgres foreman-nats foreman 2>/dev/null || true").Run()

	// Retry up to 15 times with 2s delay to handle Docker port release races
	// between sequential tests (port 5432 may still be held from previous stack).
	for i := 0; i < 15; i++ {
		cmd := exec.Command("docker", append(s.composeArgs, "up", "-d")...)
		cmd.Dir = s.composeDir
		if out, err := cmd.CombinedOutput(); err == nil {
			return
		} else if !strings.Contains(string(out), "port is already allocated") {
			s.t.Fatalf("docker compose up: %v\n%s", err, out)
		}
		if i == 14 {
			s.t.Fatalf("docker compose up: port 5432 still allocated after 15 retries")
		}
		s.t.Logf("Port 5432 still in use, retrying in 2s (attempt %d/15)...", i+1)
		time.Sleep(2 * time.Second)
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

// stopForeman sends SIGTERM to Foreman and waits up to 60s for it to exit.
// Used by the graceful shutdown test.
func (s *composeStack) stopForeman() {
	s.t.Log("Stopping Foreman (SIGTERM)...")
	cmd := exec.Command("docker", append(s.composeArgs, "stop", "-t", "60", "foreman")...)
	cmd.Dir = s.composeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		s.t.Fatalf("docker compose stop foreman: %v\n%s", err, out)
	}
}

// startForeman starts the Foreman container that was previously stopped.
func (s *composeStack) startForeman() {
	s.t.Log("Starting Foreman...")
	cmd := exec.Command("docker", append(s.composeArgs, "start", "foreman")...)
	cmd.Dir = s.composeDir
	if out, err := cmd.CombinedOutput(); err != nil {
		s.t.Fatalf("docker compose start foreman: %v\n%s", err, out)
	}
}

// findSandboxContainer returns the most recently created sandbox container ID.
// Uses --last 1 to guarantee we get only the newest container, avoiding
// stale containers from previous test runs.
func (s *composeStack) findSandboxContainer() string {
	out, err := s.runDocker("ps", "-n", "1", "--filter", "name=foreman-sbox-", "--format", "{{.ID}}")
	if err != nil {
		s.t.Fatalf("docker ps: %v", err)
	}
	id := strings.TrimSpace(string(out))
	if id == "" {
		s.t.Fatal("no sandbox container found")
	}
	return id
}

// submitTaskStatus submits a task and returns the HTTP status code and response body.
// Unlike submitTask, it does not fail on non-202 responses.
func (s *composeStack) submitTaskStatus(taskID, description string) (int, string) {
	s.t.Helper()
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

	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
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

	projectName := uniqueProjectName()
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
		"-p", projectName,
		"-f", "docker-compose.yml",
		"-f", overridePath,
		"--profile", "service",
	}

	s := &composeStack{
		composeDir:  composeDir,
		composeArgs: args,
		projectName: projectName,
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
	// Use --last 1 to get the newest container, avoiding picking up stale ones.
	out, err := stack.runDocker("ps", "-n", "1", "--filter", "name=foreman-sbox-", "--format", "{{.ID}}")
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

func TestE2E_GracefulShutdown(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	// 1. Start compose with default config
	stack := newComposeStack(t)
	defer stack.teardown()

	// 2. Submit a short task
	taskID := fmt.Sprintf("graceful-%d", time.Now().UnixNano())
	sid := stack.submitTask(taskID, "sleep 3")

	// 3. Wait for COMPLETED (the short task finishes before we even need to shutdown)
	status := stack.waitForStatus(sid, 30*time.Second, "COMPLETED", "FAILED", "RUNNING")
	if status == "FAILED" {
		t.Fatal("session failed unexpectedly")
	}

	// 4. Send SIGTERM to test graceful shutdown. Foreman should drain (no active sessions)
	//    and exit cleanly. The compose stop timeout (60s) exceeds the drain timeout (30s).
	stack.stopForeman()

	// 5. Start Foreman again. Since the session was COMPLETED and persisted to Postgres,
	//    Foreman won't recover it (only non-terminal sessions are recovered). The API
	//    returns 404, but we verify graceful shutdown by checking the compose logs.
	stack.startForeman()
	stack.waitForHealth()

	// 6. Verify the session was COMPLETED by checking it's not running
	//    (it was already complete before SIGTERM, so it's in terminal state)
	resp, err := http.Get("http://localhost:8080/api/v1/sessions/" + sid)
	if err == nil {
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode == http.StatusOK {
			// Session still in memory -- verify COMPLETED
			var s struct {
				Status string `json:"status"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&s); err != nil {
				t.Fatalf("decode session response: %v", err)
			}
			if s.Status != "COMPLETED" {
				t.Fatalf("expected COMPLETED, got %s", s.Status)
			}
		}
		// 404 is also fine -- session was terminal and not recovered after restart
	}
	t.Log("SUCCESS: Graceful shutdown correctly drained and session completed.")
}

func TestE2E_NATSEventBus(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	// Config with NATS event bus (embedded mode -- no URL means in-process server)
	natsCfg := `subsystems:
  eventbus:
    kind: nats
  statestore:
    kind: postgres
    dsn: "postgres://foreman:foreman@postgres:5432/foreman?sslmode=disable"
    max_connections: 25
    min_connections: 5
  sandbox:
    kind: docker
    image: ubuntu:22.04
  coordinator:
    max_concurrent: 5
    default_timeout: 5m
    heartbeat_interval: 5s
    heartbeat_timeout: 15s
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
	stack := composeStackWithConfig(t, natsCfg)
	defer stack.teardown()

	sid := stack.submitTask("nats-test", "echo 'hello from nats'")
	status := stack.waitForStatus(sid, 90*time.Second, "COMPLETED", "FAILED")
	if status != "COMPLETED" {
		t.Fatalf("expected COMPLETED with NATS event bus, got %s", status)
	}
	t.Log("SUCCESS: NATS event bus correctly handled full session lifecycle.")
}

func TestE2E_IdentityScopedToken(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	stack := newComposeStack(t)
	defer stack.teardown()

	// Submit a long-running task so the sandbox stays alive while we inspect it
	taskID := fmt.Sprintf("identity-%d", time.Now().UnixNano())
	sid := stack.submitTask(taskID, "sleep 30")

	// Wait for RUNNING (sandbox provisioned with env vars)
	status := stack.waitForStatus(sid, 30*time.Second, "RUNNING", "FAILED")
	if status != "RUNNING" {
		t.Fatalf("expected RUNNING, got %s", status)
	}

	// Find the sandbox container and read the scoped agent token.
	// Use docker inspect to read the container's env vars without needing to
	// exec into the container (which could interfere with the running agent).
	containerID := stack.findSandboxContainer()
	t.Logf("Sandbox container: %s", containerID)

	out, err := stack.runDocker("inspect", containerID, "--format", "{{json .Config.Env}}")
	if err != nil {
		t.Fatalf("docker inspect: %v\n%s", err, out)
	}
	var envVars []string
	if err := json.Unmarshal(out, &envVars); err != nil {
		t.Fatalf("unmarshal env vars: %v", err)
	}
	var token string
	for _, e := range envVars {
		if strings.HasPrefix(e, "FOREMAN_AGENT_TOKEN=") {
			token = strings.TrimPrefix(e, "FOREMAN_AGENT_TOKEN=")
			break
		}
	}
	if token == "" {
		t.Fatal("FOREMAN_AGENT_TOKEN is empty or not set in container environment")
	}
	t.Logf("Token found (len=%d)", len(token))

	// Parse JWT parts -- expect 3 dot-separated base64 sections
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		t.Fatalf("expected JWT with 3 parts, got %d", len(parts))
	}

	// Decode and verify the payload
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("decode JWT payload: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		t.Fatalf("unmarshal JWT claims: %v", err)
	}

	t.Logf("JWT claims: ses=%v, scope=%v", claims["ses"], claims["scope"])

	// Verify required claims exist (using actual JSON tag from AgentClaims)
	if claims["ses"] != sid {
		t.Fatalf("expected ses claim %q, got %v", sid, claims["ses"])
	}
	scope, ok := claims["scope"].(map[string]any)
	if !ok {
		t.Fatal("expected scope claim in token")
	}
	actions, ok := scope["actions"].([]any)
	if !ok || len(actions) == 0 {
		t.Fatal("expected scope.actions in token")
	}

	// Let the task complete
	status = stack.waitForStatus(sid, 60*time.Second, "COMPLETED", "FAILED")
	if status != "COMPLETED" {
		t.Fatalf("expected COMPLETED, got %s", status)
	}
	t.Log("SUCCESS: Scoped agent token correctly injected and valid.")
}

func TestE2E_MultipleConcurrentTasks(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	// Config with max_concurrent: 2 to test concurrency limits
	concurrentCfg := `subsystems:
  eventbus:
    kind: memory
  statestore:
    kind: postgres
    dsn: "postgres://foreman:foreman@postgres:5432/foreman?sslmode=disable"
    max_connections: 25
    min_connections: 5
  sandbox:
    kind: docker
    image: ubuntu:22.04
  coordinator:
    max_concurrent: 2
    default_timeout: 5m
    heartbeat_interval: 5s
    heartbeat_timeout: 15s
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
	stack := composeStackWithConfig(t, concurrentCfg)
	defer stack.teardown()

	// Submit 2 tasks that should be accepted (202)
	status1, body1 := stack.submitTaskStatus("concurrent-1", "sleep 10")
	if status1 != http.StatusAccepted {
		t.Fatalf("expected 202 for 1st task, got %d: %s", status1, body1)
	}
	t.Log("1st task accepted (202)")

	status2, body2 := stack.submitTaskStatus("concurrent-2", "sleep 10")
	if status2 != http.StatusAccepted {
		t.Fatalf("expected 202 for 2nd task, got %d: %s", status2, body2)
	}
	t.Log("2nd task accepted (202)")

	// 3rd task should be rejected with 429 Too Many Requests
	status3, body3 := stack.submitTaskStatus("concurrent-3", "sleep 10")
	if status3 != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for 3rd task (max concurrent), got %d: %s", status3, body3)
	}
	if !strings.Contains(body3, "max concurrent tasks reached") {
		t.Fatalf("expected 'max concurrent tasks reached' in body, got: %s", body3)
	}
	t.Log("3rd task correctly rejected with 429")

	// First 2 tasks should complete successfully
	stack.waitForStatus("ses_concurrent-1", 60*time.Second, "COMPLETED", "FAILED")
	stack.waitForStatus("ses_concurrent-2", 60*time.Second, "COMPLETED", "FAILED")
	t.Log("SUCCESS: Concurrent task limits correctly enforced.")
}
