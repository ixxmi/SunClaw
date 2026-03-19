package agent

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/smallnest/goclaw/acp"
	acpruntime "github.com/smallnest/goclaw/acp/runtime"
	"github.com/smallnest/goclaw/agent/tools"
	"github.com/smallnest/goclaw/bus"
	"github.com/smallnest/goclaw/channels"
	"github.com/smallnest/goclaw/config"
	"github.com/smallnest/goclaw/internal/logger"
	"github.com/smallnest/goclaw/providers"
	"github.com/smallnest/goclaw/session"
	"go.uber.org/zap"
)

var cronJobIDPattern = regexp.MustCompile(`\bjob-[a-zA-Z0-9]+\b`)
var cronListLinePattern = regexp.MustCompile(`^(job-[a-zA-Z0-9]+)\s+\((enabled|disabled)\)$`)

// AgentManager 管理多个 Agent 实例
type AgentManager struct {
	agents         map[string]*Agent        // agentID -> Agent
	bindings       map[string]*BindingEntry // channel:accountID -> BindingEntry
	defaultAgent   *Agent                   // 默认 Agent
	defaultAgentID string                   // 默认 Agent ID
	bus            *bus.MessageBus
	sessionMgr     *session.Manager
	provider       providers.Provider
	tools          *ToolRegistry
	mu             sync.RWMutex
	cfg            *config.Config
	contextBuilder *ContextBuilder
	skillsLoader   *SkillsLoader
	helper         *AgentHelper
	channelMgr     *channels.Manager
	acpManager     *acp.Manager
	manualCronMu   sync.Mutex
	manualCronLast map[string]time.Time
	// 分身支持
	subagentRegistry  *SubagentRegistry
	subagentAnnouncer *SubagentAnnouncer
	dataDir           string
	// 会话级 Agent 路由（支持运行时切换主 Agent）
	sessionRouter *SessionAgentRouter

	// per-session follow-up 注入队列：子 agent 完成后直接 push，主 agent runLoop 消费
	// key = requester session key（主 agent 的会话键），value = 待注入消息列表
	followUpQueues   map[string][]AgentMessage
	followUpQueuesMu sync.Mutex
}

// BindingEntry Agent 绑定条目
type BindingEntry struct {
	AgentID   string
	Channel   string
	AccountID string
	Agent     *Agent
}

// NewAgentManagerConfig AgentManager 配置
type NewAgentManagerConfig struct {
	Bus            *bus.MessageBus
	Provider       providers.Provider
	SessionMgr     *session.Manager
	Tools          *ToolRegistry
	DataDir        string          // 数据目录，用于存储分身注册表
	ContextBuilder *ContextBuilder // 上下文构建器
	SkillsLoader   *SkillsLoader   // 技能加载器
	ChannelMgr     *channels.Manager
	AcpManager     *acp.Manager
}

// NewAgentManager 创建 Agent 管理器
func NewAgentManager(cfg *NewAgentManagerConfig) *AgentManager {
	// 创建分身注册表
	subagentRegistry := NewSubagentRegistry(cfg.DataDir)

	// 创建分身宣告器
	subagentAnnouncer := NewSubagentAnnouncer(nil) // 回调在 Start 中设置

	// 创建会话级 Agent 路由器
	sessionRouter := NewSessionAgentRouter(cfg.DataDir)

	return &AgentManager{
		agents:            make(map[string]*Agent),
		bindings:          make(map[string]*BindingEntry),
		bus:               cfg.Bus,
		sessionMgr:        cfg.SessionMgr,
		provider:          cfg.Provider,
		tools:             cfg.Tools,
		subagentRegistry:  subagentRegistry,
		subagentAnnouncer: subagentAnnouncer,
		dataDir:           cfg.DataDir,
		contextBuilder:    cfg.ContextBuilder,
		skillsLoader:      cfg.SkillsLoader,
		helper:            NewAgentHelper(cfg.SessionMgr),
		channelMgr:        cfg.ChannelMgr,
		acpManager:        cfg.AcpManager,
		manualCronLast:    make(map[string]time.Time),
		sessionRouter:     sessionRouter,
		followUpQueues:    make(map[string][]AgentMessage),
	}
}

// handleSubagentCompletion 处理分身完成事件
//func (m *AgentManager) handleSubagentCompletion(runID string, record *SubagentRunRecord) {
//
//	// 启动宣告流程
//	if record.Outcome != nil {
//		announceParams := &SubagentAnnounceParams{
//			ChildSessionKey:     record.ChildSessionKey,
//			ChildRunID:          record.RunID,
//			RequesterSessionKey: record.RequesterSessionKey,
//			RequesterOrigin:     record.RequesterOrigin,
//			RequesterDisplayKey: record.RequesterDisplayKey,
//			Task:                record.Task,
//			Label:               record.Label,
//			StartedAt:           record.StartedAt,
//			EndedAt:             record.EndedAt,
//			Outcome:             record.Outcome,
//			Cleanup:             record.Cleanup,
//			AnnounceType:        SubagentAnnounceTypeTask,
//		}
//
//		if err := m.subagentAnnouncer.RunAnnounceFlow(announceParams); err != nil {
//			logger.Error("Failed to announce subagent result",
//				zap.String("run_id", runID),
//				zap.Error(err))
//		}
//
//		// 标记清理完成
//		m.subagentRegistry.Cleanup(runID, record.Cleanup, true)
//	}
//}

// SetupFromConfig 从配置设置 Agent 和绑定
func (m *AgentManager) SetupFromConfig(cfg *config.Config, contextBuilder *ContextBuilder) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg = cfg
	m.contextBuilder = contextBuilder

	logger.Info("Setting up agents from config")

	// 1. 创建 Agent 实例
	for _, agentCfg := range cfg.Agents.List {
		if err := m.createAgent(agentCfg, contextBuilder, cfg); err != nil {
			logger.Error("Failed to create agent",
				zap.String("agent_id", agentCfg.ID),
				zap.Error(err))
			continue
		}
	}

	// 2. 如果没有配置 Agent，创建默认 Agent
	if len(m.agents) == 0 {
		logger.Info("No agents configured, creating default agent")
		defaultAgentCfg := config.AgentConfig{
			ID:        "default",
			Name:      "Default Agent",
			Default:   true,
			Model:     cfg.Agents.Defaults.Model,
			Workspace: cfg.Workspace.Path,
		}
		if err := m.createAgent(defaultAgentCfg, contextBuilder, cfg); err != nil {
			return fmt.Errorf("failed to create default agent: %w", err)
		}
		m.defaultAgentID = "default"
	}

	// 3. 设置绑定
	for _, binding := range cfg.Bindings {
		if err := m.setupBinding(binding); err != nil {
			logger.Error("Failed to setup binding",
				zap.String("agent_id", binding.AgentID),
				zap.String("channel", binding.Match.Channel),
				zap.String("account_id", binding.Match.AccountID),
				zap.Error(err))
		}
	}

	// 打印所有 agent 初始化汇总
	logger.Info("━━━━━━━━━━ Agent Initialization Summary ━━━━━━━━━━")
	for _, agentCfg := range cfg.Agents.List {
		if _, ok := m.agents[agentCfg.ID]; !ok {
			logger.Warn("  ✗ Agent FAILED",
				zap.String("id", agentCfg.ID))
			continue
		}
		profile := agentCfg.Provider
		if profile == "" {
			profile = "(global)"
		}
		model := agentCfg.Model
		baseURL := ""
		for _, prof := range cfg.Providers.Profiles {
			if prof.Name == agentCfg.Provider {
				if model == "" {
					model = prof.Model
				}
				baseURL = prof.BaseURL
				break
			}
		}
		if model == "" {
			model = cfg.Agents.Defaults.Model
		}
		defaultMark := ""
		if agentCfg.Default {
			defaultMark = " [default]"
		}
		logger.Info(fmt.Sprintf("  ✓ %-12s profile=%-8s model=%-20s url=%s%s",
			agentCfg.ID, profile, model, baseURL, defaultMark))
	}
	logger.Info("━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━")

	// 4. 为配置了 allow_agents 的 Agent 动态注入可派生 Agent 目录
	m.injectSpawnableAgentDescriptions(cfg)

	// 5. 设置分身支持
	m.setupSubagentSupport(cfg, contextBuilder)

	// 更新所有已创建 Agent 的工具列表。
	// - 配置了 allow_tools → 只保留白名单内的工具（子 agent 专属能力）
	// - 未配置 allow_tools → 全部工具，LLM 自行按需选择
	allTools := m.tools.ListExisting()
	agentCfgMap := make(map[string]config.AgentConfig, len(cfg.Agents.List))
	for _, agentCfg := range cfg.Agents.List {
		agentCfgMap[agentCfg.ID] = agentCfg
	}
	for agentID, agent := range m.agents {
		agentCfg := agentCfgMap[agentID]
		var filtered []Tool
		if agentCfg.Subagents != nil && len(agentCfg.Subagents.AllowTools) > 0 {
			// 有 allow_tools：严格白名单，只保留列出的工具
			policy := ResolveToolPolicy(agentCfg.Subagents.DenyTools, agentCfg.Subagents.AllowTools)
			for _, t := range ToAgentTools(allTools) {
				if policy.IsToolAllowed(t.Name()) {
					filtered = append(filtered, t)
				}
			}
			logger.Info("Agent tools filtered by allow_tools",
				zap.String("agent_id", agentID),
				zap.Int("allow_tools", len(agentCfg.Subagents.AllowTools)),
				zap.Int("filtered_tools", len(filtered)))
		} else if agentCfg.Subagents != nil && len(agentCfg.Subagents.DenyTools) > 0 {
			// 只有 deny_tools：排除黑名单
			policy := ResolveToolPolicy(agentCfg.Subagents.DenyTools, nil)
			for _, t := range ToAgentTools(allTools) {
				if policy.IsToolAllowed(t.Name()) {
					filtered = append(filtered, t)
				}
			}
			logger.Info("Agent tools filtered by deny_tools",
				zap.String("agent_id", agentID),
				zap.Int("deny_tools", len(agentCfg.Subagents.DenyTools)),
				zap.Int("filtered_tools", len(filtered)))
		} else {
			// 未配置限制：全部工具
			filtered = ToAgentTools(allTools)
		}
		agent.SetTools(filtered)
	}

	// 对配置了自定义 system_prompt 的 agent，将其实际可用工具列表追加到提示词末尾。
	// 此时工具已按 allow_tools 过滤完毕，追加的是真实可用集合。
	if contextBuilder != nil {
		for _, agentCfg := range cfg.Agents.List {
			if agentCfg.SystemPrompt == "" {
				continue // 未配置自定义 prompt 的 agent 使用内置提示词，工具列表已内置
			}
			a, ok := m.agents[agentCfg.ID]
			if !ok {
				continue
			}
			agentTools := a.GetTools()
			toolsSummary := contextBuilder.BuildToolsSummary(agentTools)
			if toolsSummary != "" {
				current := a.GetSystemPrompt()
				a.SetSystemPrompt(current + "\n\n---\n\n" + toolsSummary)
				logger.Info("Appended tools summary to custom system_prompt",
					zap.String("agent_id", agentCfg.ID),
					zap.Int("tools_count", len(agentTools)))
			}
		}
	}

	logger.Info("Agent manager setup complete",
		zap.Int("agents", len(m.agents)),
		zap.Int("bindings", len(m.bindings)))

	return nil
}

