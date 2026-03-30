package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	"github.com/smallnest/goclaw/internal/core/config"
	"go.uber.org/zap"
)

const (
	shellFullOutputBytes     = 16 * 1024
	shellPreviewHeadBytes    = 4 * 1024
	shellPreviewTailBytes    = 4 * 1024
	shellPreviewDefaultName  = "stdout"
	shellRawOutputBytes      = 2 * 1024
	shellSummaryHeadLines    = 24
	shellSummaryTailLines    = 8
	shellSummaryMaxLineRunes = 180
	shellErrorPreviewRunes   = 3000
)

// ShellTool Shell 工具
type ShellTool struct {
	enabled       bool
	allowedCmds   []string
	deniedCmds    []string
	timeout       time.Duration
	workingDir    string
	sandboxConfig config.SandboxConfig
	dockerClient  *client.Client
}

// NewShellTool 创建 Shell 工具
func NewShellTool(
	enabled bool,
	allowedCmds, deniedCmds []string,
	timeout int,
	workingDir string,
	sandboxConfig config.SandboxConfig,
) *ShellTool {
	var t time.Duration
	if timeout > 0 {
		t = time.Duration(timeout) * time.Second
	} else {
		t = 120 * time.Second
	}

	st := &ShellTool{
		enabled:       enabled,
		allowedCmds:   allowedCmds,
		deniedCmds:    deniedCmds,
		timeout:       t,
		workingDir:    workingDir,
		sandboxConfig: sandboxConfig,
	}

	// 如果启用沙箱，初始化 Docker 客户端
	if sandboxConfig.Enabled {
		if cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation()); err == nil {
			st.dockerClient = cli
		} else {
			zap.L().Warn("Failed to initialize Docker client, sandbox disabled", zap.Error(err))
			st.sandboxConfig.Enabled = false
		}
	}

	return st
}

// Exec 执行 Shell 命令
func (t *ShellTool) Exec(ctx context.Context, params map[string]interface{}) (string, error) {
	if !t.enabled {
		return "", fmt.Errorf("shell tool is disabled")
	}

	command, ok := params["command"].(string)
	if !ok {
		return "", fmt.Errorf("command parameter is required")
	}

	// 检查危险命令
	if t.isDenied(command) {
		return "", fmt.Errorf("command is not allowed: %s", command)
	}

	// 根据是否启用沙箱选择执行方式
	if t.sandboxConfig.Enabled && t.dockerClient != nil {
		return t.execInSandbox(ctx, command)
	}
	return t.execDirect(ctx, command)
}

// execDirect 直接执行命令
func (t *ShellTool) execDirect(ctx context.Context, command string) (string, error) {
	// 执行命令
	cmd := exec.Command("sh", "-c", command)
	if t.workingDir != "" {
		cmd.Dir = t.workingDir
	}

	// 设置进程组，确保能够杀死整个进程树
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true,
	}

	// 获取输出管道
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdout.Close()
		return "", fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// 启动命令
	if err := cmd.Start(); err != nil {
		stdout.Close()
		stderr.Close()
		return "", fmt.Errorf("failed to start command: %w", err)
	}

	// 使用 channel 和 goroutine 实现超时控制
	type result struct {
		output string
		err    error
	}

	resultCh := make(chan result, 1)
	go func() {
		defer close(resultCh)

		stdoutCollector := newShellOutputCollector()
		stderrCollector := newShellOutputCollector()
		var stdoutErr, stderrErr error

		// 使用 goroutine 并行读取 stdout 和 stderr
		var wg sync.WaitGroup
		wg.Add(2)

		go func() {
			defer wg.Done()
			_, stdoutErr = io.Copy(stdoutCollector, stdout)
			stdout.Close()
		}()

		go func() {
			defer wg.Done()
			_, stderrErr = io.Copy(stderrCollector, stderr)
			stderr.Close()
		}()

		wg.Wait()

		// 等待命令完成
		waitErr := cmd.Wait()

		output := formatShellCommandOutput(stdoutCollector, stderrCollector)

		// 确定返回的错误
		resultErr := waitErr
		if stdoutErr != nil {
			resultErr = stdoutErr
		} else if stderrErr != nil {
			resultErr = stderrErr
		}

		resultCh <- result{output: output, err: resultErr}
	}()

	// 等待结果或超时
	select {
	case res := <-resultCh:
		if res.err != nil {
			return "", formatShellExecError(res.err, res.output)
		}
		return res.output, nil
	case <-time.After(t.timeout):
		// 超时：强制杀死进程组
		if cmd.Process != nil {
			// 先尝试优雅关闭（SIGTERM）
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
			// 给进程一点时间清理
			time.Sleep(100 * time.Millisecond)
			// 再强制杀死（SIGKILL）
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return "", fmt.Errorf("command timed out after %v", t.timeout)
	case <-ctx.Done():
		// 父 context 被取消
		if cmd.Process != nil {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		}
		return "", ctx.Err()
	}
}

