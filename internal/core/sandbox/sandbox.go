package sandbox

import "context"

// Orchestrator 负责编排沙箱执行流程。
type Orchestrator struct {
	executor SandboxExecutor
	detector Detector
	auth     Authorizer
}

// NewOrchestrator 创建沙箱编排器。
func NewOrchestrator(executor SandboxExecutor, detector Detector, auth Authorizer) *Orchestrator {
	if detector == nil {
		detector = DefaultDetector{}
	}
	if auth == nil {
		auth = NewAuthManager()
	}
	return &Orchestrator{
		executor: executor,
		detector: detector,
		auth:     auth,
	}
}

// Execute 执行代码；若未配置执行器则返回错误结果。
func (o *Orchestrator) Execute(ctx context.Context, code, lang string, opts ExecuteOptions) Result {
	if o.executor == nil {
		return Result{Error: ErrNoExecutor}
	}
	return o.executor.Execute(ctx, code, lang, opts)
}

// NeedSandbox 判断当前内容是否需要进入沙箱执行。
func (o *Orchestrator) NeedSandbox(ctx context.Context, content string) bool {
	if o.detector == nil {
		return DefaultDetector{}.NeedSandbox(ctx, content)
	}
	return o.detector.NeedSandbox(ctx, content)
}

// IsLocalExecutionAuthorized 判断当前上下文本地执行是否已授权。
func (o *Orchestrator) IsLocalExecutionAuthorized(ctx context.Context) bool {
	if o.auth == nil {
		return false
	}
	return o.auth.IsAuthorized(ctx)
}