// setupSubagentSupport 设置分身支持
func (m *AgentManager) setupSubagentSupport(cfg *config.Config, _ *ContextBuilder) {
	// 加载分身注册表
	if err := m.subagentRegistry.LoadFromDisk(); err != nil {
		logger.Warn("Failed to load subagent registry", zap.Error(err))
	}

	// 设置分身运行完成回调
	m.subagentRegistry.SetOnRunComplete(func(runID string, record *SubagentRunRecord) {
		m.handleSubagentCompletion(runID, record)
	})

	// 更新宣告器回调
	m.subagentAnnouncer = NewSubagentAnnouncer(func(sessionKey, message string) error {
		// 发送宣告消息到指定会话
		return m.sendToSession(sessionKey, message)
	})

	// 创建分身注册表适配器
	registryAdapter := &subagentRegistryAdapter{registry: m.subagentRegistry}

	// 注册 sessions_spawn 工具
	spawnTool := tools.NewSubagentSpawnTool(registryAdapter)
	spawnTool.SetAgentConfigGetter(func(agentID string) *config.AgentConfig {
		for _, agentCfg := range cfg.Agents.List {
			if agentCfg.ID == agentID {
				return &agentCfg
			}
		}
		return nil
	})
	spawnTool.SetDefaultConfigGetter(func() *config.AgentDefaults {
		return &cfg.Agents.Defaults
	})
	spawnTool.SetAgentIDGetter(func(sessionKey string) string {
		// 1. 优先从 sessionRouter 查找（wework/telegram 等 channel 的 session key 格式为 "channel:account:chatid"）
		if agentID := m.sessionRouter.GetAgentID(sessionKey); agentID != "" {
			logger.Debug("AgentIDGetter: found via sessionRouter",
				zap.String("session_key", sessionKey),
				zap.String("agent_id", agentID))
			return agentID
		}

		// 2. 尝试从 session key 格式解析（subagent 格式 "agent:<agentId>:subagent:<uuid>"）
		if agentID, _, _ := ParseAgentSessionKey(sessionKey); agentID != "" {
			logger.Debug("AgentIDGetter: found via ParseAgentSessionKey",
				zap.String("session_key", sessionKey),
				zap.String("agent_id", agentID))
			return agentID
		}

		// 3. 从 bindings 中按 session key 前缀匹配（channel:account_id）
		for _, entry := range m.bindings {
			matchKey := entry.Channel + ":" + entry.AccountID
			if strings.HasPrefix(sessionKey, matchKey) {
				logger.Debug("AgentIDGetter: found via binding match",
					zap.String("session_key", sessionKey),
					zap.String("agent_id", entry.AgentID))
				return entry.AgentID
			}
		}

		// 4. 最终兜底：使用默认 agent ID
		fallback := m.defaultAgentID
		if fallback == "" {
			fallback = "default"
		}
		logger.Warn("AgentIDGetter: fallback to default",
			zap.String("session_key", sessionKey),
			zap.String("fallback_agent_id", fallback))
		return fallback
	})
	spawnTool.SetOnSpawn(func(result *tools.SubagentSpawnResult) error {
		return m.handleSubagentSpawn(result)
	})

	// 注册工具
	if err := m.tools.RegisterExisting(spawnTool); err != nil {
		logger.Error("Failed to register sessions_spawn tool", zap.Error(err))
	}

	logger.Info("Subagent support configured")
}

// subagentRegistryAdapter 分身注册表适配器
type subagentRegistryAdapter struct {
	registry *SubagentRegistry
}

// RegisterRun 注册分身运行
func (a *subagentRegistryAdapter) RegisterRun(params *tools.SubagentRunParams) error {
	// 转换 RequesterOrigin
	var requesterOrigin *DeliveryContext
	if params.RequesterOrigin != nil {
		requesterOrigin = &DeliveryContext{
			Channel:   params.RequesterOrigin.Channel,
			AccountID: params.RequesterOrigin.AccountID,
			To:        params.RequesterOrigin.To,
			ThreadID:  params.RequesterOrigin.ThreadID,
		}
	}

	return a.registry.RegisterRun(&SubagentRunParams{
		RunID:               params.RunID,
		ChildSessionKey:     params.ChildSessionKey,
		RequesterSessionKey: params.RequesterSessionKey,
		RequesterOrigin:     requesterOrigin,
		RequesterDisplayKey: params.RequesterDisplayKey,
		Task:                params.Task,
		Cleanup:             params.Cleanup,
		Label:               params.Label,
		ArchiveAfterMinutes: params.ArchiveAfterMinutes,
	})
}

