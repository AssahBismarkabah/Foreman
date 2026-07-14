package sandbox

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/docker/go-units"
)

type containerState struct {
	id        string
	createdAt time.Time
}

// DockerSandbox provisions sandboxes as Docker containers.
// Each sandbox runs "tail -f /dev/null" as its main process to stay alive;
// actual commands are executed via the Docker SDK exec API.
type DockerSandbox struct {
	image      string
	apiClient  *client.Client
	mu         sync.Mutex
	containers map[string]*containerState // sessionID -> container
}

func NewDockerSandbox(image string) (*DockerSandbox, error) {
	// Use DOCKER_HOST env var if set, otherwise default to the standard socket.
	// Importantly, we do NOT use client.FromEnv alone because Docker SDK v28+
	// reads the Docker config file's currentContext, which on OrbStack points to
	// a host-specific socket path that doesn't exist inside containers.
	host := os.Getenv("DOCKER_HOST")
	if host == "" {
		host = "unix:///var/run/docker.sock"
	}
	cli, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("docker client: %w", err)
	}

	// Verify the daemon is reachable
	if _, err := cli.Ping(context.Background()); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker daemon unreachable: %w", err)
	}

	return &DockerSandbox{
		image:      image,
		apiClient:  cli,
		containers: make(map[string]*containerState),
	}, nil
}

// Close cleans up the underlying Docker API client.
func (d *DockerSandbox) Close() error {
	return d.apiClient.Close()
}

// APIClient exposes the underlying Docker SDK client for coordinator-level
// operations such as container reaping.
func (d *DockerSandbox) APIClient() *client.Client {
	return d.apiClient
}

func (d *DockerSandbox) Provision(ctx context.Context, spec SandboxSpec) (string, error) {
	img := spec.Image
	if img == "" {
		img = d.image
	}
	if img == "" {
		return "", fmt.Errorf("no container image specified (set in config or SandboxSpec)")
	}

	sessionID := fmt.Sprintf("foreman-sbox-%d", time.Now().UnixNano())

	// Ensure the image is available locally.
	if err := d.ensureImage(ctx, img); err != nil {
		return "", fmt.Errorf("ensure image: %w", err)
	}

	// Build container config.
	cfg := &container.Config{
		Image: img,
		Cmd:   []string{"tail", "-f", "/dev/null"},
	}
	if spec.WorkDir != "" {
		cfg.WorkingDir = spec.WorkDir
	}
	if len(spec.Env) > 0 {
		env := make([]string, 0, len(spec.Env))
		for k, v := range spec.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cfg.Env = env
	}

	// Build host config with resource limits.
	hostCfg := &container.HostConfig{}
	if spec.Memory != "" {
		mem, err := units.RAMInBytes(spec.Memory)
		if err != nil {
			return "", fmt.Errorf("invalid memory spec %q: %w", spec.Memory, err)
		}
		hostCfg.Memory = mem
	}
	if spec.CPU != "" {
		var cpu float64
		if _, err := fmt.Sscanf(spec.CPU, "%f", &cpu); err != nil {
			return "", fmt.Errorf("invalid cpu spec %q: %w", spec.CPU, err)
		}
		hostCfg.NanoCPUs = int64(cpu * 1e9)
	}

	resp, err := d.apiClient.ContainerCreate(ctx, cfg, hostCfg, nil, nil, sessionID)
	if err != nil {
		return "", fmt.Errorf("container create: %w", err)
	}

	if err := d.apiClient.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = d.apiClient.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("container start: %w", err)
	}

	// Create the working directory if specified (Docker won't mkdir WorkingDir).
	// This must happen after ContainerStart (exec requires a running container).
	if spec.WorkDir != "" {
		mkdirCfg := container.ExecOptions{
			Cmd:          []string{"mkdir", "-p", spec.WorkDir},
			AttachStdout: false,
			AttachStderr: false,
		}
		mkdirResp, mErr := d.apiClient.ContainerExecCreate(ctx, resp.ID, mkdirCfg)
		if mErr != nil {
			_ = d.apiClient.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
			return "", fmt.Errorf("create mkdir exec: %w", mErr)
		}
		if mErr = d.apiClient.ContainerExecStart(ctx, mkdirResp.ID, container.ExecStartOptions{}); mErr != nil {
			_ = d.apiClient.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true})
			return "", fmt.Errorf("start mkdir exec: %w", mErr)
		}
	}

	d.mu.Lock()
	d.containers[sessionID] = &containerState{id: resp.ID, createdAt: time.Now()}
	d.mu.Unlock()

	return sessionID, nil
}

