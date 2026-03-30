package sandbox

import (
	"context"
	"errors"
	"strings"
)

// ErrNoExecutor 表示未配置执行器。
var ErrNoExecutor = errors.New("sandbox executor is not configured")

// ErrLocalExecutionNotAuthorized 表示本地执行未授权。
var ErrLocalExecutionNotAuthorized = errors.New("local execution is not authorized")

// Detector 定义是否需要沙箱的判断接口。
type Detector interface {
	NeedSandbox(ctx context.Context, content string) bool
}

// DefaultDetector 是基础的启发式检测器。
type DefaultDetector struct{}

// NeedSandbox 根据内容中的代码/命令迹象判断是否需要沙箱。
func (DefaultDetector) NeedSandbox(_ context.Context, content string) bool {
	trimmed := strings.TrimSpace(strings.ToLower(content))
	if trimmed == "" {
		return false
	}

	indicators := []string{
		"```",
		"bash",
		"sh ",
		"shell",
		"python",
		"node",
		"go run",
		"npm ",
		"pip ",
		"execute",
		"run this",
		"运行这段",
		"执行这段",
		"命令",
	}
	for _, indicator := range indicators {
		if strings.Contains(trimmed, indicator) {
			return true
		}
	}
	return false
}