// handleSubagentSpawn 处理分身生成：为子 Agent 创建独立 Orchestrator 并在后台异步执行
func (m *AgentManager) handleSubagentSpawn(result *tools.SubagentSpawnResult) error {
	// 解析子会话密钥，格式: agent:<agentId>:subagent:<uuid>
	agentID, subagentID, isSubagent := ParseAgentSessionKey(result.ChildSessionKey)
	if !isSubagent {
		return fmt.Errorf("invalid subagent session key: %s", result.ChildSessionKey)
	}

	// 找到目标 Agent 实例，获取其专属 provider / model
	var targetAgent *Agent
	if agentID != "" {
		var ok bool
		targetAgent, ok = m.GetAgent(agentID)
		if !ok {
			logger.Warn("Target agent not found, falling back to default",
				zap.String("agent_id", agentID))
			targetAgent = m.GetDefaultAgent()
		}
	} else {
		targetAgent = m.GetDefaultAgent()
	}
	if targetAgent == nil {
		return fmt.Errorf("no agent available for subagent: %s", result.ChildSessionKey)
	}

	logger.Info("━━ Subagent spawn ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
		zap.String("target_agent_id", agentID),
		zap.String("subagent_id", subagentID),
		zap.String("model", targetAgent.orchestrator.config.Model),
		zap.String("task_preview", truncateSubagentTask(result.Task, 80)))

	// 确定超时时间
	timeoutSeconds := 300 // 默认 5 分钟
	if m.cfg != nil && m.cfg.Agents.Defaults.Subagents != nil {
		if t := m.cfg.Agents.Defaults.Subagents.TimeoutSeconds; t > 0 {
			timeoutSeconds = t
		}
	}

	// 直接使用目标 Agent 在初始化阶段已按 allow_tools/deny_tools 过滤好的工具集，
	// 无需在此重复过滤（SetupFromConfig 中已完成一次性过滤并写入 agent.state.Tools）
	subagentTools := targetAgent.GetTools()

	logger.Info("Subagent tools resolved",
		zap.String("run_id", result.RunID),
		zap.String("subagent_id", subagentID),
		zap.Int("allowed_tools", len(subagentTools)))

	// 构建子 Agent 独立状态
	subagentState := NewAgentState()
	subagentState.AgentID = agentID
	subagentState.SessionKey = result.ChildSessionKey
	subagentState.Tools = subagentTools

	// 使用目标 Agent 完整的 system_prompt（已含工具列表摘要、可派生 Agent 目录等），
	// 再追加本次具体任务上下文，降级时才用 spawn 工具生成的通用模板
	childSystemPrompt := targetAgent.GetSystemPrompt()
	if childSystemPrompt == "" {
		childSystemPrompt = result.ChildSystemPrompt
	} else {
		childSystemPrompt = childSystemPrompt + "\n\n---\n\n## Current Task\n" + result.Task
		logger.Info("Subagent using agent system_prompt with task context",
			zap.String("agent_id", agentID),
			zap.Int("prompt_len", len(childSystemPrompt)))
	}
	subagentState.SystemPrompt = childSystemPrompt

	// 构建子 Agent 独立 LoopConfig（使用目标 Agent 的专属 provider 和 model）
	subagentLoopConfig := &LoopConfig{
		Model:          targetAgent.orchestrator.config.Model,
		Provider:       targetAgent.orchestrator.config.Provider,
		SessionMgr:     targetAgent.orchestrator.config.SessionMgr,
		MaxIterations:  targetAgent.orchestrator.config.MaxIterations,
		ConvertToLLM:   defaultConvertToLLM,
		ContextBuilder: nil,
		Skills:         nil,
	}

	// 创建独立 Orchestrator 实例（与父 Agent 完全隔离，互不干扰）
	subagentOrchestrator := NewOrchestrator(subagentLoopConfig, subagentState)

	// 构建任务消息（子 Agent 的第一条 user 消息就是被分配的任务）
	taskMsg := AgentMessage{
		Role:      RoleUser,
		Content:   []ContentBlock{TextContent{Text: result.Task}},
		Timestamp: time.Now().UnixMilli(),
	}

	// 在独立 goroutine 中异步执行，不阻塞主 Agent 的响应
	go func() {
		runCtx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSeconds)*time.Second)
		defer cancel()

		now := time.Now().UnixMilli()
		outcome := &SubagentRunOutcome{}

		logger.Info("━━ Subagent execution started ━━━━━━━━━━━━━━━━━━━━",
			zap.String("run_id", result.RunID),
			zap.String("target_agent_id", agentID),
			zap.String("model", subagentLoopConfig.Model),
			zap.String("child_session_key", result.ChildSessionKey),
			zap.String("task_preview", truncateSubagentTask(result.Task, 100)))

		finalMessages, err := subagentOrchestrator.Run(runCtx, []AgentMessage{taskMsg})
		endedAt := time.Now().UnixMilli()

		if err != nil {
			if runCtx.Err() == context.DeadlineExceeded {
				outcome.Status = "timeout"
				outcome.Error = fmt.Sprintf("subagent timed out after %ds", timeoutSeconds)
				logger.Warn("Subagent timed out",
					zap.String("run_id", result.RunID),
					zap.Int("timeout_seconds", timeoutSeconds))
			} else {
				outcome.Status = "error"
				outcome.Error = err.Error()
				logger.Error("Subagent execution failed",
					zap.String("run_id", result.RunID),
					zap.Error(err))
			}
		} else {
			outcome.Status = "ok"
			// 提取子 agent 最终回复内容，存入 outcome.Result 供 Announcer 展示给主 agent
			for i := len(finalMessages) - 1; i >= 0; i-- {
				msg := finalMessages[i]
				if msg.Role == RoleAssistant {
					if reply := extractTextContent(msg); strings.TrimSpace(reply) != "" {
						outcome.Result = reply
						break
					}
				}
			}
			logger.Info("Subagent execution completed",
				zap.String("run_id", result.RunID),
				zap.Int("messages", len(finalMessages)),
				zap.Int("result_len", len(outcome.Result)))
		}

		// 通知 SubagentRegistry 任务完成，触发结果回传主 Agent 的链路
		if err := m.subagentRegistry.MarkCompleted(result.RunID, outcome, &endedAt); err != nil {
			logger.Error("Failed to mark subagent as completed",
				zap.String("run_id", result.RunID),
				zap.Error(err))
		}

		// 保存子 Agent 会话记录（便于后续查看历史）
		if sess, sessErr := m.sessionMgr.GetOrCreate(result.ChildSessionKey); sessErr == nil {
			newMsgs := finalMessages
			if len(finalMessages) > 1 {
				newMsgs = finalMessages[1:] // 跳过第一条 taskMsg（已在 Registry 中记录）
			}
			_ = m.helper.UpdateSession(sess, newMsgs, &UpdateSessionOptions{SaveImmediately: true})
		}

		_ = now // 已通过 &endedAt 传递
	}()

	logger.Info("Subagent spawned and running in background",
		zap.String("run_id", result.RunID),
		zap.String("subagent_id", subagentID),
		zap.String("child_session_key", result.ChildSessionKey),
		zap.Int("timeout_seconds", timeoutSeconds),
		zap.Int("allowed_tools", len(subagentTools)))

	return nil
}

// truncateSubagentTask 截断任务描述用于日志输出
func truncateSubagentTask(task string, maxLen int) string {
	if len(task) <= maxLen {
		return task
	}
	return task[:maxLen] + "..."
}

// handleSubagentCompletion 由 SubagentRegistry.onRunComplete 回调触发。
// 将子 agent 的执行结果通过 subagentAnnouncer 宣告到主 agent 的 follow-up 队列，
// 主 agent runLoop 在 outerCheck 轮询时感知并继续汇总。
func (m *AgentManager) handleSubagentCompletion(runID string, record *SubagentRunRecord) {
	if record == nil {
		logger.Warn("handleSubagentCompletion: nil record", zap.String("run_id", runID))
		return
	}

	logger.Info("Handling subagent completion",
		zap.String("run_id", runID),
		zap.String("requester_session_key", record.RequesterSessionKey),
		zap.String("status", func() string {
			if record.Outcome != nil {
				return record.Outcome.Status
			}
			return "unknown"
		}()))

	if m.subagentAnnouncer == nil {
		logger.Error("subagentAnnouncer is nil, cannot announce result", zap.String("run_id", runID))
		return
	}

	if err := m.subagentAnnouncer.RunAnnounceFlow(&SubagentAnnounceParams{
		ChildSessionKey:     record.ChildSessionKey,
		ChildRunID:          runID,
		RequesterSessionKey: record.RequesterSessionKey,
		RequesterOrigin:     record.RequesterOrigin,
		RequesterDisplayKey: record.RequesterDisplayKey,
		Task:                record.Task,
		Label:               record.Label,
		StartedAt:           record.StartedAt,
		EndedAt:             record.EndedAt,
		Outcome:             record.Outcome,
		Cleanup:             record.Cleanup,
		AnnounceType:        SubagentAnnounceTypeTask,
	}); err != nil {
		logger.Error("Failed to announce subagent completion",
			zap.String("run_id", runID),
			zap.Error(err))
	}
}

// pushFollowUp 向指定 session 的主 agent 推送一条 follow-up 消息。
// 子 agent 完成后调用此方法，主 agent runLoop 在下一次 fetchFollowUpMessages 时消费。
func (m *AgentManager) pushFollowUp(requesterSessionKey string, msg AgentMessage) {
	m.followUpQueuesMu.Lock()
	defer m.followUpQueuesMu.Unlock()
	m.followUpQueues[requesterSessionKey] = append(m.followUpQueues[requesterSessionKey], msg)
	logger.Debug("pushFollowUp: queued message for requester",
		zap.String("requester_session_key", requesterSessionKey),
		zap.Int("queue_len", len(m.followUpQueues[requesterSessionKey])))
}