func (d *DockerSandbox) Execute(ctx context.Context, sessionID string, cmd []string, timeout time.Duration) (*ExecutionResult, error) {
	cs, err := d.lookup(sessionID)
	if err != nil {
		return nil, err
	}

	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	// Create exec configuration.
	execCfg := container.ExecOptions{
		Cmd:          cmd,
		AttachStdout: true,
		AttachStderr: true,
	}
	execResp, err := d.apiClient.ContainerExecCreate(ctx, cs.id, execCfg)
	if err != nil {
		return nil, fmt.Errorf("exec create: %w", err)
	}

	// Attach to the exec (this also starts it).
	attachResp, err := d.apiClient.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
	if err != nil {
		return nil, fmt.Errorf("exec attach: %w", err)
	}
	defer attachResp.Close()

	// Read stdout/stderr via the multiplexed stream.
	var stdout, stderr bytes.Buffer
	readDone := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(&stdout, &stderr, attachResp.Reader)
		readDone <- err
	}()

	select {
	case err := <-readDone:
		if err != nil {
			return nil, fmt.Errorf("exec read: %w", err)
		}
	case <-ctx.Done():
		attachResp.Close()
		<-readDone // wait for goroutine to finish
		return nil, fmt.Errorf("exec cancelled: %w", ctx.Err())
	}

	// Inspect to get exit code.
	inspectResp, err := d.apiClient.ContainerExecInspect(ctx, execResp.ID)
	if err != nil {
		return nil, fmt.Errorf("exec inspect: %w", err)
	}

	return &ExecutionResult{
		ExitCode: inspectResp.ExitCode,
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
	}, nil
}

func (d *DockerSandbox) WriteFile(ctx context.Context, sessionID, path string, content []byte) error {
	if _, err := d.lookup(sessionID); err != nil {
		return err
	}

	// Create a tar archive with the single file.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	hdr := &tar.Header{
		Name: filepath.Base(path),
		Size: int64(len(content)),
		Mode: 0644,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return fmt.Errorf("tar header: %w", err)
	}
	if _, err := tw.Write(content); err != nil {
		return fmt.Errorf("tar write: %w", err)
	}
	if err := tw.Close(); err != nil {
		return fmt.Errorf("tar close: %w", err)
	}

	dstDir := filepath.Dir(path)
	return d.apiClient.CopyToContainer(ctx, sessionID, dstDir, &buf, container.CopyToContainerOptions{})
}

func (d *DockerSandbox) ReadFile(ctx context.Context, sessionID, path string) ([]byte, error) {
	if _, err := d.lookup(sessionID); err != nil {
		return nil, err
	}

	reader, _, err := d.apiClient.CopyFromContainer(ctx, sessionID, path)
	if err != nil {
		return nil, fmt.Errorf("copy from container: %w", err)
	}
	defer func() { _ = reader.Close() }()

	tr := tar.NewReader(reader)
	hdr, err := tr.Next()
	if err != nil {
		return nil, fmt.Errorf("tar read: %w", err)
	}

	data, err := io.ReadAll(tr)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}
	if int64(len(data)) != hdr.Size {
		return nil, fmt.Errorf("file size mismatch: read %d, expected %d", len(data), hdr.Size)
	}

	return data, nil
}

func (d *DockerSandbox) UploadCheckpoint(ctx context.Context, sessionID, sourceDir string) (string, error) {
	if _, err := d.lookup(sessionID); err != nil {
		return "", err
	}

	// Create a tar archive of the source directory using the standard library.
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)

	// Walk the source directory and add each file/dir to the tar.
	if err := filepath.WalkDir(sourceDir, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relPath, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		if relPath == "." {
			return nil
		}
		info, err := de.Info()
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = relPath
		if info.IsDir() {
			hdr.Name += "/"
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if !info.IsDir() {
			data, rErr := os.ReadFile(path)
			if rErr != nil {
				return rErr
			}
			if _, wErr := tw.Write(data); wErr != nil {
				return wErr
			}
		}
		return nil
	}); err != nil {
		return "", fmt.Errorf("tar source dir: %w", err)
	}

	if err := tw.Close(); err != nil {
		return "", fmt.Errorf("tar close: %w", err)
	}

	// Ensure the target directory exists first.
	mkdirCfg := container.ExecOptions{
		Cmd:          []string{"mkdir", "-p", "/workspace/.foreman"},
		AttachStdout: false,
		AttachStderr: false,
	}
	mkdirResp, mErr := d.apiClient.ContainerExecCreate(ctx, sessionID, mkdirCfg)
	if mErr != nil {
		return "", fmt.Errorf("create mkdir exec: %w", mErr)
	}
	if mErr = d.apiClient.ContainerExecStart(ctx, mkdirResp.ID, container.ExecStartOptions{}); mErr != nil {
		return "", fmt.Errorf("start mkdir exec: %w", mErr)
	}

	if err := d.apiClient.CopyToContainer(ctx, sessionID, "/workspace/.foreman", &buf, container.CopyToContainerOptions{}); err != nil {
		return "", fmt.Errorf("copy checkpoint: %w", err)
	}

	checkpointID := fmt.Sprintf("cp-%d", time.Now().UnixNano())
	return checkpointID, nil
}

