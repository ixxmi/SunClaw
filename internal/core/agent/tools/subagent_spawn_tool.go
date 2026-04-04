package tools

import (
	"context"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/smallnest/goclaw/internal/core/config"
	"github.com/smallnest/goclaw/internal/core/execution"
	"github.com/smallnest/goclaw/internal/core/namespaces"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

const (
	delegatedTaskMaxRunes    = 400
	delegatedContextMaxRunes = 2400
	delegatedListMaxItems    = 8
	delegatedItemMaxRunes    = 180
)

// SubagentTypes - 分身相关类型定义（避免循环导入）

// DeliveryContext 传递上下文
type DeliveryContext struct {
	Channel   string `json:"channel,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	To        string `json:"to,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
}

// SubagentRunOutcome 分身运行结果
type SubagentRunOutcome struct {
	Status string `json:"status"` // ok, error, timeout, unknown
	Error  string `json:"error,omitempty"`
	Result string `json:"result,omitempty"`
}

// SubagentRunParams 分身运行参数
type SubagentRunParams struct {
	RunID               string
	ChildSessionKey     string
	RequesterSessionKey string
	RequesterOrigin     *DeliveryContext
	RequesterDisplayKey string
	RequesterAgentID    string
	TargetAgentID       string
	BootstrapOwnerID    string
	PlanID              string
	StepID              string
	ContinueOf          string
	Task                string
	Cleanup             string
	Label               string
	ArchiveAfterMinutes int
	RunTimeoutSeconds   int
}

// SubagentSystemPromptParams 系统提示词参数
type SubagentSystemPromptParams struct {
	RequesterSessionKey string
	RequesterOrigin     *DeliveryContext
	ChildSessionKey     string
	Label               string
	Task                string
	TargetAgentID       string
	AllowSubagentSpawn  bool
}

// BuildSubagentSystemPrompt 构建分身系统提示词
func BuildSubagentSystemPrompt(params *SubagentSystemPromptParams) string {
	taskText := normalizeText(params.Task)
	if taskText == "" {
		taskText = "{{TASK_DESCRIPTION}}"
	}

	lines := []string{
		"# Subagent Context",
		"",
		"你是当前被派发来处理单一步骤的子 Agent。",
		"",
		"## 当前职责",
		fmt.Sprintf("- 当前委派步骤：%s", taskText),
		"- 只完成当前步骤，或在任务仍然过大时完成其中最小、最清晰、可独立交付的一段。",
		"- 不要接管主 Agent 的用户沟通、全局规划或最终收束。",
		"",
		"## 执行规则",
		"1. 只读取完成当前步骤所需的最小上下文，不要无边界扩张。",
		"2. 优先产出真实执行结果，不要只给空泛建议。",
		"3. 不要把尚未完成的后续步骤描述为已完成。",
		"4. 如果当前任务仍然太大，完成最小闭环并明确剩余拆分建议。",
		"5. 你的输出必须便于父 Agent 直接汇总。",
		"",
		"## 输出格式",
		"完成时直接输出结构化结果，至少包含：",
		"- `状态`：`completed` / `partial` / `blocked`",
		"- `结果`：当前步骤完成了什么，或卡在什么地方",
		"- `关键产出`：关键改动、文件、命令、验证结果或核心结论",
		"- `验证`：实际执行过的检查；没有就写 `未验证`",
		"- `风险与下一步`：遗留风险、限制、建议后续步骤；没有就写 `无`",
		"",
		"## 上下文控制",
		"- 不要回传大段文件全文、超长日志或无筛选的命令输出。",
		"- 优先返回摘要、关键片段、文件路径、命令名和验证结论。",
		"",
		"## 不要这样做",
		"- 不要和用户直接对话",
		"- 不要伪装成主 Agent",
		"- 不要输出“已完成”“看起来没问题”这类不可汇总的空话",
	}

	if params.AllowSubagentSpawn {
		lines = append(lines,
			"",
			"## 继续派发规则",
			"- 如果你的 Agent 核心角色本身就是编排者，并且当前运行里确实存在 `sessions_spawn`，你可以继续向下派发更小、更窄的子步骤。",
			"- 只有在当前任务明显需要另一位更合适的专业 Agent 时才继续派发；不要把你自己能完成的工作继续外包。",
			"- 任何继续派发的子任务，都必须比你当前收到的任务范围更小、边界更清晰。",
		)
	} else {
		lines = append(lines,
			"",
			"## 继续派发规则",
			"- 当前步骤不应继续派发更多子 Agent。请直接完成当前步骤，或明确说明阻塞与下一步。",
		)
	}

	if params.Label != "" {
		lines = append(lines, "")
		lines = append(lines, fmt.Sprintf("- Label: %s", params.Label))
	}
	if params.TargetAgentID != "" {
		lines = append(lines, fmt.Sprintf("- Agent ID: %s", params.TargetAgentID))
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

// normalizeText 规范化文本
func normalizeText(s string) string {
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

// GenerateChildSessionKey 生成子会话密钥
func GenerateChildSessionKey(ctx context.Context, agentID string) string {
	u := uuid.New().String()
	if identity := namespaces.FromContext(ctx); identity.NamespaceKey() != "" {
		return namespaces.BuildSubagentSessionKey(identity, agentID, u)
	}
	return fmt.Sprintf("agent:%s:subagent:%s", agentID, u)
}

// GenerateRunID 生成运行ID
func GenerateRunID() string {
	return uuid.New().String()
}

// End SubagentTypes

// SubagentSpawnToolParams 分身生成工具参数
type SubagentSpawnToolParams struct {
	Task              string   `json:"task"`                          // 任务描述（必填）
	Label             string   `json:"label,omitempty"`               // 可选标签
	AgentID           string   `json:"agent_id,omitempty"`            // 目标 Agent ID
	AgentName         string   `json:"agent_name,omitempty"`          // 目标 Agent 名称/别名
	PlanID            string   `json:"plan_id,omitempty"`             // 关联计划 ID
	StepID            string   `json:"step_id,omitempty"`             // 关联步骤 ID
	Model             string   `json:"model,omitempty"`               // 模型覆盖
	Thinking          string   `json:"thinking,omitempty"`            // 思考级别
	MaxTokens         int      `json:"max_tokens,omitempty"`          // 最大输出 token
	Temperature       float64  `json:"temperature,omitempty"`         // 温度
	RunTimeoutSeconds int      `json:"run_timeout_seconds,omitempty"` // 超时时间
	Cleanup           string   `json:"cleanup,omitempty"`             // 清理策略
	Context           string   `json:"context,omitempty"`             // 最小必要上下文
	RelevantFiles     []string `json:"relevant_files,omitempty"`      // 相关文件或目录
	Constraints       []string `json:"constraints,omitempty"`         // 约束条件
	Deliverables      []string `json:"deliverables,omitempty"`        // 期望交付物
	DoneWhen          []string `json:"done_when,omitempty"`           // 完成标准
}

// SubagentSpawnResult 分身生成结果
type SubagentSpawnResult struct {
	Status              string  `json:"status"` // accepted, forbidden, error
	ChildSessionKey     string  `json:"child_session_key,omitempty"`
	RunID               string  `json:"run_id,omitempty"`
	Error               string  `json:"error,omitempty"`
	RunTimeoutSeconds   int     `json:"run_timeout_seconds,omitempty"`
	Model               string  `json:"model,omitempty"`
	Thinking            string  `json:"thinking,omitempty"`
	MaxTokens           int     `json:"max_tokens,omitempty"`
	Temperature         float64 `json:"temperature,omitempty"`
	TargetAgentID       string  `json:"target_agent_id,omitempty"`
	Warning             string  `json:"warning,omitempty"`
	ChildSystemPrompt   string  `json:"child_system_prompt,omitempty"` // 子 Agent 专属 System Prompt
	Task                string  `json:"task,omitempty"`                // 子 Agent 要执行的任务描述
	BootstrapOwnerID    string  `json:"bootstrap_owner_id,omitempty"`  // 子 Agent 继承的主 agent 认知 owner
	ParentLoopIteration int     `json:"parent_loop_iteration,omitempty"`
	PlanID              string  `json:"plan_id,omitempty"`
	StepID              string  `json:"step_id,omitempty"`
	ContinueOf          string  `json:"continue_of,omitempty"`
}

// SubagentRegistryInterface 分身注册表接口
type SubagentRegistryInterface interface {
	RegisterRun(params *SubagentRunParams) error
}

// SubagentSpawnTool 分身生成工具
type SubagentSpawnTool struct {
	registry         SubagentRegistryInterface
	getAgentConfig   func(agentID string) *config.AgentConfig
	listAgentConfigs func() []config.AgentConfig
	getDefaultConfig func() *config.AgentDefaults
	getAgentID       func(sessionKey string) string
	onSpawn          func(spawnParams *SubagentSpawnResult) error
}

// NewSubagentSpawnTool 创建分身生成工具
func NewSubagentSpawnTool(registry SubagentRegistryInterface) *SubagentSpawnTool {
	return &SubagentSpawnTool{
		registry: registry,
	}
}

// SetAgentConfigGetter 设置 Agent 配置获取器
func (t *SubagentSpawnTool) SetAgentConfigGetter(getter func(agentID string) *config.AgentConfig) {
	t.getAgentConfig = getter
}

// SetDefaultConfigGetter 设置默认配置获取器
func (t *SubagentSpawnTool) SetDefaultConfigGetter(getter func() *config.AgentDefaults) {
	t.getDefaultConfig = getter
}

// SetAgentConfigsGetter 设置 Agent 配置列表获取器
func (t *SubagentSpawnTool) SetAgentConfigsGetter(getter func() []config.AgentConfig) {
	t.listAgentConfigs = getter
}

// SetAgentIDGetter 设置 Agent ID 获取器
func (t *SubagentSpawnTool) SetAgentIDGetter(getter func(sessionKey string) string) {
	t.getAgentID = getter
}

// SetOnSpawn 设置分身生成回调
func (t *SubagentSpawnTool) SetOnSpawn(fn func(spawnParams *SubagentSpawnResult) error) {
	t.onSpawn = fn
}

// Name 返回工具名称
func (t *SubagentSpawnTool) Name() string {
	return "sessions_spawn"
}

// Description 返回工具描述
func (t *SubagentSpawnTool) Description() string {
	return "Spawn one tracked background sub-agent task in an isolated session and announce the result back to the requester chat."
}

// Parameters 返回工具参数定义
func (t *SubagentSpawnTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task": map[string]interface{}{
				"type":        "string",
				"description": "One-sentence current step goal for the sub-agent. Keep it to the current delegated step only.",
			},
			"label": map[string]interface{}{
				"type":        "string",
				"description": "Optional short step label, such as read-code, implement-api, run-tests, fix-regression.",
			},
			"agent_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional target agent ID. If omitted, the tool may resolve a target from agent_name or names mentioned in the task/context.",
			},
			"agent_name": map[string]interface{}{
				"type":        "string",
				"description": "Optional target agent display name, identity name, or role alias such as Reviewer, Coder, inspector, reviewer.",
			},
			"plan_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional plan ID that this delegated step belongs to.",
			},
			"step_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional plan step ID that this delegated step belongs to.",
			},
			"model": map[string]interface{}{
				"type":        "string",
				"description": "Optional per-run model override for the sub-agent.",
			},
			"thinking": map[string]interface{}{
				"type":        "string",
				"description": "Optional thinking level override (off, minimal, low, medium, high, xhigh).",
			},
			"max_tokens": map[string]interface{}{
				"type":        "integer",
				"description": "Optional max output tokens override for the sub-agent run.",
			},
			"temperature": map[string]interface{}{
				"type":        "number",
				"description": "Optional temperature override for the sub-agent run.",
			},
			"run_timeout_seconds": map[string]interface{}{
				"type":        "integer",
				"description": "Optional timeout in seconds for the sub-agent run.",
			},
			"cleanup": map[string]interface{}{
				"type":        "string",
				"description": "Cleanup strategy: 'delete' to remove immediately, 'keep' to archive after timeout.",
				"enum":        []string{"delete", "keep"},
			},
			"context": map[string]interface{}{
				"type":        "string",
				"description": "Optional minimal context the child needs for this step only. Do not dump the whole project history here.",
			},
			"relevant_files": map[string]interface{}{
				"type":        "array",
				"description": "Optional shortlist of relevant files or directories for this step.",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"constraints": map[string]interface{}{
				"type":        "array",
				"description": "Optional non-negotiable boundaries or constraints for this step.",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"deliverables": map[string]interface{}{
				"type":        "array",
				"description": "Optional required outputs for this step, such as files changed, commands run, or facts to return.",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"done_when": map[string]interface{}{
				"type":        "array",
				"description": "Optional concrete completion criteria for this step.",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
		},
		"required": []string{"task"},
	}
}

// Execute 执行工具
func (t *SubagentSpawnTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	// 解析参数
	spawnParams, err := t.parseParams(params)
	if err != nil {
		result := &SubagentSpawnResult{
			Status: "error",
			Error:  err.Error(),
		}
		return t.marshalResult(result), nil
	}

	// 验证任务不为空
	if strings.TrimSpace(spawnParams.Task) == "" {
		result := &SubagentSpawnResult{
			Status: "error",
			Error:  "task is required",
		}
		return t.marshalResult(result), nil
	}

	// 规范化清理策略
	if spawnParams.Cleanup != "delete" && spawnParams.Cleanup != "keep" {
		spawnParams.Cleanup = "keep"
	}
	delegatedTask := buildDelegatedTask(spawnParams)

	// Get requester session info from context
	requesterSessionKey := execution.SessionKey(ctx)
	if requesterSessionKey == "" {
		requesterSessionKey = "main"
	}

	// 优先从 context 直接取 agent_id（orchestrator 已注入）
	requesterAgentID := execution.AgentID(ctx)
	// fallback: 通过 session key 查找
	if requesterAgentID == "" && t.getAgentID != nil {
		requesterAgentID = t.getAgentID(requesterSessionKey)
	}
	if requesterAgentID == "" {
		requesterAgentID = "default"
	}
	bootstrapOwnerID := requesterAgentID
	if ownerID := execution.BootstrapOwnerID(ctx); strings.TrimSpace(ownerID) != "" {
		bootstrapOwnerID = ownerID
	}

	logger.Info("sessions_spawn called",
		zap.String("requester_agent_id", requesterAgentID),
		zap.String("bootstrap_owner_id", bootstrapOwnerID),
		zap.String("requester_session_key", requesterSessionKey),
		zap.String("target_agent_id_param", spawnParams.AgentID),
		zap.String("target_agent_name_param", spawnParams.AgentName),
		zap.String("task_preview", normalizeText(delegatedTask)))

	// 确定目标 Agent ID
	targetAgentID := requesterAgentID
	resolvedTargetAgentID, err := t.resolveTargetAgentID(requesterAgentID, spawnParams)
	if err != nil {
		result := &SubagentSpawnResult{
			Status: "forbidden",
			Error:  err.Error(),
		}
		return t.marshalResult(result), nil
	}
	if strings.TrimSpace(resolvedTargetAgentID) != "" {
		targetAgentID = resolvedTargetAgentID
	}
	if targetAgentID != requesterAgentID {
		// 跨 Agent 派发时，子 agent 的认知文件应来自目标 Agent 本身。
		bootstrapOwnerID = targetAgentID
	}

	// 验证跨 Agent 创建权限
	if targetAgentID != requesterAgentID {
		if !t.checkCrossAgentPermission(requesterAgentID, targetAgentID) {
			result := &SubagentSpawnResult{
				Status: "forbidden",
				Error:  fmt.Sprintf("agentId %s is not allowed for sessions_spawn", targetAgentID),
			}
			return t.marshalResult(result), nil
		}
	}

	// 解析请求者来源
	requesterOrigin := &DeliveryContext{
		Channel:   "cli", // 默认值
		AccountID: "default",
	}
	if ch := execution.Channel(ctx); strings.TrimSpace(ch) != "" {
		requesterOrigin.Channel = ch
	}
	if aid := execution.AccountID(ctx); strings.TrimSpace(aid) != "" {
		requesterOrigin.AccountID = aid
	}
	if chatID := execution.ChatID(ctx); strings.TrimSpace(chatID) != "" {
		requesterOrigin.To = chatID
	}
	if tid := execution.ThreadID(ctx); strings.TrimSpace(tid) != "" {
		requesterOrigin.ThreadID = tid
	}

	// 生成子会话密钥
	childSessionKey := GenerateChildSessionKey(ctx, targetAgentID)

	// 生成运行 ID
	runID := GenerateRunID()

	// 构建分身系统提示词，并传递给分身实例
	childSystemPrompt := BuildSubagentSystemPrompt(&SubagentSystemPromptParams{
		RequesterSessionKey: requesterSessionKey,
		RequesterOrigin:     requesterOrigin,
		ChildSessionKey:     childSessionKey,
		Label:               spawnParams.Label,
		Task:                delegatedTask,
		TargetAgentID:       targetAgentID,
		AllowSubagentSpawn:  t.canTargetAgentSpawn(targetAgentID),
	})

	// 获取归档时间
	archiveAfterMinutes := 60 // 默认值
	if t.getDefaultConfig != nil {
		if defCfg := t.getDefaultConfig(); defCfg != nil && defCfg.Subagents != nil {
			if defCfg.Subagents.ArchiveAfterMinutes > 0 {
				archiveAfterMinutes = defCfg.Subagents.ArchiveAfterMinutes
			}
		}
	}

	// 注册分身运行
	if err := t.registry.RegisterRun(&SubagentRunParams{
		RunID:               runID,
		ChildSessionKey:     childSessionKey,
		RequesterSessionKey: requesterSessionKey,
		RequesterOrigin:     requesterOrigin,
		RequesterDisplayKey: requesterSessionKey,
		RequesterAgentID:    requesterAgentID,
		TargetAgentID:       targetAgentID,
		BootstrapOwnerID:    bootstrapOwnerID,
		PlanID:              strings.TrimSpace(spawnParams.PlanID),
		StepID:              strings.TrimSpace(spawnParams.StepID),
		Task:                delegatedTask,
		Cleanup:             spawnParams.Cleanup,
		Label:               spawnParams.Label,
		ArchiveAfterMinutes: archiveAfterMinutes,
		RunTimeoutSeconds:   spawnParams.RunTimeoutSeconds,
	}); err != nil {
		result := &SubagentSpawnResult{
			Status: "error",
			Error:  fmt.Sprintf("failed to register subagent: %v", err),
		}
		return t.marshalResult(result), nil
	}

	// 调用生成回调，传递完整的子 Agent 启动参数（含 System Prompt 和 Task）
	parentLoopIteration := execution.LoopIteration(ctx)
	if t.onSpawn != nil {
		spawnResult := &SubagentSpawnResult{
			Status:              "accepted",
			ChildSessionKey:     childSessionKey,
			RunID:               runID,
			RunTimeoutSeconds:   spawnParams.RunTimeoutSeconds,
			Model:               spawnParams.Model,
			Thinking:            spawnParams.Thinking,
			MaxTokens:           spawnParams.MaxTokens,
			Temperature:         spawnParams.Temperature,
			TargetAgentID:       targetAgentID,
			ChildSystemPrompt:   childSystemPrompt,
			Task:                delegatedTask,
			BootstrapOwnerID:    bootstrapOwnerID,
			ParentLoopIteration: parentLoopIteration,
			PlanID:              strings.TrimSpace(spawnParams.PlanID),
			StepID:              strings.TrimSpace(spawnParams.StepID),
		}
		if err := t.onSpawn(spawnResult); err != nil {
			logger.Error("Failed to handle subagent spawn",
				zap.String("run_id", runID),
				zap.Error(err))
			return fmt.Sprintf("Error: failed to start subagent run: %v", err), nil
		}
	}

	// 构建结果
	result := &SubagentSpawnResult{
		Status:              "accepted",
		ChildSessionKey:     childSessionKey,
		RunID:               runID,
		RunTimeoutSeconds:   spawnParams.RunTimeoutSeconds,
		Model:               spawnParams.Model,
		Thinking:            spawnParams.Thinking,
		MaxTokens:           spawnParams.MaxTokens,
		Temperature:         spawnParams.Temperature,
		TargetAgentID:       targetAgentID,
		ChildSystemPrompt:   childSystemPrompt,
		Task:                delegatedTask,
		BootstrapOwnerID:    bootstrapOwnerID,
		ParentLoopIteration: parentLoopIteration,
		PlanID:              strings.TrimSpace(spawnParams.PlanID),
		StepID:              strings.TrimSpace(spawnParams.StepID),
	}

	logger.Debug("Subagent spawned",
		zap.String("run_id", runID),
		zap.String("task", delegatedTask),
		zap.String("child_session_key", childSessionKey),
		zap.String("target_agent_id", targetAgentID))

	return t.marshalResult(result), nil
}

// parseParams 解析参数
func (t *SubagentSpawnTool) parseParams(params map[string]interface{}) (*SubagentSpawnToolParams, error) {
	result := &SubagentSpawnToolParams{
		Cleanup: "keep",
	}

	// 解析 task
	if val, ok := params["task"]; ok {
		if str, ok := val.(string); ok {
			result.Task = str
		}
	}

	// 解析 label
	if val, ok := params["label"]; ok {
		if str, ok := val.(string); ok {
			result.Label = str
		}
	}

	// 解析 agent_id
	if val, ok := params["agent_id"]; ok {
		if str, ok := val.(string); ok {
			result.AgentID = str
		}
	}

	// 解析 agent_name
	if val, ok := params["agent_name"]; ok {
		if str, ok := val.(string); ok {
			result.AgentName = str
		}
	}

	// 解析 plan_id
	if val, ok := params["plan_id"]; ok {
		if str, ok := val.(string); ok {
			result.PlanID = str
		}
	}

	// 解析 step_id
	if val, ok := params["step_id"]; ok {
		if str, ok := val.(string); ok {
			result.StepID = str
		}
	}

	// 解析 model
	if val, ok := params["model"]; ok {
		if str, ok := val.(string); ok {
			result.Model = str
		}
	}

	// 解析 thinking
	if val, ok := params["thinking"]; ok {
		if str, ok := val.(string); ok {
			result.Thinking = str
		}
	}

	// 解析 max_tokens
	if val, ok := params["max_tokens"]; ok {
		switch v := val.(type) {
		case float64:
			result.MaxTokens = int(v)
		case int:
			result.MaxTokens = v
		}
	}

	// 解析 temperature
	if val, ok := params["temperature"]; ok {
		switch v := val.(type) {
		case float64:
			result.Temperature = v
		case int:
			result.Temperature = float64(v)
		}
	}

	// 解析 run_timeout_seconds
	if val, ok := params["run_timeout_seconds"]; ok {
		switch v := val.(type) {
		case float64:
			result.RunTimeoutSeconds = int(v)
		case int:
			result.RunTimeoutSeconds = v
		}
	}

	// 解析 cleanup
	if val, ok := params["cleanup"]; ok {
		if str, ok := val.(string); ok {
			result.Cleanup = str
		}
	}

	// 解析 context
	if val, ok := params["context"]; ok {
		if str, ok := val.(string); ok {
			result.Context = str
		}
	}

	result.RelevantFiles = parseStringSlice(params["relevant_files"])
	result.Constraints = parseStringSlice(params["constraints"])
	result.Deliverables = parseStringSlice(params["deliverables"])
	result.DoneWhen = parseStringSlice(params["done_when"])

	return result, nil
}

// marshalResult 序列化结果
func (t *SubagentSpawnTool) marshalResult(result *SubagentSpawnResult) string {
	// 简化输出
	switch result.Status {
	case "accepted":
		target := result.TargetAgentID
		if strings.TrimSpace(target) == "" {
			target = "(current agent)"
		}
		return fmt.Sprintf("Subagent spawned successfully. Agent: %s. Run ID: %s, Session: %s",
			target, result.RunID, result.ChildSessionKey)
	case "forbidden":
		return fmt.Sprintf("Forbidden: %s", result.Error)
	case "error":
		return fmt.Sprintf("Error: %s", result.Error)
	default:
		return fmt.Sprintf("Unknown status: %s", result.Status)
	}
}

// checkCrossAgentPermission 检查跨 Agent 创建权限。
// 规则：
//   - 未配置 allow_agents（nil 或空）→ 放行所有，由 LLM 根据描述自行决策
//   - 配置了 allow_agents → 只允许白名单内的 agent_id，"*" 表示允许所有
func (t *SubagentSpawnTool) checkCrossAgentPermission(requesterID, targetID string) bool {
	if t.getAgentConfig == nil {
		return true // 无法获取配置时默认放行
	}

	agentCfg := t.getAgentConfig(requesterID)
	if agentCfg == nil {
		return true // 找不到配置时默认放行
	}

	// 未配置 subagents 或 allow_agents 为空 → 自动发现模式，全部放行
	if agentCfg.Subagents == nil || len(agentCfg.Subagents.AllowAgents) == 0 {
		return true
	}

	// 配置了 allow_agents → 严格白名单校验
	for _, agent := range agentCfg.Subagents.AllowAgents {
		agent = strings.TrimSpace(agent)
		if agent == "*" {
			return true
		}
		if strings.EqualFold(agent, targetID) {
			return true
		}
	}

	return false
}

func (t *SubagentSpawnTool) requiresExplicitAgentID(requesterID string) bool {
	if t.getAgentConfig == nil {
		return false
	}

	agentCfg := t.getAgentConfig(requesterID)
	if agentCfg == nil || agentCfg.Subagents == nil {
		return false
	}

	return len(agentCfg.Subagents.AllowAgents) > 0
}

func (t *SubagentSpawnTool) resolveTargetAgentID(requesterID string, params *SubagentSpawnToolParams) (string, error) {
	if params == nil {
		return requesterID, nil
	}

	if ref := strings.TrimSpace(params.AgentID); ref != "" {
		return t.resolveAgentReference(requesterID, ref)
	}
	if ref := strings.TrimSpace(params.AgentName); ref != "" {
		return t.resolveAgentReference(requesterID, ref)
	}

	inferred := t.inferTargetAgentFromTask(requesterID, params)
	if strings.TrimSpace(inferred) != "" {
		return inferred, nil
	}

	if t.requiresExplicitAgentID(requesterID) {
		return "", fmt.Errorf("sessions_spawn requires a target agent selection for this agent; provide agent_name or mention a unique subagent name in the task")
	}

	return requesterID, nil
}

func (t *SubagentSpawnTool) resolveAgentReference(requesterID, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("empty agent reference")
	}

	candidates := t.allowedTargetAgentConfigs(requesterID)
	if len(candidates) == 0 {
		candidates = t.allAgentConfigs()
	}

	matches := make([]config.AgentConfig, 0, 2)
	needle := normalizeAgentReference(ref)
	for _, cfg := range candidates {
		if agentConfigMatchesRef(cfg, needle) {
			matches = append(matches, cfg)
		}
	}

	if len(matches) == 1 {
		return matches[0].ID, nil
	}
	if len(matches) > 1 {
		return "", fmt.Errorf("agent reference %q is ambiguous; please use a more specific agent_name or agent_id", ref)
	}

	if t.getAgentConfig != nil {
		if cfg := t.getAgentConfig(ref); cfg != nil {
			return cfg.ID, nil
		}
	}

	return "", fmt.Errorf("agent reference %q not found", ref)
}

func (t *SubagentSpawnTool) inferTargetAgentFromTask(requesterID string, params *SubagentSpawnToolParams) string {
	candidates := t.allowedTargetAgentConfigs(requesterID)
	if len(candidates) == 0 {
		return ""
	}
	if len(candidates) == 1 {
		return candidates[0].ID
	}

	haystack := normalizeAgentReference(strings.Join([]string{
		strings.TrimSpace(params.Task),
		strings.TrimSpace(params.Label),
		strings.TrimSpace(params.Context),
	}, " "))
	if haystack == "" {
		return ""
	}

	bestID := ""
	bestScore := 0
	tied := false
	for _, cfg := range candidates {
		score := scoreAgentReferenceMatch(cfg, haystack)
		if score == 0 {
			continue
		}
		if score > bestScore {
			bestID = cfg.ID
			bestScore = score
			tied = false
			continue
		}
		if score == bestScore {
			tied = true
		}
	}

	if bestID == "" || tied {
		return ""
	}
	return bestID
}

func (t *SubagentSpawnTool) allowedTargetAgentConfigs(requesterID string) []config.AgentConfig {
	all := t.allAgentConfigs()
	if len(all) == 0 {
		return nil
	}

	if t.getAgentConfig == nil {
		return all
	}
	requesterCfg := t.getAgentConfig(requesterID)
	if requesterCfg == nil || requesterCfg.Subagents == nil || len(requesterCfg.Subagents.AllowAgents) == 0 {
		return filterNonSelfConfigs(all, requesterID)
	}

	allowedSet := make(map[string]struct{}, len(requesterCfg.Subagents.AllowAgents))
	allowAll := false
	for _, id := range requesterCfg.Subagents.AllowAgents {
		id = strings.TrimSpace(id)
		if id == "*" {
			allowAll = true
			break
		}
		if id != "" {
			allowedSet[strings.ToLower(id)] = struct{}{}
		}
	}

	result := make([]config.AgentConfig, 0, len(all))
	for _, cfg := range all {
		if strings.EqualFold(cfg.ID, requesterID) {
			continue
		}
		if allowAll {
			result = append(result, cfg)
			continue
		}
		if _, ok := allowedSet[strings.ToLower(strings.TrimSpace(cfg.ID))]; ok {
			result = append(result, cfg)
		}
	}
	return result
}

func (t *SubagentSpawnTool) allAgentConfigs() []config.AgentConfig {
	if t.listAgentConfigs == nil {
		return nil
	}
	configs := t.listAgentConfigs()
	out := make([]config.AgentConfig, 0, len(configs))
	for _, cfg := range configs {
		if strings.TrimSpace(cfg.ID) == "" {
			continue
		}
		out = append(out, cfg)
	}
	return out
}

func filterNonSelfConfigs(configs []config.AgentConfig, selfID string) []config.AgentConfig {
	out := make([]config.AgentConfig, 0, len(configs))
	for _, cfg := range configs {
		if strings.EqualFold(strings.TrimSpace(cfg.ID), strings.TrimSpace(selfID)) {
			continue
		}
		out = append(out, cfg)
	}
	return out
}

func normalizeAgentReference(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func agentConfigMatchesRef(cfg config.AgentConfig, needle string) bool {
	if needle == "" {
		return false
	}
	for _, alias := range agentConfigAliases(cfg) {
		if normalizeAgentReference(alias) == needle {
			return true
		}
	}
	return false
}

func scoreAgentReferenceMatch(cfg config.AgentConfig, haystack string) int {
	score := 0
	for _, alias := range agentConfigAliases(cfg) {
		alias = normalizeAgentReference(alias)
		if alias == "" {
			continue
		}
		if len(alias) < 3 && alias != strings.ToLower(strings.TrimSpace(cfg.ID)) {
			continue
		}
		if strings.Contains(haystack, alias) {
			if len(alias) > score {
				score = len(alias)
			}
		}
	}
	return score
}

func agentConfigAliases(cfg config.AgentConfig) []string {
	aliases := []string{cfg.ID, cfg.Name}
	if cfg.Identity != nil {
		aliases = append(aliases, cfg.Identity.Name)
	}
	if role, ok := cfg.Metadata["role"].(string); ok {
		aliases = append(aliases, role)
	}
	out := make([]string, 0, len(aliases))
	seen := make(map[string]struct{}, len(aliases))
	for _, alias := range aliases {
		alias = strings.TrimSpace(alias)
		if alias == "" {
			continue
		}
		key := normalizeAgentReference(alias)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, alias)
	}
	return out
}

func (t *SubagentSpawnTool) canTargetAgentSpawn(agentID string) bool {
	if t.getAgentConfig == nil {
		return false
	}

	agentCfg := t.getAgentConfig(agentID)
	if agentCfg == nil {
		return false
	}

	if agentCfg.Subagents == nil {
		return true
	}

	if len(agentCfg.Subagents.AllowTools) > 0 {
		for _, tool := range agentCfg.Subagents.AllowTools {
			if strings.EqualFold(strings.TrimSpace(tool), "sessions_spawn") {
				return true
			}
		}
		return false
	}

	for _, tool := range agentCfg.Subagents.DenyTools {
		if strings.EqualFold(strings.TrimSpace(tool), "sessions_spawn") {
			return false
		}
	}

	return true
}

func buildDelegatedTask(params *SubagentSpawnToolParams) string {
	if params == nil {
		return ""
	}

	baseTask := limitText(strings.TrimSpace(params.Task), delegatedTaskMaxRunes)
	if !hasStructuredDelegationFields(params) {
		return baseTask
	}

	sections := make([]string, 0, 6)
	if baseTask != "" {
		sections = append(sections, "## 当前步骤目标\n"+baseTask)
	}
	if ctx := strings.TrimSpace(params.Context); ctx != "" {
		sections = append(sections, "## 必要上下文\n"+limitText(ctx, delegatedContextMaxRunes))
	}
	if files := formatBulletListLimited(params.RelevantFiles, delegatedListMaxItems, delegatedItemMaxRunes); files != "" {
		sections = append(sections, "## 相关文件\n"+files)
	}
	if constraints := formatBulletListLimited(params.Constraints, delegatedListMaxItems, delegatedItemMaxRunes); constraints != "" {
		sections = append(sections, "## 约束条件\n"+constraints)
	}
	if deliverables := formatBulletListLimited(params.Deliverables, delegatedListMaxItems, delegatedItemMaxRunes); deliverables != "" {
		sections = append(sections, "## 期望产出\n"+deliverables)
	}
	if doneWhen := formatBulletListLimited(params.DoneWhen, delegatedListMaxItems, delegatedItemMaxRunes); doneWhen != "" {
		sections = append(sections, "## 完成标准\n"+doneWhen)
	}

	return strings.TrimSpace(strings.Join(sections, "\n\n"))
}

func hasStructuredDelegationFields(params *SubagentSpawnToolParams) bool {
	if params == nil {
		return false
	}
	return strings.TrimSpace(params.Context) != "" ||
		len(params.RelevantFiles) > 0 ||
		len(params.Constraints) > 0 ||
		len(params.Deliverables) > 0 ||
		len(params.DoneWhen) > 0
}

func parseStringSlice(raw interface{}) []string {
	switch value := raw.(type) {
	case nil:
		return nil
	case []string:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if trimmed := strings.TrimSpace(item); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	case []interface{}:
		out := make([]string, 0, len(value))
		for _, item := range value {
			if str, ok := item.(string); ok {
				if trimmed := strings.TrimSpace(str); trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}
		return out
	case string:
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return []string{trimmed}
		}
	}
	return nil
}

func formatBulletList(items []string) string {
	return formatBulletListLimited(items, len(items), delegatedItemMaxRunes)
}

func formatBulletListLimited(items []string, maxItems, maxRunes int) string {
	if len(items) == 0 {
		return ""
	}
	if maxItems <= 0 {
		maxItems = len(items)
	}
	lines := make([]string, 0, minSubagentInt(len(items), maxItems)+1)
	count := 0
	for _, item := range items {
		if trimmed := strings.TrimSpace(item); trimmed != "" {
			lines = append(lines, "- "+limitText(trimmed, maxRunes))
			count++
			if count >= maxItems {
				break
			}
		}
	}
	if len(items) > count {
		lines = append(lines, fmt.Sprintf("- ... 省略其余 %d 项", len(items)-count))
	}
	return strings.Join(lines, "\n")
}

func limitText(text string, maxRunes int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxRunes <= 0 {
		return text
	}
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	runes := []rune(text)
	return strings.TrimSpace(string(runes[:maxRunes])) + "...(truncated)"
}

func minSubagentInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
