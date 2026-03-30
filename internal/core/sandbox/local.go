package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// LocalExecutor 提供本地降级执行能力。
type LocalExecutor struct {
	auth Authorizer
}

// NewLocalExecutor 创建本地执行器。
func NewLocalExecutor(auth Authorizer) *LocalExecutor {
	if auth == nil {
		auth = NewAuthManager()
	}
	return &LocalExecutor{auth: auth}
}

// Execute 在本地执行代码；需要显式授权。
func (l *LocalExecutor) Execute(ctx context.Context, code, lang string, opts ExecuteOptions) Result {
	if opts.RequireAuth || l.auth != nil {
		if l.auth == nil || !l.auth.IsAuthorized(ctx) {
			return Result{Executor: "local", Sandboxed: false, Authorized: false, Error: ErrLocalExecutionNotAuthorized}
		}
	}

	command, args, err := buildCommand(code, lang, opts)
	if err != nil {
		return Result{Executor: "local", Sandboxed: false, Authorized: true, Error: err}
	}

	start := time.Now()
	cmd := exec.CommandContext(ctx, command, args...)
	if opts.WorkingDir != "" {
		cmd.Dir = opts.WorkingDir
	}
	if len(opts.Env) > 0 {
		env := make([]string, 0, len(opts.Env))
		for k, v := range opts.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}
		cmd.Env = append(cmd.Environ(), env...)
	}
	if opts.Stdin != "" {
		cmd.Stdin = strings.NewReader(opts.Stdin)
	}

	stdout, runErr := cmd.Output()
	result := Result{
		Stdout:     string(stdout),
		Duration:   time.Since(start),
		ExitCode:   0,
		Executor:   "local",
		Sandboxed:  false,
		Authorized: true,
	}

	if runErr != nil {
		result.Error = runErr
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			result.Stderr = string(exitErr.Stderr)
			result.ExitCode = exitErr.ExitCode()
		}
	}
	return result
}
