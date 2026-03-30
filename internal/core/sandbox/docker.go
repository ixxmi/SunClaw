package sandbox

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
)

// DockerExecutor 使用 Docker 执行代码。
type DockerExecutor struct {
	cli   *client.Client
	image string
	pool  *ContainerPool
	local *LocalExecutor
}

// NewDockerExecutor 创建 Docker 执行器。
func NewDockerExecutor(cli *client.Client, image string, pool *ContainerPool, local *LocalExecutor) *DockerExecutor {
	executor := &DockerExecutor{
		cli:   cli,
		image: image,
		local: local,
	}

	if cli == nil || image == "" {
		executor.pool = pool
		return executor
	}

	if pool == nil {
		pool = NewContainerPool(image, 0)
	}
	pool.cfg.Image = image
	pool.cfg.Factory = func(ctx context.Context, img string) (string, error) {
		return executor.createContainer(ctx, img)
	}
	pool.cfg.HealthCheckFunc = executor.checkContainerHealth
	pool.cfg.Destroy = executor.destroyContainer
	executor.pool = pool

	return executor
}

// Execute 使用容器池获取一个真实 Docker 容器，在容器内执行用户代码。
func (d *DockerExecutor) Execute(ctx context.Context, code, lang string, opts ExecuteOptions) Result {
	if d.cli == nil || d.image == "" {
		return d.fallback(ctx, code, lang, opts, "docker executor is not ready")
	}

	if d.pool == nil {
		d.pool = NewContainerPoolWithConfig(PoolConfig{
			Image:           d.image,
			WarmTarget:      0,
			AcquireTimeout:  30 * time.Second,
			KeepAlive:       5 * time.Minute,
			HealthCheck:     30 * time.Second,
			Factory:         func(ctx context.Context, img string) (string, error) { return d.createContainer(ctx, img) },
			Destroy:         d.destroyContainer,
			HealthCheckFunc: d.checkContainerHealth,
		})
	}

	containerID, err := d.pool.Acquire(ctx)
	if err != nil {
		log.Printf("sandbox: docker pool acquire failed, falling back to local executor: %v", err)
		return d.fallback(ctx, code, lang, opts, fmt.Sprintf("docker pool acquire failed: %v", err))
	}
	defer d.pool.Release(containerID)

	if err := d.checkContainerHealth(ctx, containerID); err != nil {
		log.Printf("sandbox: pooled container %s unhealthy, falling back to local executor: %v", containerID, err)
		_ = d.destroyContainer(context.Background(), containerID)
		return d.fallback(ctx, code, lang, opts, fmt.Sprintf("docker container unhealthy: %v", err))
	}

	result, err := d.executeInContainer(ctx, containerID, code, lang, opts)
	if err != nil {
		log.Printf("sandbox: docker exec failed in container %s, falling back to local executor: %v", containerID, err)
		return d.fallback(ctx, code, lang, opts, fmt.Sprintf("docker exec failed: %v", err))
	}
	return result
}

func (d *DockerExecutor) executeInContainer(ctx context.Context, containerID, code, lang string, opts ExecuteOptions) (Result, error) {
	command, args, err := buildCommand(code, lang, opts)
	if err != nil {
		return Result{
			Executor:   "docker",
			Sandboxed:  true,
			Authorized: true,
			ExitCode:   -1,
			Error:      err,
		}, nil
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if opts.Timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	cmd := append([]string{command}, args...)
	execConfig := container.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		AttachStdin:  opts.Stdin != "",
		WorkingDir:   opts.WorkingDir,
		Cmd:          cmd,
		Env:          mapToEnvSlice(opts.Env),
	}

	start := time.Now()
	created, err := d.cli.ContainerExecCreate(execCtx, containerID, execConfig)
	if err != nil {
		return Result{}, fmt.Errorf("create docker exec: %w", err)
	}

	attach, err := d.cli.ContainerExecAttach(execCtx, created.ID, container.ExecAttachOptions{})
	if err != nil {
		return Result{}, fmt.Errorf("attach docker exec %s: %w", created.ID, err)
	}
	defer attach.Close()

	if opts.Stdin != "" {
		go func() {
			_, _ = io.Copy(attach.Conn, strings.NewReader(opts.Stdin))
			_ = attach.CloseWrite()
		}()
	}

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	copyDone := make(chan error, 1)
	go func() {
		_, err := stdcopy.StdCopy(&stdoutBuf, &stderrBuf, attach.Reader)
		if err != nil && !isClosedConnectionError(err) {
			copyDone <- err
			return
		}
		copyDone <- nil
	}()

	var runErr error
	for {
		inspect, inspectErr := d.cli.ContainerExecInspect(execCtx, created.ID)
		if inspectErr != nil {
			runErr = fmt.Errorf("inspect docker exec %s: %w", created.ID, inspectErr)
			break
		}
		if !inspect.Running {
			result := Result{
				Stdout:     stdoutBuf.String(),
				Stderr:     stderrBuf.String(),
				ExitCode:   inspect.ExitCode,
				Duration:   time.Since(start),
				Executor:   "docker",
				Sandboxed:  true,
				Authorized: true,
			}
			if inspect.ExitCode != 0 {
				result.Error = fmt.Errorf("process exited with code %d", inspect.ExitCode)
			}
			if copyErr := <-copyDone; copyErr != nil {
				if result.Error == nil {
					result.Error = fmt.Errorf("read docker exec output: %w", copyErr)
				}
			}
			return result, nil
		}

		select {
		case copyErr := <-copyDone:
			if copyErr != nil {
				runErr = fmt.Errorf("read docker exec output: %w", copyErr)
			} else {
				runErr = fmt.Errorf("docker exec %s output stream closed unexpectedly", created.ID)
			}
		case <-execCtx.Done():
			runErr = execCtx.Err()
		case <-time.After(100 * time.Millisecond):
			continue
		}
		break
	}

	if copyErr := <-copyDone; copyErr != nil && runErr == nil {
		runErr = fmt.Errorf("read docker exec output: %w", copyErr)
	}

	return Result{
		Stdout:     stdoutBuf.String(),
		Stderr:     stderrBuf.String(),
		ExitCode:   -1,
		Duration:   time.Since(start),
		Executor:   "docker",
		Sandboxed:  true,
		Authorized: true,
		Error:      runErr,
	}, nil
}