// popFollowUps 取出并清空指定 session 的 follow-up 消息列表。
func (m *AgentManager) popFollowUps(requesterSessionKey string) []AgentMessage {
	m.followUpQueuesMu.Lock()
	defer m.followUpQueuesMu.Unlock()
	msgs := m.followUpQueues[requesterSessionKey]
	if len(msgs) > 0 {
		delete(m.followUpQueues, requesterSessionKey)
	}
	return msgs
}

// sendToSession 将子 agent 的结果注入到主 agent 的 follow-up 队列。
// 参考 picoclaw：子 agent 完成后通过 follow-up 队列将结果送回主 agent 的 runLoop，
// 主 agent 在 fetchFollowUpMessages 时感知到，继续汇总。
func (m *AgentManager) sendToSession(sessionKey, message string) error {
	if strings.TrimSpace(message) == "" {
		return nil
	}

	followUpMsg := AgentMessage{
		Role:      RoleUser,
		Content:   []ContentBlock{TextContent{Text: message}},
		Timestamp: time.Now().UnixMilli(),
		Metadata:  map[string]any{"source": "subagent_result", "injected": true},
	}

	m.pushFollowUp(sessionKey, followUpMsg)

	logger.Info("sendToSession: subagent result injected to follow-up queue",
		zap.String("requester_session_key", sessionKey),
		zap.Int("message_len", len(message)))
	return nil
}

// createAgent 创建 Agent 实例
func (m *AgentManager) createAgent(cfg config.AgentConfig, contextBuilder *ContextBuilder, globalCfg *config.Config) error {
	// 获取 workspace 路径
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = globalCfg.Workspace.Path
	}

	// 获取模型
	model := cfg.Model
	if model == "" {
		model = globalCfg.Agents.Defaults.Model
	}

	// 获取最大迭代次数
	maxIterations := globalCfg.Agents.Defaults.MaxIterations
	if maxIterations == 0 {
		maxIterations = 15
	}

	// 获取最大历史消息数
	maxHistoryMessages := globalCfg.Agents.Defaults.MaxHistoryMessages
	if maxHistoryMessages == 0 {
		maxHistoryMessages = 100
	}

	// 确定该 agent 使用的 provider
	// 优先级：agent.provider(profile name) > 全局 provider
	agentProvider := m.provider
	agentProviderType := "global"
	agentBaseURL := ""
	if cfg.Provider != "" {
		maxTokens := globalCfg.Agents.Defaults.MaxTokens
		if maxTokens == 0 {
			maxTokens = 4096
		}
		p, resolvedModel, err := providers.NewProviderFromProfile(globalCfg, cfg.Provider, cfg.Model, maxTokens)
		if err != nil {
			logger.Warn("Failed to create provider for agent, falling back to global provider",
				zap.String("agent_id", cfg.ID),
				zap.String("profile", cfg.Provider),
				zap.Error(err))
		} else {
			agentProvider = p
			model = resolvedModel
			agentProviderType = cfg.Provider
			// 从 profiles 里找到对应的 base_url 用于日志展示
			for _, prof := range globalCfg.Providers.Profiles {
				if prof.Name == cfg.Provider {
					agentBaseURL = prof.BaseURL
					break
				}
			}
		}
	}

	logger.Info("━━ Agent initialized ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━",
		zap.String("agent_id", cfg.ID),
		zap.String("name", cfg.Name),
		zap.String("profile", agentProviderType),
		zap.String("model", model),
		zap.String("base_url", agentBaseURL),
		zap.Bool("default", cfg.Default))

	// 创建 Agent
	agent, err := NewAgent(&NewAgentConfig{
		AgentID:            cfg.ID,
		Bus:                m.bus,
		Provider:           agentProvider,
		SessionMgr:         m.sessionMgr,
		Tools:              m.tools,
		Context:            contextBuilder,
		Workspace:          workspace,
		MaxIteration:       maxIterations,
		MaxHistoryMessages: maxHistoryMessages,
		SkillsLoader:       m.skillsLoader,
	})
	if err != nil {
		return fmt.Errorf("failed to create agent %s: %w", cfg.ID, err)
	}

	// 设置 system prompt 优先级：
	// 1. 配置文件中配置了 system_prompt → 直接使用，完全替换内置提示词
	// 2. 未配置 → 保留 NewAgent 已构建的内置提示词（含工具列表、工作区文件等）
	if cfg.SystemPrompt != "" {
		agent.SetSystemPrompt(cfg.SystemPrompt)
		logger.Info("Agent using config system_prompt (overrides built-in)",
			zap.String("agent_id", cfg.ID),
			zap.Int("prompt_len", len(cfg.SystemPrompt)))
	} else {
		logger.Info("Agent using built-in system_prompt",
			zap.String("agent_id", cfg.ID),
			zap.Int("prompt_len", len(agent.GetSystemPrompt())))
	}

	// 存储到管理器
	m.agents[cfg.ID] = agent

	// 如果是默认 Agent，设置默认
	if cfg.Default {
		m.defaultAgent = agent
		m.defaultAgentID = cfg.ID
	}

	logger.Info("Agent created",
		zap.String("agent_id", cfg.ID),
		zap.String("name", cfg.Name),
		zap.String("workspace", workspace),
		zap.String("model", model),
		zap.String("provider_profile", cfg.Provider),
		zap.Bool("is_default", cfg.Default))

	return nil
}

// setupBinding 设置 Agent 绑定
func (m *AgentManager) setupBinding(binding config.BindingConfig) error {
	// 获取 Agent
	agent, ok := m.agents[binding.AgentID]
	if !ok {
		return fmt.Errorf("agent not found: %s", binding.AgentID)
	}

	// 构建绑定键
	bindingKey := buildBindingKey(binding.Match.Channel, binding.Match.AccountID)

	// 存储绑定
	m.bindings[bindingKey] = &BindingEntry{
		AgentID:   binding.AgentID,
		Channel:   binding.Match.Channel,
		AccountID: binding.Match.AccountID,
		Agent:     agent,
	}

	logger.Info("Binding setup",
		zap.String("binding_key", bindingKey),
		zap.String("agent_id", binding.AgentID))

	return nil
}

type inboundRouteDecision struct {
	agent             *Agent
	agentID           string
	source            string
	sessionKey        string
	matchedSessionKey string
	staleSessionKey   string
	staleAgentID      string
	bindingKey        string
}

// RouteInbound 路由入站消息到对应的 Agent
func (m *AgentManager) RouteInbound(ctx context.Context, msg *bus.InboundMessage) error {
	if msg == nil {
		return fmt.Errorf("inbound message is nil")
	}
	sessionKey := m.buildSessionKey(msg)

	logger.Info("[RouteInbound] routing message",
		zap.String("session_key", sessionKey),
		zap.String("channel", msg.Channel),
		zap.String("account_id", normalizeAccountID(msg.AccountID)),
		zap.String("chat_id", msg.ChatID),
		zap.String("content_preview", func() string {
			if len(msg.Content) > 50 {
				return msg.Content[:50]
			}
			return msg.Content
		}()),
	)

	// --- 处理 /agent 切换指令 ---
	if cmd := parseAgentSwitchCommand(msg.Content); cmd.IsSwitch {
		return m.handleAgentSwitchCommand(ctx, cmd, msg)
	}

	decision := m.resolveInboundRoute(msg)
	if decision.staleSessionKey != "" {
		logger.Warn("Session-bound agent not found, clearing stale route",
			zap.String("session_key", decision.staleSessionKey),
			zap.String("agent_id", decision.staleAgentID))
		m.sessionRouter.ClearAgentID(decision.staleSessionKey)
	}

	if decision.agent == nil {
		return fmt.Errorf("no agent found for message: %s", decision.bindingKey)
	}

	switch decision.source {
	case "session":
		logger.Info("[RouteInbound] routed by session router",
			zap.String("session_key", decision.matchedSessionKey),
			zap.String("agent_id", decision.agentID))
	case "binding":
		logger.Debug("Message routed by binding",
			zap.String("binding_key", decision.bindingKey),
			zap.String("agent_id", decision.agentID))
	case "default":
		logger.Debug("Message routed to default agent",
			zap.String("channel", msg.Channel),
			zap.String("account_id", normalizeAccountID(msg.AccountID)),
			zap.String("agent_id", decision.agentID))
	}

	// 处理消息
	return m.handleInboundMessage(ctx, msg, decision.agent)
}

