package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// SubagentAnnounceType 分身宣告类型
type SubagentAnnounceType string

const (
	SubagentAnnounceTypeTask SubagentAnnounceType = "subagent task"
	SubagentAnnounceTypeCron SubagentAnnounceType = "cron job"
)

// SubagentAnnounceParams 分身宣告参数
type SubagentAnnounceParams struct {
	ChildSessionKey     string
	ChildRunID          string
	RequesterSessionKey string
	RequesterOrigin     *DeliveryContext
	RequesterDisplayKey string
	Task                string
	Label               string
	StartedAt           *int64
	EndedAt             *int64
	Outcome             *SubagentRunOutcome
	Cleanup             string
	AnnounceType        SubagentAnnounceType
	TimeoutSeconds      int
}

// AnnounceCallback 宣告回调
type AnnounceCallback func(sessionKey, message string) error

// SubagentAnnouncer 分身宣告器
type SubagentAnnouncer struct {
	onAnnounce AnnounceCallback
}

// NewSubagentAnnouncer 创建分身宣告器
func NewSubagentAnnouncer(onAnnounce AnnounceCallback) *SubagentAnnouncer {
	return &SubagentAnnouncer{
		onAnnounce: onAnnounce,
	}
}

// RunAnnounceFlow 执行宣告流程
func (a *SubagentAnnouncer) RunAnnounceFlow(params *SubagentAnnounceParams) error {
	// 状态标签
	statusLabel := "finished with unknown status"
	if params.Outcome != nil {
		switch params.Outcome.Status {
		case "ok":
			statusLabel = "completed successfully"
		case "timeout":
			statusLabel = "timed out"
		case "canceled":
			statusLabel = "was canceled"
		case "error":
			statusLabel = fmt.Sprintf("failed: %s", params.Outcome.Error)
		}
	}

	// 状态 emoji
	statusEmoji := "❓"
	if params.Outcome != nil {
		switch params.Outcome.Status {
		case "ok":
			statusEmoji = "✅"
		case "timeout":
			statusEmoji = "⏱️"
		case "canceled":
			statusEmoji = "🛑"
		case "error":
			statusEmoji = "❌"
		}
	}

	// 任务标签
	taskLabel := params.Label
	if taskLabel == "" {
		taskLabel = params.Task
	}

	// 执行结果内容（优先用子 agent 的实际回复；空输出时明确告诉主 agent 可直接收口）
	findings := "(no result available)"
	completionHint := "请根据以上子任务结果，结合已有信息，向用户提供完整的汇总回复。如果还有其他子任务未完成，请继续等待；如果所有子任务已完成，请综合所有结果给出最终答案。"
	if params.Outcome != nil && strings.TrimSpace(params.Outcome.Result) != "" {
		findings = params.Outcome.Result
	} else if params.Outcome != nil && params.Outcome.Status == "ok" {
		findings = "该子任务已执行完毕，但没有返回任何输出。"
		completionHint = "该子任务没有额外结果可汇总。如果还有其他子任务未完成，请继续等待；如果所有子任务都已完成且也没有额外结果，请直接先检查一下你派发的任务是否完成，你可以继续派发agent去检查，检查结果最终给出回复。"
	} else if strings.TrimSpace(params.Task) != "" {
		findings = params.Task
	}

	// 统计信息
	statsLine := a.buildStatsLine(params)

	// 构建注入主 agent 的消息（结构化，便于主 agent 汇总）
	triggerMessage := fmt.Sprintf(
		"[子任务完成] %s %s（%s）\n\n任务描述：%s\n\n执行结果：\n%s\n\n%s\n\n%s",
		statusEmoji, taskLabel, statusLabel,
		params.Task,
		findings,
		statsLine,
		completionHint,
	)

	// 发送宣告到主 Agent（注入 follow-up 队列）
	if err := a.onAnnounce(params.RequesterSessionKey, triggerMessage); err != nil {
		logger.Error("Failed to announce subagent result",
			zap.String("run_id", params.ChildRunID),
			zap.Error(err))
		return err
	}

	logger.Info("Subagent result announced",
		zap.String("run_id", params.ChildRunID),
		zap.String("task", taskLabel),
		zap.String("status", statusLabel))

	return nil
}

