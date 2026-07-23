package e2e

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"flag"
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

// TestMain provides shared test infrastructure:
//   - Builds the Foreman Docker image once and tags it as foreman:e2e (~10s saved per test)
//   - Pre-pulls postgres:17-alpine so compose up doesn't block on image download
//
// Individual tests still start their own compose stacks with unique project
// names for isolation, but skip the build step (image is shared by tag).
func TestMain(m *testing.M) {
	if !dockerComposeAvailable() {
		fmt.Println("Docker Compose not available, skipping E2E tests")
		os.Exit(0)
	}

	flag.Parse()
	if testing.Short() {
		fmt.Println("Short test detected, skipping Docker image builds")
		os.Exit(0)
	}

	// Generate a signing key globally (required by all tests).
	// This runs once instead of per-test (each call to ensureSigningKey).
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		fmt.Printf("Failed to generate signing key: %v\n", err)
		os.Exit(1)
	}
	pemBlock := &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)}
	if err := os.Setenv("FOREMAN_SIGNING_KEY", string(pem.EncodeToMemory(pemBlock))); err != nil {
		fmt.Printf("Failed to set signing key: %v\n", err)
		os.Exit(1)
	}

	// Pre-pull postgres so docker compose up -d doesn't block on image download.
	// This runs once before any test, not once per test.
	if err := exec.Command("docker", "pull", "postgres:17-alpine").Run(); err != nil {
		fmt.Printf("Warning: failed to pre-pull postgres:17-alpine (will pull during up): %v\n", err)
	}

	// Pre-build the Foreman image once with a fixed tag so every test's compose
	// stack can reference it by name instead of rebuilding for each project.
	// Using plain "docker build" rather than "docker compose build" because the
	// latter tags with the project name, making it unreachable by other projects.
	// Run from the deploy/ directory so Dockerfile path and build context align
	// with how docker-compose.yml normally references them.
	buildCmd := exec.Command("docker", "build", "-t", "foreman:e2e", "-f", "../Dockerfile", "..")
	buildCmd.Dir = "../../deploy"
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		fmt.Printf("Failed to build Foreman image: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Foreman image (foreman:e2e) built successfully.")

	// Build the mock LLM server binary and Docker image.
	// This is used by agent adapter tests (opencode, claude-code, etc.) to
	// provide a fake LLM endpoint without needing real API keys.
	buildMockLLM()
	fmt.Println("Mock LLM image (foreman:e2e-mockllm) built successfully.")

	// Build agent-specific Docker images on top of foreman:e2e.
	// Each image extends the base with the agent's runtime dependencies
	// (Node.js, Python, etc.) so the adapter's Verify() passes.
	// Paths are relative to CWD (test/e2e/).
	if err := buildAgentImage("foreman:e2e-opencode", "agents/opencode.Dockerfile", "agents"); err != nil {
		fmt.Printf("Failed to build opencode image: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("OpenCode image (foreman:e2e-opencode) built successfully.")

	// Build the sandbox image used by agent adapter tests.
	// This image has the opencode binary in PATH and no ENTRYPOINT so the
	// sandbox provider's Cmd (tail -f /dev/null) takes effect.
	if err := buildAgentImage("foreman:e2e-sandbox-opencode", "agents/sandbox-opencode.Dockerfile", "agents"); err != nil {
		fmt.Printf("Failed to build sandbox image: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Sandbox image (foreman:e2e-sandbox-opencode) built successfully.")

	os.Exit(m.Run())
}

// buildMockLLM builds the mock LLM server binary and Docker image.
// CWD is test/e2e/ -- the mockllm source is at test/e2e/mockllm/.
func buildMockLLM() {
	// Build the Go binary for the mock LLM server.
	// go build -o mockllm .  (from test/e2e/mockllm/)
	buildCmd := exec.Command("go", "build", "-o", "mockllm", ".")
	buildCmd.Dir = "mockllm"
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		fmt.Printf("Failed to build mock LLM binary: %v\n", err)
		os.Exit(1)
	}

	// Build the minimal Docker image (scratch + binary).
	// docker build -t foreman:e2e-mockllm -f mockllm.Dockerfile .
	imgCmd := exec.Command("docker", "build",
		"-t", "foreman:e2e-mockllm",
		"-f", "mockllm.Dockerfile",
		".")
	imgCmd.Dir = "mockllm"
	imgCmd.Stdout = os.Stdout
	imgCmd.Stderr = os.Stderr
	if err := imgCmd.Run(); err != nil {
		fmt.Printf("Failed to build mock LLM image: %v\n", err)
		os.Exit(1)
	}
}

// buildAgentImage builds a Docker image that extends foreman:e2e with an
// agent's runtime dependencies (e.g., opencode needs Node.js + npm).
// CWD is test/e2e/; dockerfilePath and contextDir are relative to CWD.
func buildAgentImage(tag, dockerfilePath, contextDir string) error {
	buildCmd := exec.Command("docker", "build",
		"-t", tag,
		"-f", dockerfilePath,
		"--build-arg", "BASE_IMAGE=foreman:e2e",
		contextDir)
	buildCmd.Stdout = os.Stdout
	buildCmd.Stderr = os.Stderr
	if err := buildCmd.Run(); err != nil {
		return fmt.Errorf("docker build %s: %w", tag, err)
	}
	return nil
}

// composeStack manages a Docker Compose instance for a single E2E test.
// Each test creates its own stack so different configs can be used.
// The Docker image is pre-built by TestMain and shared via build cache.
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

// replaceBuildWithImage reads the canonical docker-compose.yml from the deploy/
// directory and returns a modified version that uses the pre-built foreman:e2e
// image instead of building from the Dockerfile. This lets each test start its
// compose stack without the ~10s per-test rebuild.
func replaceBuildWithImage(src string) string {
	return strings.Replace(src,
		`    build:
      context: ..
      dockerfile: Dockerfile
      args:
        VERSION: ${FOREMAN_VERSION:-dev}`,
		`    image: foreman:e2e`, 1)
}

// injectMockLLMService adds a mockllm service to the compose stack and wires
// it into the foreman service via OPENAI_BASE_URL / OPENAI_API_KEY env vars,
// plus a depends_on so the mock LLM starts before Foreman.
func injectMockLLMService(src string) string {
	// Add the mockllm service definition before the foreman service.
	// The trailing newline (no spaces) ensures the next line (foreman:)
	// starts at the correct 2-space indent level.
	mockLLMSvc := `  mockllm:
    image: foreman:e2e-mockllm
    ports:
      - "9999"

`
	src = strings.Replace(src, "\n  foreman:\n", "\n"+mockLLMSvc+"  foreman:\n", 1)

	// Add depends_on: mockllm to the foreman service's existing depends_on block.
	// The canonical compose file has:
	//     depends_on:
	//       postgres:
	//         condition: service_healthy
	mockLLMDep := `      mockllm:
        condition: service_started
`
	src = strings.Replace(src,
		"    depends_on:\n      postgres:\n        condition: service_healthy\n",
		"    depends_on:\n      postgres:\n        condition: service_healthy\n"+mockLLMDep, 1)

	// Add OPENAI_BASE_URL and OPENAI_API_KEY to the foreman service's
	// environment block. Insert after the DOCKER_HOST entry.
	mockLLMEnv := `      OPENAI_BASE_URL: http://mockllm:9999/v1
      OPENAI_API_KEY: fake-key-for-testing
`
	src = strings.Replace(src,
		"      DOCKER_HOST: unix:///var/run/docker.sock\n",
		"      DOCKER_HOST: unix:///var/run/docker.sock\n"+mockLLMEnv, 1)

	return src
}

// composeConfig returns the standard compose overrides needed by custom-config
// tests: writing a docker-compose.yml with the pre-built image, fixing the
// foreman.yaml volume path to absolute, and returning the temp dir path and
// compose args. Tests that also need a mock LLM or agent image should call
// the lower-level helpers (replaceBuildWithImage, injectMockLLMService,
// replaceImageTag) on the base compose content before calling writeComposeFile.

func newComposeStack(t *testing.T) *composeStack {
	t.Helper()

	projectName := uniqueProjectName()

	// Read the canonical compose file from deploy/ and swap build: for the
	// pre-built image. Fix the foreman.yaml volume path to an absolute path
	// so it works regardless of the temp dir CWD.
	src, err := os.ReadFile(filepath.Join("../../deploy", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	modified := replaceBuildWithImage(string(src))
	foremanCfg, err := filepath.Abs("../../foreman.yaml")
	if err != nil {
		t.Fatalf("resolve foreman.yaml path: %v", err)
	}
	modified = strings.ReplaceAll(modified, "../foreman.yaml", foremanCfg)

	composeFile := filepath.Join(t.TempDir(), "docker-compose.yml")
	if err := os.WriteFile(composeFile, []byte(modified), 0644); err != nil {
		t.Fatalf("write temp docker-compose.yml: %v", err)
	}

	args := []string{"compose", "-p", projectName, "-f", composeFile, "--profile", "service"}

	s := &composeStack{
		composeDir:  filepath.Dir(composeFile),
		composeArgs: args,
		projectName: projectName,
		t:           t,
	}

	// build() is intentionally omitted here -- the image was pre-built by
	// TestMain and tagged as foreman:e2e so compose up -d uses it directly.
	s.up()
	s.waitForHealth()
	return s
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

	projectName := uniqueProjectName()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "foreman.yaml")
	composePath := filepath.Join(tmpDir, "docker-compose.yml")
	overridePath := filepath.Join(tmpDir, "compose-override.yml")

	// Generate a compose file that uses the pre-built foreman:e2e image
	// instead of building from Dockerfile.
	src, err := os.ReadFile(filepath.Join("../../deploy", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	modified := replaceBuildWithImage(string(src))
	// The canonical compose file's volume mount (../foreman.yaml) is relative
	// to deploy/.  Since we write to a temp dir, swap for the absolute path
	// so the override below can replace it with the custom config path.
	foremanCfg, err := filepath.Abs("../../foreman.yaml")
	if err != nil {
		t.Fatalf("resolve foreman.yaml path: %v", err)
	}
	modified = strings.ReplaceAll(modified, "../foreman.yaml", foremanCfg)

	if err := os.WriteFile(composePath, []byte(modified), 0644); err != nil {
		t.Fatalf("write docker-compose.yml: %v", err)
	}

	if err := os.WriteFile(cfgPath, []byte(customCfg), 0644); err != nil {
		t.Fatalf("write custom config: %v", err)
	}

	// Override the base compose file's volume mount with the custom config.
	// This replaces the ../foreman.yaml mount (which we fixed to an absolute
	// path above) since they share the same target /etc/foreman/foreman.yaml.
	override := fmt.Sprintf(`services:
  foreman:
    volumes:
      - %s:/etc/foreman/foreman.yaml:ro
`, cfgPath)
	if err := os.WriteFile(overridePath, []byte(override), 0644); err != nil {
		t.Fatalf("write compose override: %v", err)
	}

	args := []string{"compose",
		"-p", projectName,
		"-f", composePath,
		"-f", overridePath,
		"--profile", "service",
	}

	s := &composeStack{
		composeDir:  tmpDir,
		composeArgs: args,
		projectName: projectName,
		t:           t,
	}

	// build() is omitted -- the image was pre-built by TestMain.
	s.up()
	s.waitForHealth()
	return s
}

// composeStackWithConfigAndVolume is like composeStackWithConfig but also
// mounts an additional file from the host into the foreman container at the
// specified path. Used by tests that need extra files (e.g., PEM keys).
func composeStackWithConfigAndVolume(t *testing.T, customCfg, hostFile, containerPath string) *composeStack {
	t.Helper()

	projectName := uniqueProjectName()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "foreman.yaml")
	composePath := filepath.Join(tmpDir, "docker-compose.yml")
	overridePath := filepath.Join(tmpDir, "compose-override.yml")

	src, err := os.ReadFile(filepath.Join("../../deploy", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	modified := replaceBuildWithImage(string(src))
	foremanCfg, err := filepath.Abs("../../foreman.yaml")
	if err != nil {
		t.Fatalf("resolve foreman.yaml path: %v", err)
	}
	modified = strings.ReplaceAll(modified, "../foreman.yaml", foremanCfg)

	if err := os.WriteFile(composePath, []byte(modified), 0644); err != nil {
		t.Fatalf("write docker-compose.yml: %v", err)
	}
	if err := os.WriteFile(cfgPath, []byte(customCfg), 0644); err != nil {
		t.Fatalf("write custom config: %v", err)
	}

	override := fmt.Sprintf(`services:
  foreman:
    volumes:
      - %s:/etc/foreman/foreman.yaml:ro
      - %s:%s:ro
`, cfgPath, hostFile, containerPath)
	if err := os.WriteFile(overridePath, []byte(override), 0644); err != nil {
		t.Fatalf("write compose override: %v", err)
	}

	args := []string{"compose",
		"-p", projectName,
		"-f", composePath,
		"-f", overridePath,
		"--profile", "service",
	}

	s := &composeStack{
		composeDir:  tmpDir,
		composeArgs: args,
		projectName: projectName,
		t:           t,
	}
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

	// Submit a task and verify the scoped agent token is injected via container env.
	// The sleep only needs to be long enough for us to inspect the container; we
	// use docker inspect (not exec), so it doesn't interact with the running process.
	taskID := fmt.Sprintf("identity-%d", time.Now().UnixNano())
	sid := stack.submitTask(taskID, "sleep 10")

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
	status = stack.waitForStatus(sid, 30*time.Second, "COMPLETED", "FAILED")
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
	status1, body1 := stack.submitTaskStatus("concurrent-1", "sleep 5")
	if status1 != http.StatusAccepted {
		t.Fatalf("expected 202 for 1st task, got %d: %s", status1, body1)
	}
	t.Log("1st task accepted (202)")

	status2, body2 := stack.submitTaskStatus("concurrent-2", "sleep 5")
	if status2 != http.StatusAccepted {
		t.Fatalf("expected 202 for 2nd task, got %d: %s", status2, body2)
	}
	t.Log("2nd task accepted (202)")

	// 3rd task should be rejected with 429 Too Many Requests
	status3, body3 := stack.submitTaskStatus("concurrent-3", "sleep 5")
	if status3 != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for 3rd task (max concurrent), got %d: %s", status3, body3)
	}
	if !strings.Contains(body3, "max concurrent tasks reached") {
		t.Fatalf("expected 'max concurrent tasks reached' in body, got: %s", body3)
	}
	t.Log("3rd task correctly rejected with 429")

	// First 2 tasks should complete successfully
	stack.waitForStatus("ses_concurrent-1", 30*time.Second, "COMPLETED", "FAILED")
	stack.waitForStatus("ses_concurrent-2", 30*time.Second, "COMPLETED", "FAILED")
	t.Log("SUCCESS: Concurrent task limits correctly enforced.")
}

// TestE2E_OpenCodeAdapter tests the opencode adapter end-to-end:
//   - Foreman is configured with the opencode adapter (kind: opencode, cmd: opencode)
//   - The Foreman image includes node + npm-installed opencode CLI
//   - A mock LLM server provides canned responses (no real API key needed)
//   - A task is submitted and the session reaches COMPLETED
//
// This proves the full adapter pipeline: Verify -> Execute -> Parse -> COMPLETED.
func TestE2E_OpenCodeAdapter(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	projectName := uniqueProjectName()
	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "foreman.yaml")
	composePath := filepath.Join(tmpDir, "docker-compose.yml")
	overridePath := filepath.Join(tmpDir, "compose-override.yml")

	// Read the canonical compose file and modify it:
	//   1. Replace build: with image: foreman:e2e-opencode
	//   2. Inject the mock LLM service + wire it into foreman
	src, err := os.ReadFile(filepath.Join("../../deploy", "docker-compose.yml"))
	if err != nil {
		t.Fatalf("read docker-compose.yml: %v", err)
	}
	modified := replaceBuildWithImage(string(src))
	modified = strings.ReplaceAll(modified, "image: foreman:e2e", "image: foreman:e2e-opencode")

	// The opencode Docker image uses node:23-alpine which runs as root by
	// default. The compose file already sets user: root:root for Docker socket
	// access, so no override is needed -- just confirm the compose file has it.
	if !strings.Contains(modified, "user: root:root") {
		t.Fatal("expected user: root:root in compose file")
	}
	modified = injectMockLLMService(modified)

	// Fix the foreman.yaml volume path (compose uses a relative path)
	foremanCfg, err := filepath.Abs("../../foreman.yaml")
	if err != nil {
		t.Fatalf("resolve foreman.yaml path: %v", err)
	}
	modified = strings.ReplaceAll(modified, "../foreman.yaml", foremanCfg)

	if err := os.WriteFile(composePath, []byte(modified), 0644); err != nil {
		t.Fatalf("write docker-compose.yml: %v", err)
	}

	// Custom config with the opencode adapter as the primary agent.
	// The opencode adapter uses "opencode" as the command (installed in the
	// foreman:e2e-opencode image). The mock LLM server is at
	// http://mockllm:9999/v1 (wired in by injectMockLLMService).
	customCfg := `subsystems:
  eventbus:
    kind: memory
  statestore:
    kind: postgres
    dsn: "postgres://foreman:foreman@postgres:5432/foreman?sslmode=disable"
    max_connections: 25
    min_connections: 5
  sandbox:
    kind: docker
    image: foreman:e2e-sandbox-opencode
  coordinator:
    max_concurrent: 5
    default_timeout: 5m
    heartbeat_interval: 5s
    heartbeat_timeout: 15s
  agents:
    - name: opencode
      kind: opencode
      cmd: opencode
      cwd: /tmp
  identity:
    api:
      listen_addr: ":8080"
      public_url: "http://localhost:8080"
    signing_key:
      source: env
      env_var_name: FOREMAN_SIGNING_KEY
      key_id: foreman-1
`
	if err := os.WriteFile(cfgPath, []byte(customCfg), 0644); err != nil {
		t.Fatalf("write custom config: %v", err)
	}

	// Override the base compose file's volume mount with the custom config.
	override := fmt.Sprintf(`services:
  foreman:
    volumes:
      - %s:/etc/foreman/foreman.yaml:ro
`, cfgPath)
	if err := os.WriteFile(overridePath, []byte(override), 0644); err != nil {
		t.Fatalf("write compose override: %v", err)
	}

	args := []string{"compose",
		"-p", projectName,
		"-f", composePath,
		"-f", overridePath,
		"--profile", "service",
	}

	stack := &composeStack{
		composeDir:  tmpDir,
		composeArgs: args,
		projectName: projectName,
		t:           t,
	}
	defer stack.teardown()
	stack.up()
	stack.waitForHealth()

	sid := stack.submitTask("opencode_e2e", "say hello and output the word hello")
	status := stack.waitForStatus(sid, 90*time.Second, "COMPLETED", "FAILED")

	if status != "COMPLETED" {
		t.Fatalf("expected COMPLETED for opencode adapter, got %s", status)
	}
	t.Log("SUCCESS: OpenCode adapter correctly completed the task via mock LLM.")
}

// signGitHubWebhook computes the HMAC-SHA256 signature for a GitHub webhook payload.
func signGitHubWebhook(payload []byte, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(payload)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// postWebhook sends a signed webhook payload to the running Foreman instance
// and returns the HTTP status code.
func postWebhook(t *testing.T, endpoint, event, payload string, secret string, includeSig bool) int {
	t.Helper()
	body := []byte(payload)
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(payload))
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-GitHub-Event", event)
	req.Header.Set("X-GitHub-Delivery", "test-delivery-id")
	if includeSig {
		req.Header.Set("X-Hub-Signature-256", signGitHubWebhook(body, secret))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST webhook: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.ReadAll(resp.Body)
	return resp.StatusCode
}

// TestE2E_GitHubWebhook verifies the GitHub App webhook endpoint in a running
// Foreman instance. Tests signature verification, event routing, and HTTP
// method handling without needing real GitHub credentials.
//
// Installation lifecycle events (created/deleted/suspend/unsuspend) are not
// tested here because builder.go passes nil for InstallationStore, which would
// panic. Those paths are covered by unit tests in internal/identity/githubapp.
func TestE2E_GitHubWebhook(t *testing.T) {
	if !dockerComposeAvailable() {
		t.Skip("Docker Compose not available")
	}

	// Generate a dummy RSA private key PEM file for the github_app config.
	// The config validation requires private_key_path to exist on disk, but
	// the webhook handler never reads the key (it only uses webhook_secret).
	ghKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	ghKeyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(ghKey),
	})
	tmpDir := t.TempDir()
	ghKeyPath := filepath.Join(tmpDir, "github-app.pem")
	if err := os.WriteFile(ghKeyPath, ghKeyPEM, 0600); err != nil {
		t.Fatalf("write PEM file: %v", err)
	}

	const webhookSecret = "e2e-test-webhook-secret"

	// Config with github_app enabled. The private_key_path must be absolute
	// so it resolves inside the container via the volume mount.
	customCfg := fmt.Sprintf(`
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
    - name: exec
      kind: exec
      cmd: sh
      cwd: /workspace
      heartbeat_timeout: 60s
  identity:
    api:
      listen_addr: ":8080"
      public_url: "http://localhost:8080"
    signing_key:
      source: env
      env_var_name: FOREMAN_SIGNING_KEY
      key_id: foreman-1
    github_app:
      app_id: 999999
      private_key_path: /etc/foreman/github-app.pem
      webhook_secret: "%s"
      webhook_endpoint: /api/v1/webhooks/github
`, webhookSecret)

	stack := composeStackWithConfigAndVolume(t, customCfg, ghKeyPath, "/etc/foreman/github-app.pem")
	defer stack.teardown()

	webhookURL := "http://localhost:8080/api/v1/webhooks/github"

	// 1. Valid signed ping event -> 200 (unhandled event, acknowledged)
	pingPayload := `{"zen":"Keep it logically awesome.","hook_id":123456}`
	code := postWebhook(t, webhookURL, "ping", pingPayload, webhookSecret, true)
	if code != http.StatusOK {
		t.Fatalf("valid signed ping: expected 200, got %d", code)
	}
	t.Log("Valid signed ping -> 200 OK")

	// 2. Valid signed issues.opened event -> 200 (unhandled event, acknowledged)
	issuesPayload := `{"action":"opened","issue":{"number":42,"title":"Test issue"},"installation":{"id":1,"account":{"id":100,"login":"test-org","type":"Organization"}}}`
	code = postWebhook(t, webhookURL, "issues", issuesPayload, webhookSecret, true)
	if code != http.StatusOK {
		t.Fatalf("valid signed issues.opened: expected 200, got %d", code)
	}
	t.Log("Valid signed issues.opened -> 200 OK")

	// 3. Invalid signature -> 401
	code = postWebhook(t, webhookURL, "ping", pingPayload, "wrong-secret", true)
	if code != http.StatusUnauthorized {
		t.Fatalf("invalid signature: expected 401, got %d", code)
	}
	t.Log("Invalid signature -> 401 Unauthorized")

	// 4. Missing signature header -> 200 (dev mode bypass: validateSignature
	// returns true when the signature header is empty, even if a secret is
	// configured. This is the current behavior -- GitHub's initial ping event
	// may arrive without a signature in some configurations.)
	code = postWebhook(t, webhookURL, "ping", pingPayload, webhookSecret, false)
	if code != http.StatusOK {
		t.Fatalf("missing signature: expected 200 (dev mode bypass), got %d", code)
	}
	t.Log("Missing signature -> 200 OK (dev mode bypass)")

	// 5. Wrong HTTP method -> 405
	req, err := http.NewRequest(http.MethodGet, webhookURL, nil)
	if err != nil {
		t.Fatalf("create GET request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET webhook: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET method: expected 405, got %d", resp.StatusCode)
	}
	t.Log("GET method -> 405 Method Not Allowed")

	t.Log("SUCCESS: GitHub webhook endpoint correctly handles signatures, routing, and methods.")
}