// handleAgentSwitchCommand 处理 /agent 切换指令，返回结果给用户
func (m *AgentManager) handleAgentSwitchCommand(ctx context.Context, cmd *agentSwitchResult, msg *bus.InboundMessage) error {
	sessionKey := m.buildSessionKey(msg)
	sessionKeyCandidates := m.buildSessionKeyCandidates(msg)
	currentDecision := m.resolveInboundRoute(msg)
	allAgentIDs := m.ListAgents()
	sort.Strings(allAgentIDs)

	m.mu.RLock()
	defaultAgentID := m.defaultAgentID
	_, targetExists := m.agents[cmd.AgentID]
	m.mu.RUnlock()

	logger.Info("[AgentSwitch] switch command received",
		zap.String("session_key", sessionKey),
		zap.String("cmd_agent_id", cmd.AgentID),
		zap.Bool("is_query", cmd.IsQuery),
		zap.Bool("is_clear", cmd.IsClear),
		zap.String("current_agent_id", currentDecision.agentID),
		zap.String("current_source", currentDecision.source),
		zap.Strings("all_agents", allAgentIDs),
	)

	var replyText string

	switch {
	case cmd.IsQuery:
		replyText = buildAgentSwitchReply(cmd, currentDecision.agentID, currentDecision.source, defaultAgentID, allAgentIDs)

	case cmd.IsClear:
		m.clearSessionRoutes(sessionKeyCandidates)
		clearedDecision := m.resolveInboundRoute(msg)
		replyText = buildAgentSwitchReply(cmd, clearedDecision.agentID, clearedDecision.source, defaultAgentID, allAgentIDs)

	case cmd.AgentID == "list":
		replyText = buildAgentSwitchReply(cmd, currentDecision.agentID, currentDecision.source, defaultAgentID, allAgentIDs)

	default:
		// 切换到指定 Agent
		targetID := cmd.AgentID
		if !targetExists {
			replyText = fmt.Sprintf("Agent `%s` 不存在。\n可用 Agent：%s",
				targetID, formatAgentIDList(allAgentIDs))
			logger.Warn("[AgentSwitch] target agent not found",
				zap.String("target_agent_id", targetID),
				zap.Strings("available_agents", allAgentIDs),
			)
		} else {
			m.clearSessionRoutes(sessionKeyCandidates)
			m.sessionRouter.SetAgentID(sessionKey, targetID)
			logger.Info("[AgentSwitch] agent switched",
				zap.String("session_key", sessionKey),
				zap.String("target_agent_id", targetID),
			)
			replyText = buildAgentSwitchReply(cmd, targetID, "session", defaultAgentID, allAgentIDs)
		}
	}

	// 发布回复消息
	outbound := &bus.OutboundMessage{
		Channel:   msg.Channel,
		AccountID: msg.AccountID,
		ChatID:    msg.ChatID,
		Content:   replyText,
		ReplyTo:   outboundReplyTarget(msg),
		Timestamp: msg.Timestamp,
	}
	return m.bus.PublishOutbound(ctx, outbound)
}

// formatAgentIDList 将 Agent ID 列表格式化为可读字符串
func formatAgentIDList(ids []string) string {
	if len(ids) == 0 {
		return "（无）"
	}
	quoted := make([]string, len(ids))
	for i, id := range ids {
		quoted[i] = "`" + id + "`"
	}
	return strings.Join(quoted, ", ")
}

func outboundReplyTarget(msg *bus.InboundMessage) string {
	if msg == nil {
		return ""
	}

	if msg.Metadata != nil {
		for _, key := range []string{"message_id", "platform_message_id"} {
			if raw, ok := msg.Metadata[key]; ok {
				if replyTo := stringifyReplyTarget(raw); replyTo != "" {
					return replyTo
				}
			}
		}
	}

	replyTo := strings.TrimSpace(msg.ID)
	if replyTo == "" {
		return ""
	}
	if _, err := uuid.Parse(replyTo); err == nil {
		return ""
	}
	return replyTo
}

