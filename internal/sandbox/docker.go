package sandbox

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/foreman/foreman/internal/schemas"
)

const containerKeepAlive = "tail -f /dev/null"

type containerState struct {
	id        string
	createdAt time.Time
}

// DockerSandbox provisions sandboxes as Docker containers.
// Each sandbox runs "tail -f /dev/null" as its main process to stay alive;
// actual commands are executed via "docker exec".
type DockerSandbox struct {
	image      string
	mu         sync.Mutex
	containers map[string]*containerState // sessionID -> container
}

func NewDockerSandbox(image string) *DockerSandbox {
	return &DockerSandbox{
		image:      image,
		containers: make(map[string]*containerState),
	}
}

func (d *DockerSandbox) Provision(ctx context.Context, spec SandboxSpec) (string, error) {
	if _, err := exec.LookPath("docker"); err != nil {
		return "", fmt.Errorf("docker not found in PATH: %w", err)
	}

	img := spec.Image
	if img == "" {
		img = d.image
	}
	if img == "" {
		return "", fmt.Errorf("no container image specified (set in config or SandboxSpec)")
	}

	sessionID := fmt.Sprintf("foreman-sbox-%d", time.Now().UnixNano())

	args := []string{"run", "-d", "--name", sessionID}
	if spec.Memory != "" {
		args = append(args, "--memory", spec.Memory)
	}
	if spec.CPU != "" {
		args = append(args, "--cpus", spec.CPU)
	}
	for k, v := range spec.Env {
		args = append(args, "-e", fmt.Sprintf("%s=%s", k, v))
	}
	if spec.WorkDir != "" {
		args = append(args, "-w", spec.WorkDir)

		// Ensure working directory exists (docker create won't mkdir it)
		initArgs := []string{"run", "--rm", "--entrypoint", "", img, "mkdir", "-p", spec.WorkDir}
		if initOut, initErr := exec.CommandContext(ctx, "docker", initArgs...).CombinedOutput(); initErr != nil {
			return "", fmt.Errorf("docker create workdir: %w\noutput: %s", initErr, strings.TrimSpace(string(initOut)))
		}
	}
	// Split keep-alive command into separate args (Docker exec doesn't use a shell)
	keepAliveArgs := strings.Fields(containerKeepAlive)
	args = append(args, img)
	args = append(args, keepAliveArgs...)

	// Use Output() (stdout only), NOT CombinedOutput(), because docker run -d writes
	// the container ID to stdout but may write pull progress to stderr. CombinedOutput
	// merges both, polluting the container ID.
	cmd := exec.CommandContext(ctx, "docker", args...)
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("docker run: %w\nstderr: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("docker run: %w", err)
	}
	containerID := strings.TrimSpace(string(out))

	d.mu.Lock()
	d.containers[sessionID] = &containerState{id: containerID, createdAt: time.Now()}
	d.mu.Unlock()

	return sessionID, nil
}

func (d *DockerSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*ExecutionResult, error) {
	if _, err := d.lookup(sessionID); err != nil {
		return nil, err
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	dockerArgs := append([]string{"exec", "-i", sessionID}, cmd...)
	execCmd := exec.CommandContext(ctx, "docker", dockerArgs...)

	var stdout, stderr bytes.Buffer
	execCmd.Stdout = &stdout
	execCmd.Stderr = &stderr

	err := execCmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("docker exec: %w", err)
		}
	}

	return &ExecutionResult{
		ExitCode: exitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

func (d *DockerSandbox) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	if _, err := d.lookup(sessionID); err != nil {
		return err
	}

	// Use docker exec with tee to write content
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", sessionID, "tee", path)
	cmd.Stdin = bytes.NewReader(content)
	cmd.Stdout = io.Discard

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("docker exec tee start: %w", err)
	}

	errBuf := new(bytes.Buffer)
	if _, err := io.Copy(errBuf, stderr); err != nil {
		_ = cmd.Wait()
		return fmt.Errorf("read stderr: %w", err)
	}

	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("docker exec tee: %w\nstderr: %s", err, strings.TrimSpace(errBuf.String()))
	}
	return nil
}

func (d *DockerSandbox) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	if _, err := d.lookup(sessionID); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "docker", "exec", sessionID, "cat", path)
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("docker exec cat: %w", err)
	}
	return out, nil
}