// execInSandbox 在 Docker 容器中执行命令
func (t *ShellTool) execInSandbox(ctx context.Context, command string) (string, error) {
	containerName := fmt.Sprintf("goclaw-%d", time.Now().UnixNano())

	// 准备工作目录
	workdir := t.workingDir
	if workdir == "" {
		workdir = "."
	}

	// 准备挂载点
	binds := []string{
		workdir + ":" + t.sandboxConfig.Workdir,
	}

	// 创建并运行容器
	resp, err := t.dockerClient.ContainerCreate(ctx, &container.Config{
		Image:      t.sandboxConfig.Image,
		Cmd:        []string{"sh", "-c", command},
		WorkingDir: t.sandboxConfig.Workdir,
		Tty:        false,
	}, &container.HostConfig{
		Binds:       binds,
		NetworkMode: container.NetworkMode(t.sandboxConfig.Network),
		Privileged:  t.sandboxConfig.Privileged,
		AutoRemove:  t.sandboxConfig.Remove,
	}, nil, nil, containerName)
	if err != nil {
		return "", fmt.Errorf("failed to create container: %w", err)
	}

	// 确保容器被清理
	if !t.sandboxConfig.Remove {
		defer func() {
			_ = t.dockerClient.ContainerRemove(ctx, resp.ID, container.RemoveOptions{
				Force: true,
			})
		}()
	}

	// 启动容器
	if err := t.dockerClient.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("failed to start container: %w", err)
	}

	// 等待容器完成
	var statusCode int64
	statusCh, errCh := t.dockerClient.ContainerWait(ctx, resp.ID, container.WaitConditionNotRunning)
	select {
	case err := <-errCh:
		return "", fmt.Errorf("container wait error: %w", err)
	case status := <-statusCh:
		statusCode = status.StatusCode
	case <-ctx.Done():
		return "", ctx.Err()
	}

	// 获取日志
	out, err := t.dockerClient.ContainerLogs(ctx, resp.ID, container.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
	})
	if err != nil {
		return "", fmt.Errorf("failed to get container logs: %w", err)
	}
	defer out.Close()

	stdoutCollector := newShellOutputCollector()
	stderrCollector := newShellOutputCollector()
	if _, err := stdcopy.StdCopy(stdoutCollector, stderrCollector, out); err != nil {
		return "", fmt.Errorf("failed to decode container logs: %w", err)
	}

	output := formatShellCommandOutput(stdoutCollector, stderrCollector)
	if statusCode != 0 {
		return "", formatShellExecError(fmt.Errorf("container exited with code %d", statusCode), output)
	}

	return output, nil
}

// isDenied 检查命令是否被拒绝
func (t *ShellTool) isDenied(command string) bool {
	// 检查明确拒绝的命令
	for _, denied := range t.deniedCmds {
		if strings.Contains(command, denied) {
			return true
		}
	}

	// 如果有允许列表，检查是否在允许列表中
	if len(t.allowedCmds) > 0 {
		parts := strings.Fields(command)
		if len(parts) == 0 {
			return true
		}
		cmdName := parts[0]

		for _, allowed := range t.allowedCmds {
			if cmdName == allowed {
				return false
			}
		}
		return true
	}

	return false
}

// GetTools 获取所有 Shell 工具
func (t *ShellTool) GetTools() []Tool {
	var desc strings.Builder
	desc.WriteString("Execute a shell command")

	if t.sandboxConfig.Enabled {
		desc.WriteString(" inside a Docker sandbox container. Commands run in a containerized environment with network isolation.")
	} else {
		desc.WriteString(" on the host system")
	}

	desc.WriteString(". Use this for file operations, running scripts (Python, Node.js, etc.), installing dependencies, HTTP requests (curl), system diagnostics and more. Commands run in a non-interactive shell. ")
	desc.WriteString("PROHIBITED: Do NOT use 'crontab', 'crontab -l', 'crontab -e', or any system cron commands. ")
	desc.WriteString("For ALL scheduled task operations (create, list, edit, delete, enable, disable), you MUST use the 'cron' tool instead. ")
	desc.WriteString("The 'cron' tool manages goclaw's built-in scheduler - this is the ONLY way to manage scheduled tasks. ")
	desc.WriteString("Available cron commands: 'add' (create), 'list/ls' (list), 'rm/remove' (delete), 'enable', 'disable', 'run' (execute immediately), 'status', 'runs' (history).")

	return []Tool{
		NewBaseTool(
			"run_shell",
			desc.String(),
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "string",
						"description": "Shell command to execute. DO NOT use crontab commands - use the 'cron' tool for scheduled task management.",
					},
				},
				"required": []string{"command"},
			},
			t.Exec,
		),
	}
}