func stringifyReplyTarget(raw interface{}) string {
	if raw == nil {
		return ""
	}

	switch value := raw.(type) {
	case string:
		return strings.TrimSpace(value)
	case fmt.Stringer:
		return strings.TrimSpace(value.String())
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func isNewSessionCommand(content string) bool {
	return strings.TrimSpace(content) == "/new"
}

func normalizeAccountID(accountID string) string {
	trimmed := strings.TrimSpace(accountID)
	if trimmed == "" {
		return "default"
	}
	return trimmed
}

func buildBindingKey(channel, accountID string) string {
	return fmt.Sprintf("%s:%s", channel, normalizeAccountID(accountID))
}

func extractThreadSessionID(msg *bus.InboundMessage) string {
	if msg == nil || msg.Metadata == nil {
		return ""
	}

	for _, key := range []string{"thread_id", "thread_ts", "message_thread_id"} {
		if raw, ok := msg.Metadata[key]; ok {
			if value, ok := raw.(string); ok && strings.TrimSpace(value) != "" {
				return strings.TrimSpace(value)
			}
		}
	}

	return ""
}

func (m *AgentManager) buildSessionKey(msg *bus.InboundMessage) string {
	accountID := normalizeAccountID(msg.AccountID)
	sessionKey := fmt.Sprintf("%s:%s:%s", msg.Channel, accountID, msg.ChatID)
	if msg.ChatID == "default" || msg.ChatID == "" {
		sessionKey = fmt.Sprintf("%s:%s", msg.Channel, accountID)
	}
	if threadID := extractThreadSessionID(msg); threadID != "" {
		sessionKey = sessionKey + ":thread:" + threadID
	}
	return sessionKey
}

func (m *AgentManager) buildSessionKeyCandidates(msg *bus.InboundMessage) []string {
	if msg == nil {
		return nil
	}

	canonical := m.buildSessionKey(msg)
	keys := []string{canonical}
	rawAccountID := strings.TrimSpace(msg.AccountID)
	if rawAccountID == "" {
		legacy := strings.Replace(canonical, ":default", ":", 1)
		if legacy != canonical {
			keys = append(keys, legacy)
		}
	}
	return keys
}

func (m *AgentManager) findSessionBoundAgentID(sessionKeys []string) (string, string) {
	for _, sessionKey := range sessionKeys {
		if sessionKey == "" || m.sessionRouter == nil {
			continue
		}
		if agentID := m.sessionRouter.GetAgentID(sessionKey); agentID != "" {
			return sessionKey, agentID
		}
	}
	return "", ""
}

func (m *AgentManager) clearSessionRoutes(sessionKeys []string) {
	if m.sessionRouter == nil {
		return
	}
	cleared := make(map[string]struct{}, len(sessionKeys))
	for _, sessionKey := range sessionKeys {
		if sessionKey == "" {
			continue
		}
		if _, seen := cleared[sessionKey]; seen {
			continue
		}
		m.sessionRouter.ClearAgentID(sessionKey)
		cleared[sessionKey] = struct{}{}
	}
}

func (m *AgentManager) resolveInboundRoute(msg *bus.InboundMessage) inboundRouteDecision {
	decision := inboundRouteDecision{
		sessionKey: m.buildSessionKey(msg),
		bindingKey: buildBindingKey(msg.Channel, msg.AccountID),
	}

	if matchedSessionKey, sessionAgentID := m.findSessionBoundAgentID(m.buildSessionKeyCandidates(msg)); sessionAgentID != "" {
		decision.matchedSessionKey = matchedSessionKey
		m.mu.RLock()
		agent, ok := m.agents[sessionAgentID]
		m.mu.RUnlock()
		if ok {
			decision.agent = agent
			decision.agentID = sessionAgentID
			decision.source = "session"
			return decision
		}

		decision.staleSessionKey = matchedSessionKey
		decision.staleAgentID = sessionAgentID
	}

	m.mu.RLock()
	entry, hasBinding := m.bindings[decision.bindingKey]
	defaultAgent := m.defaultAgent
	defaultAgentID := m.defaultAgentID
	m.mu.RUnlock()

	if hasBinding && entry != nil && entry.Agent != nil {
		decision.agent = entry.Agent
		decision.agentID = entry.AgentID
		decision.source = "binding"
		return decision
	}

	if defaultAgent != nil {
		decision.agent = defaultAgent
		decision.agentID = defaultAgentID
		decision.source = "default"
	}

	return decision
}

func (m *AgentManager) resetSessionContextIfNeeded(ctx context.Context, msg *bus.InboundMessage) (bool, error) {
	if msg == nil || !isNewSessionCommand(msg.Content) {
		return false, nil
	}
	if m.sessionMgr == nil {
		return true, fmt.Errorf("session manager is not available")
	}

	sessionKey := m.buildSessionKey(msg)
	if err := m.sessionMgr.Delete(sessionKey); err != nil {
		return true, fmt.Errorf("failed to reset session context: %w", err)
	}

	newSessionID := uuid.NewString()
	sess, err := m.sessionMgr.GetOrCreate(sessionKey)
	if err != nil {
		return true, fmt.Errorf("failed to create new session: %w", err)
	}
	if sess.Metadata == nil {
		sess.Metadata = make(map[string]interface{})
	}
	sess.Metadata["session_id"] = newSessionID
	sess.Metadata["reset_at"] = time.Now().Format(time.RFC3339Nano)
	if err := m.sessionMgr.Save(sess); err != nil {
		return true, fmt.Errorf("failed to persist new session metadata: %w", err)
	}

	ack := AgentMessage{
		Role:      RoleAssistant,
		Content:   []ContentBlock{TextContent{Text: fmt.Sprintf("已开启新会话，session_id: %s", newSessionID)}},
		Timestamp: time.Now().UnixMilli(),
	}
	m.publishToBus(ctx, msg.Channel, msg.AccountID, msg.ChatID, buildOutboundMetadataFromInbound(msg), ack, outboundReplyTarget(msg))

	logger.Info("Session context reset by /new command",
		zap.String("session_key", sessionKey),
		zap.String("new_session_id", newSessionID),
	)
	return true, nil
}

// handleInboundMessage 处理入站消息
func (m *AgentManager) handleInboundMessage(ctx context.Context, msg *bus.InboundMessage, agent *Agent) error {
	logger.Info("[Manager] Processing inbound message",
		zap.String("message_id", msg.ID),
		zap.String("channel", msg.Channel),
		zap.String("account_id", msg.AccountID),
		zap.String("chat_id", msg.ChatID),
		zap.String("content", msg.Content),
	)

	if handled, err := m.resetSessionContextIfNeeded(ctx, msg); handled {
		return err
	}

	if handled, err := m.handleAcpThreadBindingInbound(ctx, msg); handled {
		logger.Info("[Manager] Message handled by ACP thread binding", zap.String("message_id", msg.ID))
		return err
	}
	if handled, err := m.handleDirectCronOneShot(ctx, msg); handled {
		logger.Info("[Manager] Message handled by cron oneshot", zap.String("message_id", msg.ID))
		return err
	}

	// 调用 Agent 处理消息（内部逻辑和 agent.go 中的 handleInboundMessage 类似）
	logger.Debug("[Manager] Routing to agent",
		zap.String("channel", msg.Channel),
		zap.String("account_id", msg.AccountID),
		zap.String("chat_id", msg.ChatID))

	// 生成会话键（包含 account_id 以区分不同账号的消息）
	sessionKey := m.buildSessionKey(msg)
	if msg.ChatID == "default" || msg.ChatID == "" {
		logger.Debug("[Manager] Creating fresh session", zap.String("session_key", sessionKey))
	}
	logger.Debug("[Manager] Creating fresh session", zap.String("session_key", sessionKey))
	replyTo := outboundReplyTarget(msg)

	// 获取或创建会话
	sess, err := m.sessionMgr.GetOrCreate(sessionKey)
	if err != nil {
		logger.Error("Failed to get session", zap.Error(err))
		return err
	}

	// 转换为 Agent 消息
	agentMsg := AgentMessage{
		Role:      RoleUser,
		Content:   []ContentBlock{TextContent{Text: msg.Content}},
		Timestamp: msg.Timestamp.UnixMilli(),
	}

	// 添加媒体内容
	for _, media := range msg.Media {
		if media.Type == "image" {
			agentMsg.Content = append(agentMsg.Content, ImageContent{
				URL:      media.URL,
				Data:     media.Base64,
				MimeType: media.MimeType,
			})
		}
	}

	// 获取 Agent 的 orchestrator
	orchestrator := agent.GetOrchestrator()

	// 同步本次请求的 sessionKey 到 agent state，
	// spawn tool 在工具 context 里读 SessionKeyContextKey 时能取到正确值，
	// 避免 fallback 到初始化时的空值（""）或 "main"
	orchestrator.state.SessionKey = sessionKey

	// 为本次 Run 克隆一份独立的 LoopConfig，避免并发请求互相覆盖回调。
	// 子 agent 完成后把结果 push 到 m.followUpQueues[sessionKey]，
	// runLoop 通过 GetFollowUpMessages 消费，识别到 source=subagent_result 后
	// 自动递减 state.PendingSubagents，实现"等所有子 agent 完成再汇总"。
	capturedSessionKey := sessionKey
	runConfig := *orchestrator.config // shallow copy
	runConfig.GetFollowUpMessages = func() ([]AgentMessage, error) {
		msgs := m.popFollowUps(capturedSessionKey)
		if len(msgs) > 0 {
			logger.Info("Follow-up messages injected from subagent results",
				zap.String("session_key", capturedSessionKey),
				zap.Int("count", len(msgs)))
		}
		return msgs, nil
	}
	runOrchestrator := NewOrchestrator(&runConfig, orchestrator.state)

	// 加载历史消息并添加当前消息
	// 使用配置的最大历史消息数限制，避免 token 超限
	// 使用 GetHistorySafe 确保不会在工具调用中间截断消息
	maxHistory := m.cfg.Agents.Defaults.MaxHistoryMessages
	if maxHistory <= 0 {
		maxHistory = 100 // 默认值
	}
	history := sess.GetHistorySafe(maxHistory)
	historyAgentMsgs := sessionMessagesToAgentMessages(history)
	allMessages := append(historyAgentMsgs, agentMsg)

	// 执行 Agent
	logger.Info("[Manager] Starting agent execution",
		zap.String("message_id", msg.ID),
		zap.Int("history_count", len(history)),
		zap.Int("total_messages", len(allMessages)),
	)
	runCtx := withInboundToolContext(ctx, msg)
	finalMessages, err := runOrchestrator.Run(runCtx, allMessages)
	logger.Info("[Manager] Agent execution completed",
		zap.String("message_id", msg.ID),
		zap.Int("final_messages", len(finalMessages)),
		zap.Error(err),
	)
	if err != nil {
		// Check if error is related to tool_call_id mismatch (old session format)
		errStr := err.Error()
		if strings.Contains(errStr, "tool_call_id") && strings.Contains(errStr, "mismatch") {
			logger.Warn("Detected old session format, clearing session",
				zap.String("session_key", sessionKey),
				zap.Error(err))
			if delErr := m.sessionMgr.Delete(sessionKey); delErr != nil {
				logger.Error("Failed to clear old session", zap.Error(delErr))
			} else {
				sess, getErr := m.sessionMgr.GetOrCreate(sessionKey)
				if getErr != nil {
					logger.Error("Failed to create fresh session", zap.Error(getErr))
					return getErr
				}
				finalMessages, retryErr := runOrchestrator.Run(runCtx, []AgentMessage{agentMsg})
				if retryErr != nil {
					logger.Error("Agent execution failed on retry", zap.Error(retryErr))
					return retryErr
				}
				m.updateSession(sess, finalMessages, 0)
				if replyMsg := findLatestReplyableAssistantMessage(finalMessages); replyMsg != nil {
					m.publishToBus(ctx, msg.Channel, msg.AccountID, msg.ChatID, buildOutboundMetadataFromInbound(msg), *replyMsg, replyTo)
				}
				return nil
			}
		}

		// 其他错误：尝试从已有消息里找最后一条 assistant 消息发布给用户，
		// 避免因错误而完全无回复（runLoop 已在内部注入了 fallback 消息时走这条路）
		if replyMsg := findLatestReplyableAssistantMessage(finalMessages); replyMsg != nil {
			logger.Warn("Agent error but publishing last assistant message",
				zap.Error(err))
			m.updateSession(sess, finalMessages, len(history))
			m.publishToBus(ctx, msg.Channel, msg.AccountID, msg.ChatID, buildOutboundMetadataFromInbound(msg), *replyMsg, replyTo)
			return nil
		}

		logger.Error("Agent execution failed", zap.Error(err))
		return err
	}

	// 更新会话（只保存新产生的消息）
	m.updateSession(sess, finalMessages, len(history))

	// 发布响应
	if replyMsg := findLatestReplyableAssistantMessage(finalMessages); replyMsg != nil {
		m.publishToBus(ctx, msg.Channel, msg.AccountID, msg.ChatID, buildOutboundMetadataFromInbound(msg), *replyMsg, replyTo)
	}

	return nil
}

func (m *AgentManager) handleDirectCronOneShot(ctx context.Context, msg *bus.InboundMessage) (bool, error) {
	if msg == nil || m.tools == nil {
		return false, nil
	}

	content := strings.TrimSpace(msg.Content)
	if !isCronOneShotRequest(content) {
		return false, nil
	}

	jobID, err := m.resolveCronJobIDForOneShot(ctx, content)
	if err != nil {
		m.publishAcpThreadBindingError(ctx, msg, "已识别为一次性测试请求，但未找到可执行任务："+err.Error())
		return true, nil
	}
	if ok, wait := m.allowManualCronRun(jobID, time.Now()); !ok {
		m.publishAcpThreadBindingError(ctx, msg, fmt.Sprintf("任务 `%s` 刚刚手工触发过，请 %d 秒后再试。", jobID, wait))
		return true, nil
	}

	ack := AgentMessage{
		Role:      RoleAssistant,
		Content:   []ContentBlock{TextContent{Text: fmt.Sprintf("收到，开始手工执行一次任务 `%s`。", jobID)}},
		Timestamp: time.Now().UnixMilli(),
	}
	m.publishToBus(ctx, msg.Channel, msg.AccountID, msg.ChatID, buildOutboundMetadataFromInbound(msg), ack, outboundReplyTarget(msg))

	outboundMetadata := buildOutboundMetadataFromInbound(msg)
	go func(channel, accountID, chatID, replyTo, id string, metadata map[string]interface{}) {
		runCtx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		_, runErr := m.tools.Execute(runCtx, "cron", map[string]interface{}{
			"command": fmt.Sprintf("run %s", id),
		})

		text := fmt.Sprintf("已手工执行一次任务 `%s`。", id)
		if runErr != nil {
			text = fmt.Sprintf("手工执行任务 `%s` 失败：%v", id, runErr)
		}

		done := AgentMessage{
			Role:      RoleAssistant,
			Content:   []ContentBlock{TextContent{Text: text}},
			Timestamp: time.Now().UnixMilli(),
		}
		m.publishToBus(context.Background(), channel, accountID, chatID, metadata, done, replyTo)
	}(msg.Channel, msg.AccountID, msg.ChatID, msg.ID, jobID, outboundMetadata)

	return true, nil
}

func (m *AgentManager) allowManualCronRun(jobID string, now time.Time) (bool, int) {
	const cooldown = 60 * time.Second
	m.manualCronMu.Lock()
	defer m.manualCronMu.Unlock()

	if last, ok := m.manualCronLast[jobID]; ok {
		if delta := now.Sub(last); delta < cooldown {
			wait := int((cooldown - delta).Round(time.Second).Seconds())
			if wait < 1 {
				wait = 1
			}
			return false, wait
		}
	}
	m.manualCronLast[jobID] = now
	return true, 0
}

func isCronOneShotRequest(text string) bool {
	if text == "" {
		return false
	}
	normalized := strings.ToLower(strings.TrimSpace(text))
	if strings.Contains(normalized, "cron run") {
		return true
	}
	keywords := []string{
		"执行一次定时任务",
		"只测试一次定时任务",
		"手工执行一次定时任务",
		"临时执行一次定时任务",
		"测试一次定时任务",
	}
	for _, kw := range keywords {
		if strings.Contains(normalized, kw) {
			return true
		}
	}
	return false
}

func (m *AgentManager) resolveCronJobIDForOneShot(ctx context.Context, text string) (string, error) {
	if id := cronJobIDPattern.FindString(text); id != "" {
		return id, nil
	}

	listOut, err := m.tools.Execute(ctx, "cron", map[string]interface{}{"command": "list"})
	if err != nil {
		return "", fmt.Errorf("获取任务列表失败: %w", err)
	}

	enabledIDs := extractEnabledCronJobIDs(listOut)
	switch len(enabledIDs) {
	case 0:
		return "", fmt.Errorf("没有启用中的任务")
	case 1:
		return enabledIDs[0], nil
	default:
		return "", fmt.Errorf("存在多个启用任务，请在消息中指定 job-id")
	}
}

func extractEnabledCronJobIDs(listOutput string) []string {
	lines := strings.Split(listOutput, "\n")
	ids := make([]string, 0)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		matches := cronListLinePattern.FindStringSubmatch(line)
		if len(matches) != 3 {
			continue
		}
		if matches[2] == "enabled" {
			ids = append(ids, matches[1])
		}
	}
	return ids
}

func (m *AgentManager) resolveAcpThreadBindingSession(msg *bus.InboundMessage) string {
	if m.channelMgr == nil || m.acpManager == nil || msg == nil {
		return ""
	}
	return m.channelMgr.RouteToAcpSession(msg.Channel, msg.AccountID, msg.ChatID)
}

func (m *AgentManager) handleAcpThreadBindingInbound(ctx context.Context, msg *bus.InboundMessage) (bool, error) {
	sessionKey := m.resolveAcpThreadBindingSession(msg)
	if sessionKey == "" {
		return false, nil
	}

	go m.runAcpThreadBindingTurn(ctx, sessionKey, msg)
	return true, nil
}

func (m *AgentManager) runAcpThreadBindingTurn(ctx context.Context, sessionKey string, msg *bus.InboundMessage) {
	requestID := msg.ID
	if requestID == "" {
		requestID = uuid.NewString()
	}

	result, err := m.acpManager.RunTrackedTurn(ctx, acp.RunTrackedTurnInput{
		Cfg:        m.cfg,
		SessionKey: sessionKey,
		Text:       msg.Content,
		Mode:       acpruntime.AcpPromptModePrompt,
		RequestID:  requestID,
	})
	if err != nil {
		logger.Error("Failed to run ACP turn for thread binding",
			zap.String("session_key", sessionKey),
			zap.String("channel", msg.Channel),
			zap.String("account_id", msg.AccountID),
			zap.String("chat_id", msg.ChatID),
			zap.Error(err))
		m.publishAcpThreadBindingError(ctx, msg, "ACP session is currently unavailable. Please retry.")
		return
	}

	var response strings.Builder
	for event := range result.EventChan {
		switch e := event.(type) {
		case *acpruntime.AcpEventTextDelta:
			if e.Stream == "" || e.Stream == "output" {
				response.WriteString(e.Text)
			}
		case *acpruntime.AcpEventError:
			logger.Error("ACP turn failed for thread binding",
				zap.String("session_key", sessionKey),
				zap.String("channel", msg.Channel),
				zap.String("account_id", msg.AccountID),
				zap.String("chat_id", msg.ChatID),
				zap.String("error_message", e.Message))
			m.publishAcpThreadBindingError(ctx, msg, "ACP session failed to complete this request.")
			return
		}
	}

	reply := response.String()
	if strings.TrimSpace(reply) == "" {
		reply = "ACP task finished."
	}

	outbound := AgentMessage{
		Role:      RoleAssistant,
		Content:   []ContentBlock{TextContent{Text: reply}},
		Timestamp: time.Now().UnixMilli(),
	}
	m.publishToBus(ctx, msg.Channel, msg.AccountID, msg.ChatID, buildOutboundMetadataFromInbound(msg), outbound, outboundReplyTarget(msg))
}

func (m *AgentManager) publishAcpThreadBindingError(ctx context.Context, msg *bus.InboundMessage, text string) {
	if msg == nil || strings.TrimSpace(text) == "" {
		return
	}
	outbound := AgentMessage{
		Role:      RoleAssistant,
		Content:   []ContentBlock{TextContent{Text: text}},
		Timestamp: time.Now().UnixMilli(),
	}
	m.publishToBus(ctx, msg.Channel, msg.AccountID, msg.ChatID, buildOutboundMetadataFromInbound(msg), outbound, outboundReplyTarget(msg))
}

// updateSession 更新会话
func (m *AgentManager) updateSession(sess *session.Session, messages []AgentMessage, historyLen int) {
	// 只保存新产生的消息（不包括历史消息）
	newMessages := messages
	if historyLen >= 0 && len(messages) > historyLen {
		newMessages = messages[historyLen:]
	}

	_ = m.helper.UpdateSession(sess, newMessages, &UpdateSessionOptions{SaveImmediately: true})
}

// publishToBus 发布消息到总线
func (m *AgentManager) publishToBus(ctx context.Context, channel, accountID, chatID string, metadata map[string]interface{}, msg AgentMessage, replyTo string) {
	content := extractTextContent(msg)

	outbound := &bus.OutboundMessage{
		Channel:   channel,
		AccountID: accountID,
		ChatID:    chatID,
		Content:   content,
		ReplyTo:   replyTo,
		Metadata:  metadata,
		Timestamp: time.Unix(msg.Timestamp/1000, 0),
	}

	if err := m.bus.PublishOutbound(ctx, outbound); err != nil {
		logger.Error("Failed to publish outbound", zap.Error(err))
	}
}

// sessionMessagesToAgentMessages 将 session 消息转换为 Agent 消息
func sessionMessagesToAgentMessages(sessMsgs []session.Message) []AgentMessage {
	result := make([]AgentMessage, 0, len(sessMsgs))
	for _, sessMsg := range sessMsgs {
		agentMsg := AgentMessage{
			Role:      MessageRole(sessMsg.Role),
			Content:   []ContentBlock{TextContent{Text: sessMsg.Content}},
			Timestamp: sessMsg.Timestamp.UnixMilli(),
		}

		// Handle tool calls in assistant messages
		if sessMsg.Role == "assistant" && len(sessMsg.ToolCalls) > 0 {
			// Clear the text content if there are tool calls
			agentMsg.Content = []ContentBlock{}
			for _, tc := range sessMsg.ToolCalls {
				agentMsg.Content = append(agentMsg.Content, ToolCallContent{
					ID:        tc.ID,
					Name:      tc.Name,
					Arguments: tc.Params,
				})
			}
		}

		// Handle tool result messages
		if sessMsg.Role == "tool" {
			agentMsg.Role = RoleToolResult
			// Set tool_call_id in metadata
			if sessMsg.ToolCallID != "" {
				if agentMsg.Metadata == nil {
					agentMsg.Metadata = make(map[string]any)
				}
				agentMsg.Metadata["tool_call_id"] = sessMsg.ToolCallID
			}
			// Restore tool_name from metadata if exists
			if toolName, ok := sessMsg.Metadata["tool_name"].(string); ok {
				if agentMsg.Metadata == nil {
					agentMsg.Metadata = make(map[string]any)
				}
				agentMsg.Metadata["tool_name"] = toolName
			}
		}

		result = append(result, agentMsg)
	}
	return result
}

// GetAgent 获取 Agent
func (m *AgentManager) GetAgent(agentID string) (*Agent, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent, ok := m.agents[agentID]
	return agent, ok
}

// ListAgents 列出所有 Agent ID
func (m *AgentManager) ListAgents() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	ids := make([]string, 0, len(m.agents))
	for id := range m.agents {
		ids = append(ids, id)
	}
	return ids
}

