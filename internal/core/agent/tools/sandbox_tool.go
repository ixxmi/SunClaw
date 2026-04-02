package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/client"
	"github.com/smallnest/goclaw/internal/core/agent/tooltypes"
	"github.com/smallnest/goclaw/internal/core/config"
	"github.com/smallnest/goclaw/internal/core/sandbox"
	"go.uber.org/zap"
)

const (
	sandboxToolDefaultTimeout = 30 * time.Second
)

// SandboxTool executes AI-generated code through the sandbox orchestrator when needed.
type SandboxTool struct {
	orchestrator *sandbox.Orchestrator
}

// NewSandboxTool creates a sandbox agent tool.
func NewSandboxTool(orchestrator *sandbox.Orchestrator) Tool {
	if orchestrator == nil {
		orchestrator = sandbox.NewOrchestrator(nil, nil, nil)
	}

	tool := &SandboxTool{orchestrator: orchestrator}
	return NewBaseToolWithSpec(
		"sandbox_execute",
		"Run an inline code snippet or command through sandbox-aware execution. Use this only for short, self-contained code/command content that may need isolated execution or authorization checks. Do NOT use it for ordinary workspace shell tasks, file operations, package installs, or long command chains; use run_shell instead. The tool first checks whether the input actually looks executable. If not, it returns needs_sandbox=false. Actual execution also requires a configured sandbox executor; local fallback requires prior authorization.",
		map[string]any{
			"type": "object",
			"properties": map[string]any{
				"code": map[string]any{
					"type":        "string",
					"description": "Inline code snippet or command text to execute. Prefer short, self-contained content instead of multi-step shell workflows.",
				},
				"language": map[string]any{
					"type":        "string",
					"description": "Execution language. Supported values include bash, sh, shell, python, python3, node, javascript, and js.",
				},
				"working_dir": map[string]any{
					"type":        "string",
					"description": "Optional working directory for the executed process.",
				},
				"stdin": map[string]any{
					"type":        "string",
					"description": "Optional stdin content passed to the process.",
				},
				"args": map[string]any{
					"type":        "array",
					"description": "Optional extra arguments appended by the executor when the selected language supports them.",
					"items":       map[string]any{"type": "string"},
				},
				"env": map[string]any{
					"type":                 "object",
					"description":          "Optional environment variables for the executed process.",
					"additionalProperties": map[string]any{"type": "string"},
				},
				"timeout_seconds": map[string]any{
					"type":        "integer",
					"description": "Optional timeout in seconds.",
					"minimum":     1,
				},
			},
			"required": []string{"code"},
		},
		tooltypes.ToolSpec{
			Concurrency:      tooltypes.ConcurrencyExclusive,
			Mutation:         tooltypes.MutationSideEffect,
			Risk:             tooltypes.RiskHigh,
			DefaultTimeout:   int(sandboxToolDefaultTimeout / time.Second),
			PrefersSandbox:   true,
			RequiresApproval: true,
			Tags:             []string{"sandbox", "code", "command"},
		},
		tool.execute,
	)
}

// NewSandboxToolWithConfig creates sandbox_execute with a configured executor chain.
func NewSandboxToolWithConfig(sandboxConfig config.SandboxConfig, approvals config.ApprovalsConfig) Tool {
	auth := sandbox.NewAuthManager()
	if sandboxLocalExecutionAllowed(approvals) {
		_ = auth.Authorize(context.Background())
	}

	localExecutor := sandbox.NewLocalExecutor(auth)
	var executor sandbox.SandboxExecutor = localExecutor

	if sandboxConfig.Enabled {
		image := strings.TrimSpace(sandboxConfig.Image)
		if image == "" {
			zap.L().Warn("sandbox_execute enabled without image; falling back to local executor")
		} else if cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation()); err == nil {
			executor = sandbox.NewDockerExecutor(cli, image, nil, localExecutor)
		} else {
			zap.L().Warn("Failed to initialize Docker client for sandbox_execute; falling back to local executor", zap.Error(err))
		}
	}

	return NewSandboxTool(sandbox.NewOrchestrator(executor, nil, auth))
}