func (d *DockerSandbox) UploadCheckpoint(ctx context.Context, sessionID, sourceDir string) (string, error) {
	if _, err := d.lookup(sessionID); err != nil {
		return "", err
	}

	// Create the .foreman directory inside the container
	mkdir := exec.CommandContext(ctx, "docker", "exec", sessionID, "mkdir", "-p", "/workspace/.foreman")
	if out, err := mkdir.CombinedOutput(); err != nil {
		return "", fmt.Errorf("mkdir checkpoint dir: %w\noutput: %s", err, strings.TrimSpace(string(out)))
	}

	// Archive and pipe into the container
	// tar cf - <sourceDir> | docker exec -i <sessionID> tar xf - -C /workspace/.foreman
	tarCmd := exec.CommandContext(ctx, "tar", "cf", "-", "-C", sourceDir, ".")
	dockerCmd := exec.CommandContext(ctx, "docker", "exec", "-i", sessionID, "tar", "xf", "-", "-C", "/workspace/.foreman")

	pipe, err := tarCmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("tar stdout pipe: %w", err)
	}
	dockerCmd.Stdin = pipe

	if err := tarCmd.Start(); err != nil {
		return "", fmt.Errorf("tar start: %w", err)
	}
	if err := dockerCmd.Start(); err != nil {
		_ = tarCmd.Wait()
		return "", fmt.Errorf("docker tar start: %w", err)
	}

	if err := tarCmd.Wait(); err != nil {
		_ = dockerCmd.Wait()
		return "", fmt.Errorf("tar wait: %w", err)
	}
	if err := dockerCmd.Wait(); err != nil {
		return "", fmt.Errorf("docker tar wait: %w", err)
	}

	checkpointID := fmt.Sprintf("cp-%d", time.Now().UnixNano())
	return checkpointID, nil
}

func (d *DockerSandbox) SubscribeEvents(ctx context.Context, sessionID string) (<-chan SandboxEvent, error) {
	if _, err := d.lookup(sessionID); err != nil {
		return nil, err
	}

	ch := make(chan SandboxEvent, 100)

	cmd := exec.CommandContext(ctx, "docker", "logs", "-f", sessionID)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		close(ch)
		return nil, fmt.Errorf("docker logs start: %w", err)
	}

	go func() {
		defer func() {
			if err := cmd.Wait(); err != nil {
				log.Printf("docker logs wait: %v", err)
			}
			close(ch)
		}()

		var wg sync.WaitGroup
		wg.Add(2)

		// Read stdout
		go func() {
			defer wg.Done()
			scanStream(ctx, ch, stdout, string(schemas.SandboxRunning))
		}()

		// Read stderr
		go func() {
			defer wg.Done()
			scanStream(ctx, ch, stderr, "stderr")
		}()

		wg.Wait()
	}()

	return ch, nil
}

func scanStream(ctx context.Context, ch chan<- SandboxEvent, r io.Reader, streamType string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		select {
		case ch <- SandboxEvent{Type: streamType, Payload: scanner.Text()}:
		case <-ctx.Done():
			return
		}
	}
}

func (d *DockerSandbox) Heartbeat(ctx context.Context, sessionID string) error {
	if _, err := d.lookup(sessionID); err != nil {
		return err
	}

	// Use the container name (sessionID) so docker inspect can find it
	cmd := exec.CommandContext(ctx, "docker", "inspect", "-f", "{{.State.Status}}", sessionID)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("docker inspect: %w", err)
	}

	status := strings.TrimSpace(string(out))
	if status != "running" {
		return fmt.Errorf("container status is %s (expected running)", status)
	}
	return nil
}

func (d *DockerSandbox) Destroy(ctx context.Context, sessionID string) error {
	cs, err := d.lookup(sessionID)
	if err != nil {
		return err
	}

	// Force remove the container
	out, err := exec.CommandContext(ctx, "docker", "rm", "-f", cs.id).CombinedOutput()
	if err != nil {
		return fmt.Errorf("docker rm: %w\noutput: %s", err, strings.TrimSpace(string(out)))
	}

	d.mu.Lock()
	delete(d.containers, sessionID)
	d.mu.Unlock()

	return nil
}

func (d *DockerSandbox) lookup(sessionID string) (*containerState, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	cs, ok := d.containers[sessionID]
	if !ok {
		return nil, fmt.Errorf("sandbox %s not found (not provisioned or already destroyed)", sessionID)
	}
	return cs, nil
}