// Start 启动所有 Agent
func (m *AgentManager) Start(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for id := range m.agents {
		logger.Info("Agent registered under manager (inbound loop handled by AgentManager)",
			zap.String("agent_id", id))
	}

	// 启动消息处理器
	go m.processMessages(ctx)

	return nil
}

// Stop 停止所有 Agent
func (m *AgentManager) Stop() error {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for id, agent := range m.agents {
		if err := agent.Stop(); err != nil {
			logger.Error("Failed to stop agent",
				zap.String("agent_id", id),
				zap.Error(err))
		}
	}

	return nil
}

// ReloadBindings replaces runtime channel bindings with the current config values.
func (m *AgentManager) ReloadBindings(cfg *config.Config) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.cfg = cfg
	m.bindings = make(map[string]*BindingEntry)

	m.defaultAgent = nil
	m.defaultAgentID = ""
	for _, agentCfg := range cfg.Agents.List {
		if !agentCfg.Default {
			continue
		}
		m.defaultAgentID = agentCfg.ID
		m.defaultAgent = m.agents[agentCfg.ID]
		break
	}
	if m.defaultAgent == nil && m.defaultAgentID == "" {
		for agentID, agent := range m.agents {
			m.defaultAgentID = agentID
			m.defaultAgent = agent
			break
		}
	}

	for _, binding := range cfg.Bindings {
		if err := m.setupBinding(binding); err != nil {
			return err
		}
	}

	return nil
}