// buildStatsLine 构建统计信息行
func (a *SubagentAnnouncer) buildStatsLine(params *SubagentAnnounceParams) string {
	parts := []string{}

	// 运行时间
	if params.StartedAt != nil && params.EndedAt != nil {
		runtimeMs := *params.EndedAt - *params.StartedAt
		parts = append(parts, fmt.Sprintf("runtime %s", formatDuration(runtimeMs)))
	}

	// 会话密钥
	parts = append(parts, fmt.Sprintf("sessionKey %s", params.ChildSessionKey))

	return fmt.Sprintf("Stats: %s", joinParts(parts, " • "))
}

// formatDuration 格式化持续时间
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}
	if ms < 60_000 {
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	}
	if ms < 3600_000 {
		min := ms / 60_000
		sec := (ms % 60_000) / 1000
		return fmt.Sprintf("%dm%ds", min, sec)
	}
	hour := ms / 3600_000
	min := (ms % 3600_000) / 60_000
	return fmt.Sprintf("%dh%dm", hour, min)
}

// joinParts 连接部分
func joinParts(parts []string, sep string) string {
	if len(parts) == 0 {
		return ""
	}
	result := parts[0]
	for i := 1; i < len(parts); i++ {
		result += sep + parts[i]
	}
	return result
}

// BuildSubagentSystemPrompt 构建分身系统提示词
func BuildSubagentSystemPrompt(params *SubagentSystemPromptParams) string {
	// 清理任务描述
	taskText := normalizeText(params.Task)
	if taskText == "" {
		taskText = "{{TASK_DESCRIPTION}}"
	}

	lines := []string{
		"# Subagent Context",
		"",
		"You are a **subagent** spawned by the main agent for a specific task.",
		"",
		"## Your Role",
		fmt.Sprintf("- You were created to handle: %s", taskText),
		"- Complete this task. That's your entire purpose.",
		"- You are NOT the main agent. Don't try to be.",
		"",
		"## Core Rules",
		"1. **Stay focused** - Do your assigned task, nothing else",
		"2. **Complete the task** - Your final message will be automatically reported to the main agent",
		"3. **Don't initiate** - No heartbeats, no proactive actions, no side quests",
		"4. **Be ephemeral** - You will be terminated after task completion. That's fine.",
		"5. **No pretending** - Never act as if you're the main agent or have broader authority",
		"",
		"## Output Format",
		"When complete, your final response should include:",
		"- What you accomplished or found",
		"- Any relevant details the main agent should know",
		"- Keep it concise but informative",
		"- No meta-commentary about being a subagent",
		"",
		"## What You DON'T Do",
		"- NO user conversations (that's main agent's job)",
		"- NO external messages (email, tweets, etc.) unless explicitly tasked",
		"- NO cron jobs or persistent state",
		"- NO spawning other subagents",
		"- NO system management tasks (gateway, config, updates)",
		"- NO claiming to be the main agent",
		"",
		"## Tool Policy",
		"- Some tools are intentionally denied (sessions_spawn, gateway, cron, etc.)",
		"- If you need a denied tool, that's a signal to ask the main agent for help",
		"- Focus on completing your task with available tools",
	}

	// 添加上下文信息
	if params.Label != "" {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("- Label: %s", params.Label))
	}
	if params.RequesterSessionKey != "" {
		lines = append(lines, fmt.Sprintf("- Requester session: %s", params.RequesterSessionKey))
	}
	if params.RequesterOrigin != nil && params.RequesterOrigin.Channel != "" {
		lines = append(lines, fmt.Sprintf("- Requester channel: %s", params.RequesterOrigin.Channel))
	}
	lines = append(lines, fmt.Sprintf("- Your session: %s", params.ChildSessionKey))
	lines = append(lines, "")

	return joinLines(lines)
}