func (t *SandboxTool) execute(ctx context.Context, params map[string]interface{}) (string, error) {
	code := strings.TrimSpace(sandboxStringParam(params, "code"))
	if code == "" {
		return marshalSandboxToolResult("sandbox_execute requires non-empty code", map[string]any{"ok": false}), fmt.Errorf("missing code")
	}

	lang := strings.TrimSpace(sandboxStringParam(params, "language"))
	if lang == "" {
		lang = "bash"
	}

	timeout := sandboxDurationParam(params, "timeout_seconds", sandboxToolDefaultTimeout)
	execCtx := ctx
	if timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	if !t.orchestrator.NeedSandbox(execCtx, code) {
		return marshalSandboxToolResult(
			"detector judged that this request does not require sandbox execution",
			map[string]any{
				"ok":            true,
				"needs_sandbox": false,
				"language":      lang,
			},
		), nil
	}

	result := t.orchestrator.Execute(execCtx, code, lang, sandbox.ExecuteOptions{
		Timeout:     timeout,
		WorkingDir:  sandboxStringParam(params, "working_dir"),
		Env:         stringMapParam(params, "env"),
		Args:        stringSliceParam(params, "args"),
		Stdin:       sandboxStringParam(params, "stdin"),
		RequireAuth: true,
	})

	details := map[string]any{
		"ok":            result.Error == nil,
		"language":      lang,
		"executor":      result.Executor,
		"sandboxed":     result.Sandboxed,
		"authorized":    result.Authorized,
		"used_fallback": result.UsedFallback,
		"exit_code":     result.ExitCode,
		"duration_ms":   result.Duration.Milliseconds(),
		"needs_sandbox": true,
		"working_dir":   sandboxStringParam(params, "working_dir"),
	}
	if result.Stdout != "" {
		details["stdout"] = result.Stdout
	}
	if result.Stderr != "" {
		details["stderr"] = result.Stderr
	}
	if result.Error != nil {
		details["error"] = result.Error.Error()
	}

	message := formatSandboxExecutionMessage(result)
	if result.Error != nil {
		return marshalSandboxToolResult(message, details), result.Error
	}
	return marshalSandboxToolResult(message, details), nil
}

func sandboxStringParam(params map[string]interface{}, key string) string {
	value, _ := params[key].(string)
	return strings.TrimSpace(value)
}

func sandboxDurationParam(params map[string]interface{}, key string, fallback time.Duration) time.Duration {
	raw, ok := params[key]
	if !ok || raw == nil {
		return fallback
	}

	switch v := raw.(type) {
	case int:
		if v > 0 {
			return time.Duration(v) * time.Second
		}
	case int32:
		if v > 0 {
			return time.Duration(v) * time.Second
		}
	case int64:
		if v > 0 {
			return time.Duration(v) * time.Second
		}
	case float64:
		if v > 0 {
			return time.Duration(v) * time.Second
		}
	case json.Number:
		if n, err := v.Int64(); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	case string:
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			return time.Duration(n) * time.Second
		}
	}

	return fallback
}

func stringMapParam(params map[string]any, key string) map[string]string {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case map[string]string:
		result := make(map[string]string, len(v))
		for key, value := range v {
			result[key] = value
		}
		return result
	case map[string]any:
		result := make(map[string]string, len(v))
		for key, value := range v {
			if s, ok := value.(string); ok {
				result[key] = s
			}
		}
		return result
	default:
		return nil
	}
}

func stringSliceParam(params map[string]any, key string) []string {
	raw, ok := params[key]
	if !ok || raw == nil {
		return nil
	}

	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		result := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				result = append(result, s)
			}
		}
		return result
	default:
		return nil
	}
}

func formatSandboxExecutionMessage(result sandbox.Result) string {
	var builder strings.Builder
	builder.WriteString("sandbox execution completed")
	if result.Executor != "" {
		builder.WriteString(" via ")
		builder.WriteString(result.Executor)
	}
	if result.Sandboxed {
		builder.WriteString(" (sandboxed)")
	}
	if result.UsedFallback {
		builder.WriteString(" with fallback")
	}
	builder.WriteString(fmt.Sprintf(", exit_code=%d, duration=%s", result.ExitCode, result.Duration.Truncate(time.Millisecond)))

	if result.Stdout != "" {
		builder.WriteString("\nstdout:\n")
		builder.WriteString(result.Stdout)
	}
	if result.Stderr != "" {
		builder.WriteString("\nstderr:\n")
		builder.WriteString(result.Stderr)
	}
	if result.Error != nil {
		builder.WriteString("\nerror: ")
		builder.WriteString(result.Error.Error())
	}

	return builder.String()
}

func sandboxLocalExecutionAllowed(approvals config.ApprovalsConfig) bool {
	if strings.EqualFold(strings.TrimSpace(approvals.Behavior), "auto") {
		return true
	}
	for _, allowedTool := range approvals.Allowlist {
		if strings.EqualFold(strings.TrimSpace(allowedTool), "sandbox_execute") {
			return true
		}
	}
	return false
}

func marshalSandboxToolResult(message string, details map[string]any) string {
	payload := map[string]any{
		"content": []map[string]string{{"type": "text", "text": message}},
		"details": details,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return message
	}
	return string(data)
}