// Close 关闭工具
func (t *ShellTool) Close() error {
	if t.dockerClient != nil {
		return t.dockerClient.Close()
	}
	return nil
}

type shellOutputCollector struct {
	fullLimit int
	headLimit int
	tailLimit int

	totalBytes int
	totalLines int
	overflowed bool

	full bytes.Buffer
	head bytes.Buffer
	tail tailByteBuffer
}

func newShellOutputCollector() *shellOutputCollector {
	return &shellOutputCollector{
		fullLimit: shellFullOutputBytes,
		headLimit: shellPreviewHeadBytes,
		tailLimit: shellPreviewTailBytes,
		tail: tailByteBuffer{
			limit: shellPreviewTailBytes,
		},
	}
}

func (c *shellOutputCollector) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}

	c.totalBytes += len(p)
	c.totalLines += bytes.Count(p, []byte{'\n'})

	if !c.overflowed && c.full.Len()+len(p) <= c.fullLimit {
		return c.full.Write(p)
	}

	if !c.overflowed {
		c.overflowed = true
		existing := append([]byte(nil), c.full.Bytes()...)
		c.full.Reset()
		c.appendHead(existing)
		c.tail.Write(existing)
	}

	c.appendHead(p)
	c.tail.Write(p)
	return len(p), nil
}

func (c *shellOutputCollector) appendHead(p []byte) {
	if len(p) == 0 || c.head.Len() >= c.headLimit {
		return
	}
	remaining := c.headLimit - c.head.Len()
	if len(p) > remaining {
		p = p[:remaining]
	}
	_, _ = c.head.Write(p)
}

func (c *shellOutputCollector) render(name string, forceLabel bool) string {
	if c.totalBytes == 0 {
		return ""
	}
	if !c.overflowed {
		raw := c.full.String()
		if !forceLabel && name == shellPreviewDefaultName && shouldKeepRawShellOutput(raw) {
			return raw
		}
		summary := summarizeShellText(raw)
		if forceLabel || name != shellPreviewDefaultName {
			return formatShellSection(name, summary)
		}
		return summary
	}

	headText := summarizeShellExcerpt(c.head.String(), true)
	tailText := summarizeShellExcerpt(string(c.tail.Bytes()), false)
	omittedBytes := c.totalBytes - c.head.Len() - len(c.tail.Bytes())
	if omittedBytes < 0 {
		omittedBytes = 0
	}

	var body strings.Builder
	body.WriteString(headText)
	if body.Len() > 0 && !strings.HasSuffix(body.String(), "\n") {
		body.WriteString("\n")
	}
	body.WriteString(fmt.Sprintf("\n... omitted %s from %s", formatByteSize(int64(omittedBytes)), name))
	body.WriteString(fmt.Sprintf(" (total %s", formatByteSize(int64(c.totalBytes))))
	if c.totalLines > 0 {
		body.WriteString(fmt.Sprintf(", %d lines", c.totalLines))
	}
	body.WriteString(") ...\n\n")
	body.WriteString(tailText)

	if forceLabel || name != shellPreviewDefaultName {
		return formatShellSection(name, body.String())
	}
	return body.String()
}

type tailByteBuffer struct {
	limit int
	data  []byte
}

func (b *tailByteBuffer) Write(p []byte) {
	if b.limit <= 0 || len(p) == 0 {
		return
	}

	if len(p) >= b.limit {
		b.data = append(b.data[:0], p[len(p)-b.limit:]...)
		return
	}

	if len(b.data)+len(p) <= b.limit {
		b.data = append(b.data, p...)
		return
	}

	overflow := len(b.data) + len(p) - b.limit
	copy(b.data, b.data[overflow:])
	b.data = b.data[:len(b.data)-overflow]
	b.data = append(b.data, p...)
}

