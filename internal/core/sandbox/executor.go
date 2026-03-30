package sandbox

import (
	"context"
	"time"
)

// ExecuteOptions 定义沙箱执行选项。
type ExecuteOptions struct {
	Timeout     time.Duration
	WorkingDir  string
	Env         map[string]string
	Args        []string
	Stdin       string
	RequireAuth bool
}

// Result 表示一次代码执行结果。
type Result struct {
	Stdout       string
	Stderr       string
	ExitCode     int
	Duration     time.Duration
	Executor     string
	Sandboxed    bool
	Authorized   bool
	UsedFallback bool
	Error        error
}

// SandboxExecutor 定义统一的沙箱执行接口。
type SandboxExecutor interface {
	Execute(ctx context.Context, code, lang string, opts ExecuteOptions) Result
}