// SubagentSystemPromptParams 系统提示词参数
type SubagentSystemPromptParams struct {
	RequesterSessionKey string
	RequesterOrigin     *DeliveryContext
	ChildSessionKey     string
	Label               string
	Task                string
}

// joinLines 连接行
func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	result := lines[0]
	for i := 1; i < len(lines); i++ {
		result += "\n" + lines[i]
	}
	return result
}

// normalizeText 规范化文本
func normalizeText(s string) string {
	// 移除多余空格
	inSpace := false
	var result []rune
	for _, r := range s {
		if r == ' ' || r == '\t' || r == '\n' {
			if !inSpace {
				result = append(result, ' ')
				inSpace = true
			}
		} else {
			result = append(result, r)
			inSpace = false
		}
	}
	return string(result)
}

// DefaultToolDenyList 默认拒绝的工具列表
var DefaultToolDenyList = []string{
	"sessions_spawn", // 防止嵌套创建
	"sessions_list",  // 会话管理 - 主 Agent 协调
	"sessions_history",
	"sessions_delete",
	"gateway", // 系统管理 - 分身不应操作
	"cron",    // 定时任务
}

// ResolveToolPolicy 解析工具策略
func ResolveToolPolicy(denyTools []string, allowTools []string) *ToolPolicy {
	policy := &ToolPolicy{
		Deny:  make(map[string]bool),
		Allow: make(map[string]bool),
	}

	// 先添加默认拒绝列表
	for _, tool := range DefaultToolDenyList {
		policy.Deny[tool] = true
	}

	// 添加配置的拒绝列表
	for _, tool := range denyTools {
		policy.Deny[tool] = true
	}

	// 如果有允许列表，则使用 allow-only 模式
	if len(allowTools) > 0 {
		policy.AllowOnly = true
		for _, tool := range allowTools {
			policy.Allow[tool] = true
		}
	}

	return policy
}

// ResolveConfiguredToolPolicy 仅按配置解析工具策略，不注入任何隐藏默认拒绝列表。
// 用于主 Agent 的运行时工具集过滤，保证 allow_tools / deny_tools 与最终提示词一致。
func ResolveConfiguredToolPolicy(denyTools []string, allowTools []string) *ToolPolicy {
	policy := &ToolPolicy{
		Deny:  make(map[string]bool),
		Allow: make(map[string]bool),
	}

	for _, tool := range denyTools {
		policy.Deny[tool] = true
	}

	if len(allowTools) > 0 {
		policy.AllowOnly = true
		for _, tool := range allowTools {
			policy.Allow[tool] = true
		}
	}

	return policy
}

// ToolPolicy 工具策略
type ToolPolicy struct {
	Deny      map[string]bool
	Allow     map[string]bool
	AllowOnly bool
}

// IsToolAllowed 检查工具是否被允许
func (p *ToolPolicy) IsToolAllowed(toolName string) bool {
	// 先检查拒绝列表（优先）
	if p.Deny[toolName] {
		return false
	}

	// 如果是 allow-only 模式，检查是否在允许列表中
	if p.AllowOnly {
		return p.Allow[toolName]
	}

	// 默认允许
	return true
}

// WaitForSubagentCompletion 等待分身完成
func WaitForSubagentCompletion(runID string, timeoutSeconds int, waitFunc func(string, int) (*SubagentCompletion, error)) (*SubagentCompletion, error) {
	timeout := time.Duration(timeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}

	done := make(chan *SubagentCompletion, 1)
	errChan := make(chan error, 1)

	go func() {
		result, err := waitFunc(runID, timeoutSeconds)
		if err != nil {
			errChan <- err
			return
		}
		done <- result
	}()

	select {
	case result := <-done:
		return result, nil
	case err := <-errChan:
		return nil, err
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for subagent completion")
	}
}

// SubagentCompletion 分身完成结果
type SubagentCompletion struct {
	Status    string // ok, error, timeout
	StartedAt int64
	EndedAt   int64
	Error     string
}