// SubscribeEvents returns a channel that emits container log lines as they
// are produced. This uses the Docker SDK's logs API and only captures output
// from PID 1 (the keep-alive process), not exec'd commands.
func (d *DockerSandbox) SubscribeEvents(ctx context.Context, sessionID string) (<-chan SandboxEvent, error) {
	if _, err := d.lookup(sessionID); err != nil {
		return nil, err
	}

	reader, err := d.apiClient.ContainerLogs(ctx, sessionID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       "all",
	})
	if err != nil {
		return nil, fmt.Errorf("container logs: %w", err)
	}

	ch := make(chan SandboxEvent, 100)

	go func() {
		defer func() { _ = reader.Close() }()
		defer close(ch)

		// Read multiplexed frames from the log stream.
		// Each frame has an 8-byte header: [streamType(1) + pad(3) + size(4)].
		hdr := make([]byte, 8)
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			if _, err := io.ReadFull(reader, hdr); err != nil {
				return
			}

			size := binary.BigEndian.Uint32(hdr[4:8])
			if size == 0 {
				continue
			}

			data := make([]byte, size)
			if _, err := io.ReadFull(reader, data); err != nil {
				return
			}

			streamType := "stdout"
			if hdr[0] == 2 {
				streamType = "stderr"
			}

			// Emit each line from the chunk.
			scanner := bufio.NewScanner(bytes.NewReader(data))
			for scanner.Scan() {
				line := strings.TrimRight(scanner.Text(), "\r\n")
				if line == "" {
					continue
				}
				select {
				case ch <- SandboxEvent{Type: streamType, Payload: line}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return ch, nil
}

func (d *DockerSandbox) Heartbeat(ctx context.Context, sessionID string) error {
	cs, err := d.lookup(sessionID)
	if err != nil {
		return err
	}

	inspect, err := d.apiClient.ContainerInspect(ctx, cs.id)
	if err != nil {
		return fmt.Errorf("container inspect: %w", err)
	}

	if inspect.State == nil {
		return fmt.Errorf("container state is nil")
	}
	if inspect.State.Status != container.StateRunning {
		log.Printf("sandbox: heartbeat FAIL for container %s (state=%s, id=%s)", sessionID, inspect.State.Status, cs.id)
		return fmt.Errorf("container status is %s (expected running)", inspect.State.Status)
	}
	return nil
}

// Stats returns CPU and memory usage for the sandbox container using the
// Docker Engine API. Implements the optional StatsProvider interface.
func (d *DockerSandbox) Stats(ctx context.Context, sessionID string) (*ResourceUsage, error) {
	if _, err := d.lookup(sessionID); err != nil {
		return nil, err
	}

	statsResp, err := d.apiClient.ContainerStats(ctx, sessionID, false)
	if err != nil {
		return nil, fmt.Errorf("container stats: %w", err)
	}
	defer func() { _ = statsResp.Body.Close() }()

	var raw dockerStatsJSON
	if err := json.NewDecoder(statsResp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("parse stats JSON: %w", err)
	}

	usage := &ResourceUsage{}

	// CPU percent calculation (same formula used by docker stats CLI).
	if raw.CPUStats.SystemCPUUsage > 0 && raw.PrecpuStats.SystemCPUUsage > 0 {
		cpuDelta := raw.CPUStats.CPUUsage.TotalUsage - raw.PrecpuStats.CPUUsage.TotalUsage
		systemDelta := raw.CPUStats.SystemCPUUsage - raw.PrecpuStats.SystemCPUUsage
		onlineCPUs := raw.CPUStats.OnlineCPUs
		if onlineCPUs == 0 {
			onlineCPUs = 1
		}
		usage.CPUPercent = (float64(cpuDelta) / float64(systemDelta)) * float64(onlineCPUs) * 100.0
	}

	// Memory stats.
	usage.MemoryBytes = raw.MemoryStats.Usage
	usage.MemoryLimit = raw.MemoryStats.Limit

	return usage, nil
}

// dockerStatsJSON maps the relevant fields from the Docker stats API response.
type dockerStatsJSON struct {
	CPUStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
		OnlineCPUs     uint32 `json:"online_cpus"`
	} `json:"cpu_stats"`
	PrecpuStats struct {
		CPUUsage struct {
			TotalUsage uint64 `json:"total_usage"`
		} `json:"cpu_usage"`
		SystemCPUUsage uint64 `json:"system_cpu_usage"`
	} `json:"precpu_stats"`
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
}

func (d *DockerSandbox) Destroy(ctx context.Context, sessionID string) error {
	cs, err := d.lookup(sessionID)
	if err != nil {
		return err
	}

	if err := d.apiClient.ContainerRemove(ctx, cs.id, container.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("container remove: %w", err)
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

// ensureImage pulls the image if it is not available locally.
func (d *DockerSandbox) ensureImage(ctx context.Context, img string) error {
	_, err := d.apiClient.ImageInspect(ctx, img)
	if err == nil {
		return nil // image already present
	}

	reader, err := d.apiClient.ImagePull(ctx, img, image.PullOptions{})
	if err != nil {
		return fmt.Errorf("image pull: %w", err)
	}
	defer func() { _ = reader.Close() }()
	// Consume the pull output to completion.
	_, _ = io.Copy(io.Discard, reader)
	return nil
}