func (b *tailByteBuffer) Bytes() []byte {
	return append([]byte(nil), b.data...)
}

func formatShellCommandOutput(stdout, stderr *shellOutputCollector) string {
	hasStdout := stdout != nil && stdout.totalBytes > 0
	hasStderr := stderr != nil && stderr.totalBytes > 0
	if !hasStdout && !hasStderr {
		return ""
	}

	forceLabel := hasStderr ||
		(hasStdout && stdout.overflowed) ||
		(hasStderr && stderr.overflowed)

	sections := make([]string, 0, 2)
	if hasStdout {
		sections = append(sections, stdout.render("stdout", forceLabel))
	}
	if hasStderr {
		sections = append(sections, stderr.render("stderr", true))
	}

	return strings.Join(sections, "\n\n")
}

func formatShellSection(name, body string) string {
	body = strings.TrimLeft(body, "\n")
	if body == "" {
		return fmt.Sprintf("[%s]", name)
	}
	return fmt.Sprintf("[%s]\n%s", name, body)
}

func formatShellExecError(execErr error, output string) error {
	if strings.TrimSpace(output) == "" {
		return fmt.Errorf("command failed: %w", execErr)
	}

	preview := truncateRunes(output, shellErrorPreviewRunes)
	if preview != output {
		preview += "\n\n... (error output truncated)"
	}
	return fmt.Errorf("command failed: %w\n%s", execErr, preview)
}

func shouldKeepRawShellOutput(raw string) bool {
	if len(raw) > shellRawOutputBytes {
		return false
	}

	lines := splitShellLines(raw)
	if len(lines) > shellSummaryHeadLines {
		return false
	}

	for _, line := range lines {
		if runeCountWithoutTrailingNewline(line) > shellSummaryMaxLineRunes {
			return false
		}
	}

	return true
}

func summarizeShellText(raw string) string {
	lines := splitShellLines(raw)
	if len(lines) == 0 {
		return raw
	}

	if len(lines) <= shellSummaryHeadLines+shellSummaryTailLines {
		return joinAndAnnotateShellLines(lines)
	}

	head := lines[:shellSummaryHeadLines]
	tail := lines[len(lines)-shellSummaryTailLines:]

	var body strings.Builder
	body.WriteString(joinAndAnnotateShellLines(head))
	if body.Len() > 0 && !strings.HasSuffix(body.String(), "\n") {
		body.WriteString("\n")
	}
	body.WriteString(fmt.Sprintf("\n... omitted %d lines ...\n\n", len(lines)-len(head)-len(tail)))
	body.WriteString(joinAndAnnotateShellLines(tail))
	return body.String()
}

func summarizeShellExcerpt(raw string, keepHead bool) string {
	lines := splitShellLines(raw)
	if len(lines) == 0 {
		return raw
	}

	limit := shellSummaryHeadLines
	if !keepHead {
		limit = shellSummaryTailLines
	}
	if len(lines) > limit {
		if keepHead {
			lines = lines[:limit]
		} else {
			lines = lines[len(lines)-limit:]
		}
	}

	return joinAndAnnotateShellLines(lines)
}

func splitShellLines(raw string) []string {
	if raw == "" {
		return nil
	}
	lines := strings.SplitAfter(raw, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func joinAndAnnotateShellLines(lines []string) string {
	var body strings.Builder
	truncatedLines := 0
	for _, line := range lines {
		shortened, changed := compactShellLine(line)
		if changed {
			truncatedLines++
		}
		body.WriteString(shortened)
	}

	if truncatedLines > 0 {
		if body.Len() > 0 && !strings.HasSuffix(body.String(), "\n") {
			body.WriteString("\n")
		}
		body.WriteString(fmt.Sprintf("\n... %d long line(s) truncated to %d chars ...\n", truncatedLines, shellSummaryMaxLineRunes))
	}

	return body.String()
}

func compactShellLine(line string) (string, bool) {
	hasNewline := strings.HasSuffix(line, "\n")
	core := strings.TrimSuffix(line, "\n")
	runes := []rune(core)
	if len(runes) <= shellSummaryMaxLineRunes {
		return line, false
	}

	shortened := string(runes[:shellSummaryMaxLineRunes]) + "...(truncated)"
	if hasNewline {
		shortened += "\n"
	}
	return shortened, true
}

func runeCountWithoutTrailingNewline(line string) int {
	return len([]rune(strings.TrimSuffix(line, "\n")))
}

func truncateRunes(text string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}