// processMessages 处理入站消息
func (m *AgentManager) processMessages(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			logger.Info("Agent manager message processor stopped")
			return
		default:
			msg, err := m.bus.ConsumeInbound(ctx)
			if err != nil {
				if err == context.DeadlineExceeded || err == context.Canceled {
					continue
				}
				logger.Error("Failed to consume inbound", zap.Error(err))
				continue
			}

			logger.Debug("[Manager] Consumed inbound message from bus",
				zap.String("message_id", msg.ID),
				zap.String("channel", msg.Channel),
				zap.String("chat_id", msg.ChatID),
			)
			if err := m.RouteInbound(ctx, msg); err != nil {
				logger.Error("Failed to route message",
					zap.String("channel", msg.Channel),
					zap.String("account_id", msg.AccountID),
					zap.Error(err))
			}
		}
	}
}

// GetDefaultAgent 获取默认 Agent
func (m *AgentManager) GetDefaultAgent() *Agent {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultAgent
}

// GetToolsInfo 获取工具信息
func (m *AgentManager) GetToolsInfo() (map[string]interface{}, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 从 tool registry 获取工具列表
	existingTools := m.tools.ListExisting()
	result := make(map[string]interface{})

	for _, tool := range existingTools {
		result[tool.Name()] = map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"parameters":  tool.Parameters(),
		}
	}

	return result, nil
}

// injectSpawnableAgentDescriptions 为配置了 allow_agents 的 Agent 动态注入可派生 Agent 目录。
// 在 system_prompt 末尾追加一段"可派生 Agent 目录"，让 LLM 在调用 sessions_spawn 时能
// 根据描述准确选择 agent_id，无需在 prompt 里手写静态列表。
func (m *AgentManager) injectSpawnableAgentDescriptions(cfg *config.Config) {
	// 建立 id -> AgentConfig 的快速索引
	agentCfgMap := make(map[string]config.AgentConfig, len(cfg.Agents.List))
	for _, a := range cfg.Agents.List {
		agentCfgMap[a.ID] = a
	}

	type entry struct {
		id          string
		name        string
		description string
	}

	for _, agentCfg := range cfg.Agents.List {
		// 只有配置了 sessions_spawn（即能 spawn）的 agent 才需要注入目录
		// 判断依据：Subagents 不为 nil，或者没有在 deny_tools 里禁掉 sessions_spawn
		// 简单判断：只要不在自身的 deny_tools 里禁掉 sessions_spawn，就视为可 spawn
		if agentCfg.Subagents != nil {
			spawnDenied := false
			for _, dt := range agentCfg.Subagents.DenyTools {
				if strings.EqualFold(strings.TrimSpace(dt), "sessions_spawn") {
					spawnDenied = true
					break
				}
			}
			if spawnDenied {
				continue
			}
		}

		var candidates []config.AgentConfig

		if agentCfg.Subagents != nil && len(agentCfg.Subagents.AllowAgents) > 0 {
			// 明确配置了 allow_agents：只收集白名单内的 agent
			for _, allowedID := range agentCfg.Subagents.AllowAgents {
				allowedID = strings.TrimSpace(allowedID)
				if allowedID == "*" {
					// 通配符：收集所有有描述的非自身 agent
					for _, a := range cfg.Agents.List {
						if a.ID != agentCfg.ID && a.Description != "" {
							candidates = append(candidates, a)
						}
					}
					break
				}
				if allowedID == agentCfg.ID {
					continue
				}
				if target, ok := agentCfgMap[allowedID]; ok {
					candidates = append(candidates, target)
				}
			}
		} else {
			// 未配置 allow_agents：自动收集所有有描述的非自身、非默认 agent
			for _, a := range cfg.Agents.List {
				if a.ID != agentCfg.ID && !a.Default && a.Description != "" {
					candidates = append(candidates, a)
				}
			}
		}

		// 过滤掉没有 description 的
		var entries []entry
		for _, target := range candidates {
			if target.Description == "" {
				continue
			}
			name := target.Name
			if name == "" {
				name = target.ID
			}
			entries = append(entries, entry{id: target.ID, name: name, description: target.Description})
		}

		if len(entries) == 0 {
			continue
		}

		// 拼接目录段落
		var sb strings.Builder
		sb.WriteString("\n\n## 可派生 Agent 目录\n")
		sb.WriteString("调用 sessions_spawn 时**必须**设置 `agent_id` 字段，指定由哪个 Agent 执行任务：\n")
		for _, e := range entries {
			sb.WriteString(fmt.Sprintf("\n- **agent_id: \"%s\"** — %s — %s\n", e.id, e.name, e.description))
		}
		sb.WriteString("\n> ⚠️ 不传 agent_id 则子任务将由当前 Agent 自己执行，无法利用专属模型能力。\n")

		// 追加到该 Agent 的 system_prompt
		agent, ok := m.agents[agentCfg.ID]
		if !ok {
			continue
		}
		existing := agent.GetSystemPrompt()
		agent.SetSystemPrompt(existing + sb.String())

		logger.Info("Injected spawnable agent descriptions",
			zap.String("agent_id", agentCfg.ID),
			zap.Int("spawnable_count", len(entries)),
			zap.Bool("auto_discover", agentCfg.Subagents == nil || len(agentCfg.Subagents.AllowAgents) == 0))
	}
}