func (d *DockerExecutor) createContainer(ctx context.Context, imageName string) (string, error) {
	if d.cli == nil {
		return "", fmt.Errorf("docker client is nil")
	}
	if imageName == "" {
		return "", fmt.Errorf("docker image is empty")
	}
	if err := d.ensureImage(ctx, imageName); err != nil {
		return "", err
	}

	resp, err := d.cli.ContainerCreate(ctx, &container.Config{
		Image:       imageName,
		Tty:         false,
		OpenStdin:   false,
		StdinOnce:   false,
		AttachStdin: false,
		Cmd:         []string{"sh", "-c", "while true; do sleep 3600; done"},
	}, &container.HostConfig{}, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("create docker container: %w", err)
	}

	if err := d.cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		_ = d.cli.ContainerRemove(context.Background(), resp.ID, container.RemoveOptions{Force: true})
		return "", fmt.Errorf("start docker container %s: %w", resp.ID, err)
	}
	return resp.ID, nil
}

func (d *DockerExecutor) destroyContainer(ctx context.Context, containerID string) error {
	if d.cli == nil || containerID == "" {
		return nil
	}
	if err := d.cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil && !isDockerNotFound(err) {
		return fmt.Errorf("remove docker container %s: %w", containerID, err)
	}
	return nil
}

func (d *DockerExecutor) checkContainerHealth(ctx context.Context, containerID string) error {
	if d.cli == nil {
		return fmt.Errorf("docker client is nil")
	}
	if containerID == "" {
		return fmt.Errorf("docker container id is empty")
	}
	inspected, err := d.cli.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect docker container %s: %w", containerID, err)
	}
	if !inspected.State.Running {
		return fmt.Errorf("docker container %s is not running", containerID)
	}
	return nil
}

func (d *DockerExecutor) ensureImage(ctx context.Context, imageName string) error {
	if d.cli == nil {
		return fmt.Errorf("docker client is nil")
	}
	if strings.TrimSpace(imageName) == "" {
		return fmt.Errorf("docker image is empty")
	}

	_, _, err := d.cli.ImageInspectWithRaw(ctx, imageName)
	if err == nil {
		return nil
	}
	if !client.IsErrNotFound(err) {
		return fmt.Errorf("inspect docker image %s: %w", imageName, err)
	}

	reader, pullErr := d.cli.ImagePull(ctx, imageName, image.PullOptions{})
	if pullErr != nil {
		return fmt.Errorf("pull docker image %s: %w", imageName, pullErr)
	}
	defer reader.Close()
	_, _ = io.Copy(io.Discard, reader)
	return nil
}

func (d *DockerExecutor) fallback(ctx context.Context, code, lang string, opts ExecuteOptions, reason string) Result {
	if d.local != nil {
		result := d.local.Execute(ctx, code, lang, opts)
		result.Executor = "docker-fallback-local"
		result.UsedFallback = true
		return result
	}
	if reason == "" {
		reason = "docker executor fallback unavailable"
	}
	return Result{
		Executor:     "docker",
		Sandboxed:    true,
		UsedFallback: false,
		Error:        errors.New(reason),
	}
}

func isDockerNotFound(err error) bool {
	return err != nil && client.IsErrNotFound(err)
}

func mapToEnvSlice(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	result := make([]string, 0, len(env))
	for k, v := range env {
		result = append(result, fmt.Sprintf("%s=%s", k, v))
	}
	return result
}

func isClosedConnectionError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "use of closed network connection") || strings.Contains(msg, "closed pipe")
}
