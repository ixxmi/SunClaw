package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/smallnest/goclaw/internal/core/providers"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// Context keys for passing agent state through context
type contextKey string

const (
	SessionKeyContextKey     contextKey = "session_key"
	AgentIDContextKey        contextKey = "agent_id"
	BootstrapOwnerContextKey contextKey = "bootstrap_owner_id"
)

// toolResultPair is used to pass tool execution results from goroutines
type toolResultPair struct {
	result *ToolResult
	err    error
}

// Orchestrator manages the agent execution loop
// Based on pi-mono's agent-loop.ts design
//
// Concurrency: Each Run() call creates a cloned state for isolation.
// The original state stored in o.state is used only as a template.
// Multiple Run() calls can execute concurrently safely.
type Orchestrator struct {
	config     *LoopConfig
	state      *AgentState // Initial state, used as template for each Run
	eventChan  chan *Event
	cancelFunc context.CancelFunc
	mu         sync.Mutex // protects cancelFunc, eventChan, and Stop()
}

// NewOrchestrator creates a new agent orchestrator
func NewOrchestrator(config *LoopConfig, initialState *AgentState) *Orchestrator {
	return &Orchestrator{
		config:    config,
		state:     initialState,
		eventChan: make(chan *Event, 1000),
	}
}

// Run starts the agent loop with initial prompts
func (o *Orchestrator) Run(ctx context.Context, prompts []AgentMessage) ([]AgentMessage, error) {
	logger.Debug("=== Orchestrator Run Start ===",
		zap.Int("prompts_count", len(prompts)))

	ctx, cancel := context.WithCancel(ctx)
	o.mu.Lock()
	o.cancelFunc = cancel
	o.mu.Unlock()

	// Initialize state with prompts
	newMessages := make([]AgentMessage, len(prompts))
	copy(newMessages, prompts)
	currentState := o.state.Clone()
	currentState.AddMessages(newMessages)

	// Emit start event
	o.emit(NewEvent(EventAgentStart))

	// Main loop
	finalMessages, err := o.runLoop(ctx, currentState)

	logger.Debug("=== Orchestrator Run End ===",
		zap.Int("final_messages_count", len(finalMessages)),
		zap.Error(err))

	// Emit end event
	endEvent := NewEvent(EventAgentEnd)
	if finalMessages != nil {
		endEvent = NewEvent(EventAgentEnd).WithFinalMessages(finalMessages)
	}
	o.emit(endEvent)

	cancel()
	if err != nil {
		return nil, fmt.Errorf("agent loop failed: %w", err)
	}

	return finalMessages, nil
}

// runLoop implements the main agent loop logic.
//
// 设计参考 picoclaw agent-loop：
//   - LLMCallCount 精确计数（只计真实 LLM 调用，不含消息注入）
//   - 连续工具失败检测 + 自动触发反思提示
//   - stop_reason 分类处理（tool_calls / length / stop）
//   - 超限时注入反思提示让 LLM 自主总结，而非强制截断
func (o *Orchestrator) runLoop(ctx context.Context, state *AgentState) ([]AgentMessage, error) {
	// ---------- 参数归一化 ----------
	maxIterations := o.config.MaxIterations
	if maxIterations <= 0 {
		maxIterations = 15
	}
	maxConsecutiveErrors := o.config.MaxConsecutiveErrors
	if maxConsecutiveErrors <= 0 {
		maxConsecutiveErrors = 3
	}
	maxToolFailures := o.config.MaxToolFailures
	if maxToolFailures <= 0 {
		maxToolFailures = 10
	}
	// 默认开启反思（EnableReflection 零值 false 时也视为开启，需显式在 config 中设置 DisableReflection）
	enableReflection := true
	if o.config.MaxConsecutiveErrors < 0 {
		// 负值表示显式禁用反思机制
		enableReflection = false
	}
	_ = enableReflection // 后续条件中直接读此变量

	pendingMessages := o.fetchSteeringMessages()
	firstTurn := true

	// ─────────────────────────────────────────────────────────
	// 外层循环：处理 follow-up 消息驱动的新一轮对话
	// ─────────────────────────────────────────────────────────
	for {
		// ─────────────────────────────────────────────
		// 内层循环：单次对话中 LLM ↔ 工具的 ReAct 循环
		// ─────────────────────────────────────────────
		for {
			// 1. 检查 context 取消
			select {
			case <-ctx.Done():
				logger.Warn("Agent loop interrupted", zap.Error(ctx.Err()))
				// 已有 assistant 消息则直接返回，否则注入提示后返回
				hasReply := false
				for i := len(state.Messages) - 1; i >= 0; i-- {
					if state.Messages[i].Role == RoleAssistant {
						hasReply = true
						break
					}
				}
				if !hasReply {
					state.AddMessage(AgentMessage{
						Role:      RoleAssistant,
						Content:   []ContentBlock{TextContent{Text: "请求超时，请稍后重试。"}},
						Timestamp: time.Now().UnixMilli(),
						Metadata:  map[string]any{"stop_reason": "timeout"},
					})
				}
				return state.Messages, nil
			default:
			}

			// 2. 注入 pending 消息（steering / follow-up 注入）
			if len(pendingMessages) > 0 {
				for _, msg := range pendingMessages {
					o.emit(NewEvent(EventMessageStart))
					state.AddMessage(msg)
					o.emit(NewEvent(EventMessageEnd))
				}
				pendingMessages = nil
			}

			// 3. 检查 LLM 调用次数上限
			if state.LLMCallCount >= maxIterations {
				logger.Warn("Max LLM iterations reached, triggering final reflection",
					zap.Int("llm_calls", state.LLMCallCount),
					zap.Int("max", maxIterations))
				o.injectSystemNotice(state, fmt.Sprintf(
					"[SYSTEM] 已达到最大迭代次数（%d 次 LLM 调用）。请根据已有工具结果给出最终回复，不要再调用任何工具。",
					maxIterations,
				))
				assistantMsg, err := o.streamAssistantResponse(ctx, state)
				if err != nil {
					if assistantMsg.Role == RoleAssistant && len(assistantMsg.Content) > 0 {
						state.AddMessage(assistantMsg)
					}
					return state.Messages, nil
				}
				state.AddMessage(assistantMsg)
				return state.Messages, nil
			}

			// 4. 检查连续错误是否需要反思
			if state.ConsecutiveErrors >= maxConsecutiveErrors {
				state.ReflectionCount++
				logger.Warn("Consecutive tool errors, injecting reflection prompt",
					zap.Int("consecutive_errors", state.ConsecutiveErrors),
					zap.Int("reflection_count", state.ReflectionCount))
				o.injectSystemNotice(state, fmt.Sprintf(
					"[SYSTEM] 最近 %d 次工具调用连续失败。请反思：1) 工具参数是否正确？2) 是否选择了错误的工具？3) 能否换一种方式完成任务？请调整策略后继续，或直接给出当前已知结论。",
					state.ConsecutiveErrors,
				))
				state.ConsecutiveErrors = 0 // 反思后重置，给 LLM 机会修正
			}

			// 5. 检查总失败次数
			if state.ToolFailCount >= maxToolFailures {
				logger.Error("Too many tool failures, aborting loop",
					zap.Int("tool_fail_count", state.ToolFailCount),
					zap.Int("max", maxToolFailures))
				o.injectSystemNotice(state, fmt.Sprintf(
					"[SYSTEM] 工具调用累计失败 %d 次，已超过上限。请给出当前已知的最佳回复。",
					state.ToolFailCount,
				))
				assistantMsg, err := o.streamAssistantResponse(ctx, state)
				if err != nil {
					if assistantMsg.Role == RoleAssistant && len(assistantMsg.Content) > 0 {
						state.AddMessage(assistantMsg)
					}
					return state.Messages, nil
				}
				state.AddMessage(assistantMsg)
				return state.Messages, nil
			}

			// 6. 发起 LLM 调用
			if !firstTurn {
				o.emit(NewEvent(EventTurnStart))
			} else {
				firstTurn = false
			}

			state.LLMCallCount++
			logger.Info("Agent loop: LLM call",
				zap.Int("llm_call", state.LLMCallCount),
				zap.Int("max", maxIterations),
				zap.Int("tool_calls", state.ToolCallCount),
				zap.Int("tool_fails", state.ToolFailCount),
				zap.Int("consecutive_errors", state.ConsecutiveErrors))

			assistantMsg, err := o.streamAssistantResponse(ctx, state)
			if err != nil {
				// streamAssistantResponse 在失败时已返回 fallbackMsg（降级提示），
				// 将其加入消息列表并发布给用户，然后正常退出而非抛出错误。
				if assistantMsg.Role == RoleAssistant && len(assistantMsg.Content) > 0 {
					state.AddMessage(assistantMsg)
					logger.Warn("LLM call failed, returning fallback message to user",
						zap.Error(err))
				} else {
					o.emitErrorEnd(state, err)
				}
				return state.Messages, nil // 不向上传播错误，让 manager 正常发布回复
			}
			state.AddMessage(assistantMsg)

			// 7. 分析 stop_reason
			stopReason := "stop"
			if meta, ok := assistantMsg.Metadata["stop_reason"].(string); ok {
				stopReason = meta
			}

			// 8. 提取工具调用
			toolCalls := extractToolCalls(assistantMsg)

			logger.Info("Agent loop: LLM response",
				zap.String("stop_reason", stopReason),
				zap.Int("tool_calls", len(toolCalls)),
				zap.Int("content_len", extractContentLength(assistantMsg)))

			// 9. 处理 stop_reason
			switch stopReason {
			case "length":
				// 响应被截断：注入提示继续生成
				logger.Warn("LLM response truncated (length), injecting continuation prompt")
				o.injectSystemNotice(state,
					"[SYSTEM] 上一条回复因长度限制被截断。请继续完成未完成的内容。")
				o.emit(NewEvent(EventTurnEnd))
				continue // 继续内层循环，再次调用 LLM

			case "tool_calls", "":
				// 有工具调用，正常执行
				if len(toolCalls) == 0 && stopReason == "tool_calls" {
					// 异常：stop_reason 说有工具但实际没有，视为正常结束
					logger.Warn("stop_reason=tool_calls but no tool calls found, treating as stop")
					o.emit(NewEvent(EventTurnEnd))
					break
				}

			case "stop", "end_turn":
				// 正常结束：没有工具调用，退出内层循环
				if len(toolCalls) == 0 {
					o.emit(NewEvent(EventTurnEnd))
					goto outerCheck
				}
			}

			// 10. 执行工具调用
			if len(toolCalls) > 0 {
				toolResults, steering := o.executeToolCalls(ctx, toolCalls, state)

				// 更新状态计数
				state.ToolCallCount += len(toolCalls)

				// 统计本轮失败数
				roundFails := 0
				for _, r := range toolResults {
					if _, hasErr := r.Metadata["error"]; hasErr {
						roundFails++
					}
				}
				if roundFails > 0 {
					state.ToolFailCount += roundFails
					state.ConsecutiveErrors += roundFails
				} else {
					state.ConsecutiveErrors = 0 // 有成功则重置连续错误
				}

				// 注入工具结果
				for _, result := range toolResults {
					state.AddMessage(result)
				}

				// steering 消息中断
				if len(steering) > 0 {
					pendingMessages = steering
					o.emit(NewEvent(EventTurnEnd))
					break // 退出内层循环，外层再进入
				}
			} else {
				// 没有工具调用且 stop_reason 不是 stop：视为完成
				o.emit(NewEvent(EventTurnEnd))
				goto outerCheck
			}

			o.emit(NewEvent(EventTurnEnd))

			// 内层循环末尾：拉取新的 steering 消息
			if len(pendingMessages) == 0 {
				pendingMessages = o.fetchSteeringMessages()
				if len(pendingMessages) > 0 {
					break // 有 steering，退出内层重新处理
				}
			}
		} // end inner loop

	outerCheck:
		// ── 子 agent 等待逻辑 ────────────────────────────────────────
		for state.HasPendingSubagents() {
			select {
			case <-ctx.Done():
				// ctx 超时/取消：不再等待子 agent，用已有消息（含 fallback）返回
				logger.Warn("Context cancelled while waiting for subagents, returning partial result",
					zap.Int64("pending_subagents", atomic.LoadInt64(&state.PendingSubagents)),
					zap.Error(ctx.Err()))
				// 如果没有任何 assistant 回复，注入一条超时提示
				hasAssistantReply := false
				for i := len(state.Messages) - 1; i >= 0; i-- {
					if state.Messages[i].Role == RoleAssistant {
						hasAssistantReply = true
						break
					}
				}
				if !hasAssistantReply {
					state.AddMessage(AgentMessage{
						Role:      RoleAssistant,
						Content:   []ContentBlock{TextContent{Text: "子任务执行超时，请稍后重试。"}},
						Timestamp: time.Now().UnixMilli(),
						Metadata:  map[string]any{"stop_reason": "timeout"},
					})
				}
				return state.Messages, nil // 不向上传播 error
			default:
			}

			followUpMessages := o.fetchFollowUpMessages()
			if len(followUpMessages) > 0 {
				for _, fm := range followUpMessages {
					if fm.Metadata != nil {
						if src, _ := fm.Metadata["source"].(string); src == "subagent_result" {
							state.RemovePendingSubagent()
							logger.Info("Subagent result received, pending count -1",
								zap.Int64("pending_subagents", atomic.LoadInt64(&state.PendingSubagents)))
						}
					}
					pendingMessages = append(pendingMessages, fm)
				}
				break
			}

			logger.Debug("Waiting for subagents...",
				zap.Int64("pending_subagents", atomic.LoadInt64(&state.PendingSubagents)))
			time.Sleep(500 * time.Millisecond)
		}

		// 检查普通 follow-up 消息（用户在 Agent 运行中追加的消息）
		if len(pendingMessages) == 0 {
			followUpMessages := o.fetchFollowUpMessages()
			if len(followUpMessages) > 0 {
				for _, fm := range followUpMessages {
					if fm.Metadata != nil {
						if src, _ := fm.Metadata["source"].(string); src == "subagent_result" {
							state.RemovePendingSubagent()
						}
					}
					pendingMessages = append(pendingMessages, fm)
				}
			}
		}

		if len(pendingMessages) > 0 {
			firstTurn = false
			continue // 外层继续，让 LLM 汇总
		}

		// 没有 pending 子 agent，没有 follow-up，正常退出
		break
	}

	logger.Info("Agent loop completed",
		zap.Int("llm_calls", state.LLMCallCount),
		zap.Int("tool_calls", state.ToolCallCount),
		zap.Int("tool_fails", state.ToolFailCount),
		zap.Int("reflections", state.ReflectionCount))

	return state.Messages, nil
}

// injectSystemNotice 注入系统提示消息（以 user 角色，避免破坏 assistant/tool 交替结构）
func (o *Orchestrator) injectSystemNotice(state *AgentState, text string) {
	notice := AgentMessage{
		Role:      RoleUser,
		Content:   []ContentBlock{TextContent{Text: text}},
		Timestamp: time.Now().UnixMilli(),
		Metadata:  map[string]any{"injected": true, "type": "system_notice"},
	}
	state.AddMessage(notice)
}

// extractContentLength 提取 AgentMessage 内容总长度（用于日志）
func extractContentLength(msg AgentMessage) int {
	total := 0
	for _, b := range msg.Content {
		if t, ok := b.(TextContent); ok {
			total += len(t.Text)
		}
	}
	return total
}

func isBootstrapGuideModeContent(content string) bool {
	if content == "" {
		return false
	}
	if !strings.Contains(content, "### BOOTSTRAP.md") {
		return false
	}
	cognitiveHeaders := []string{
		"### IDENTITY.md",
		"### AGENTS.md",
		"### SOUL.md",
		"### USER.md",
	}
	for _, header := range cognitiveHeaders {
		if strings.Contains(content, header) {
			return false
		}
	}
	return true
}

// streamAssistantResponse calls the LLM and streams the response
func (o *Orchestrator) streamAssistantResponse(ctx context.Context, state *AgentState) (AgentMessage, error) {
	logger.Debug("=== streamAssistantResponse Start ===",
		zap.Int("message_count", len(state.Messages)),
		zap.Strings("loaded_skills", state.LoadedSkills))

	state.IsStreaming = true
	defer func() { state.IsStreaming = false }()

	// Apply context transform if configured
	messages := state.Messages
	if o.config.TransformContext != nil {
		transformed, err := o.config.TransformContext(messages)
		if err == nil {
			messages = transformed
		} else {
			logger.Warn("Context transform failed, using original", zap.Error(err))
		}
	}

	// Convert to provider messages
	var providerMsgs []providers.Message
	if o.config.ConvertToLLM != nil {
		converted, err := o.config.ConvertToLLM(messages)
		if err != nil {
			return AgentMessage{}, fmt.Errorf("convert to LLM failed: %w", err)
		}
		providerMsgs = converted
	} else {
		// Default conversion
		providerMsgs = convertToProviderMessages(messages)
	}

	// Prepare tool definitions
	toolDefs := convertToToolDefinitions(state.Tools)

	// Emit message start
	o.emit(NewEvent(EventMessageStart))

	// Build system prompt with skills.
	// IMPORTANT: keep agent-specific system prompt (state.SystemPrompt) effective,
	// but inject skill summary/full content dynamically at request time so custom
	// prompts can still participate in the two-phase skill workflow.
	fullMessages := []providers.Message{}
	skillsContent := ""
	bootstrapContent := ""
	if o.config.ContextBuilder != nil {
		skillsContent = o.config.ContextBuilder.buildSkillsContext(o.config.Skills, state.LoadedSkills, PromptModeFull)
		bootstrapContent = o.config.ContextBuilder.buildBootstrapSectionForOwner(state.BootstrapOwnerID)
	}
	bootstrapModeNotice := ""
	if isBootstrapGuideModeContent(bootstrapContent) {
		bootstrapModeNotice = `## Bootstrap Mode

This main agent does not have agent-specific cognitive files yet (` + "`IDENTITY.md`" + `, ` + "`AGENTS.md`" + `, ` + "`SOUL.md`" + `, ` + "`USER.md`" + `).

Treat ` + "`BOOTSTRAP.md`" + ` below as authoritative for identity bootstrapping.
Any fixed identity or role wording elsewhere in this system prompt defines only temporary operational behavior, not the final self-identity.

If the user asks who you are, do not answer with a fixed identity such as "I am SunClaw" unless that identity has already been explicitly written into ` + "`IDENTITY.md`" + `.
Instead, explain that this agent is not fully defined yet, use the bootstrap guide to establish identity with the user, and then write the resulting cognitive files.`
	}
	if state.SystemPrompt != "" {
		// state.SystemPrompt already includes base instructions + optional agent-specific instructions.
		systemPrompt := state.SystemPrompt
		systemPrompt = joinNonEmpty([]string{
			systemPrompt,
			bootstrapModeNotice,
			bootstrapContent,
			skillsContent,
		}, "\n\n---\n\n")
		fullMessages = append(fullMessages, providers.Message{
			Role:    "system",
			Content: systemPrompt,
		})
		logger.Info("System prompt source: state.SystemPrompt",
			zap.Int("prompt_length", len(systemPrompt)),
			zap.Bool("has_config_prompt", !strings.Contains(systemPrompt, "# Identity\n\nYou are **GoClaw**")),
			zap.Int("loaded_skills", len(state.LoadedSkills)))
	} else if o.config.ContextBuilder != nil {
		// Fallback: build from context builder when state prompt is empty.
		systemPrompt := joinNonEmpty([]string{
			o.config.ContextBuilder.buildSystemPromptWithSkills(skillsContent, PromptModeFull),
			bootstrapModeNotice,
			bootstrapContent,
		}, "\n\n---\n\n")
		fullMessages = append(fullMessages, providers.Message{
			Role:    "system",
			Content: systemPrompt,
		})
		logger.Info("System prompt source: contextBuilder",
			zap.Int("prompt_length", len(systemPrompt)),
			zap.Int("loaded_skills", len(state.LoadedSkills)))
	}
	fullMessages = append(fullMessages, providerMsgs...)

	logger.Info("=== Calling LLM ===",
		zap.Int("messages_count", len(fullMessages)),
		zap.Int("tools_count", len(toolDefs)),
		zap.Bool("has_loaded_skills", len(state.LoadedSkills) > 0))

	// Try streaming if provider supports it
	if sp, ok := o.config.Provider.(providers.StreamingProvider); ok {
		msg, err := o.callWithStreaming(ctx, sp, fullMessages, toolDefs)
		if err == nil {
			return msg, nil
		}
		// errStreamingFailed：无任何内容，降级到 non-streaming 重试
		if err == errStreamingFailed {
			logger.Warn("Streaming returned no content, falling back to non-streaming")
		} else {
			// 其他 streaming 错误也降级重试
			logger.Warn("Streaming error, falling back to non-streaming", zap.Error(err))
		}
		// 降级继续执行 non-streaming
	}

	// Non-streaming（首选或 streaming 降级）
	response, err := o.config.Provider.Chat(ctx, fullMessages, toolDefs)
	if err != nil {
		logger.Error("LLM call failed", zap.Error(err))
		// 返回一条降级提示而不是空响应，确保用户一定收到回复
		fallbackMsg := AgentMessage{
			Role:      RoleAssistant,
			Content:   []ContentBlock{TextContent{Text: "抱歉，LLM 请求失败，请稍后重试。"}},
			Timestamp: time.Now().UnixMilli(),
			Metadata:  map[string]any{"stop_reason": "error", "llm_error": err.Error()},
		}
		return fallbackMsg, fmt.Errorf("LLM call failed: %w", err)
	}

	logger.Info("=== LLM Response Received ===",
		zap.Int("content_length", len(response.Content)),
		zap.Int("tool_calls_count", len(response.ToolCalls)),
		zap.String("content_preview", truncateString(response.Content, 200)))

	o.emit(NewEvent(EventMessageEnd))

	assistantMsg := convertFromProviderResponse(response)

	logger.Debug("=== streamAssistantResponse End ===",
		zap.Bool("has_tool_calls", len(response.ToolCalls) > 0),
		zap.Int("tool_calls_count", len(response.ToolCalls)))

	return assistantMsg, nil
}

// errStreamingFailed 是 streaming 失败且无任何已积累内容时的哨兵错误，
// 供 streamAssistantResponse 识别并触发 non-streaming 降级重试。
var errStreamingFailed = fmt.Errorf("streaming failed with no partial content")

// callWithStreaming calls the LLM with streaming support
func (o *Orchestrator) callWithStreaming(ctx context.Context, sp providers.StreamingProvider, messages []providers.Message, tools []providers.ToolDefinition) (AgentMessage, error) {
	var contentBuilder, thinkingBuilder, finalBuilder strings.Builder
	var toolCalls []providers.ToolCall
	var streamErr error

	err := sp.ChatStream(ctx, messages, tools, func(chunk providers.StreamChunk) {
		if chunk.Error != nil {
			streamErr = chunk.Error
			return
		}

		// Handle different chunk types
		if chunk.ToolCall != nil {
			toolCalls = append(toolCalls, *chunk.ToolCall)
		} else if chunk.IsThinking {
			thinkingBuilder.WriteString(chunk.Content)
			o.emit(&Event{
				Type:          EventStreamThinking,
				StreamContent: chunk.Content,
				Timestamp:     time.Now().UnixMilli(),
			})
		} else if chunk.IsFinal {
			finalBuilder.WriteString(chunk.Content)
			o.emit(&Event{
				Type:          EventStreamFinal,
				StreamContent: chunk.Content,
				Timestamp:     time.Now().UnixMilli(),
			})
		} else if chunk.Content != "" {
			contentBuilder.WriteString(chunk.Content)
			o.emit(&Event{
				Type:          EventStreamContent,
				StreamContent: chunk.Content,
				Timestamp:     time.Now().UnixMilli(),
			})
		}

		if chunk.Done {
			o.emit(&Event{
				Type:      EventStreamDone,
				Timestamp: time.Now().UnixMilli(),
			})
		}
	})

	// 合并所有错误来源
	firstErr := err
	if firstErr == nil {
		firstErr = streamErr
	}

	hasPartialContent := contentBuilder.Len() > 0 || finalBuilder.Len() > 0 || thinkingBuilder.Len() > 0
	hasToolCalls := len(toolCalls) > 0

	if firstErr != nil {
		if hasPartialContent || hasToolCalls {
			// 已有部分内容：作为 partial response 返回，不抛错
			// 追加截断标记，让用户感知到内容可能不完整
			logger.Warn("LLM streaming interrupted, returning partial response",
				zap.Error(firstErr),
				zap.Int("content_len", contentBuilder.Len()),
				zap.Int("tool_calls", len(toolCalls)))
			if hasPartialContent && !hasToolCalls {
				contentBuilder.WriteString("\n\n*[回复因网络超时被截断，以上为已接收内容]*")
			}
		} else {
			// 完全没有内容：返回哨兵错误，让上层降级到 non-streaming
			logger.Error("LLM streaming failed with no partial content",
				zap.Error(firstErr))
			return AgentMessage{}, errStreamingFailed
		}
	}

	// Build final content (thinking + content + final)
	var fullContent strings.Builder
	if thinkingBuilder.Len() > 0 {
		fullContent.WriteString("<thinking>")
		fullContent.WriteString(thinkingBuilder.String())
		fullContent.WriteString("</thinking>")
	}
	fullContent.WriteString(contentBuilder.String())
	if finalBuilder.Len() > 0 {
		fullContent.WriteString("<final>")
		fullContent.WriteString(finalBuilder.String())
		fullContent.WriteString("</final>")
	}

	response := &providers.Response{
		Content:      fullContent.String(),
		ToolCalls:    toolCalls,
		FinishReason: "stop",
	}

	logger.Info("=== LLM Streaming Response Complete ===",
		zap.Int("content_length", fullContent.Len()),
		zap.Int("tool_calls_count", len(toolCalls)))

	o.emit(NewEvent(EventMessageEnd))

	assistantMsg := convertFromProviderResponse(response)

	logger.Debug("=== streamAssistantResponse End ===",
		zap.Bool("has_tool_calls", len(toolCalls) > 0),
		zap.Int("tool_calls_count", len(toolCalls)))

	return assistantMsg, nil
}

// executeToolCalls executes tool calls with interruption support
func (o *Orchestrator) executeToolCalls(ctx context.Context, toolCalls []ToolCallContent, state *AgentState) ([]AgentMessage, []AgentMessage) {
	results := make([]AgentMessage, 0, len(toolCalls))

	logger.Info("=== Execute Tool Calls Start ===",
		zap.Int("count", len(toolCalls)))
	for _, tc := range toolCalls {
		logger.Info("Tool call start",
			zap.String("tool_id", tc.ID),
			zap.String("tool_name", tc.Name),
			zap.Any("arguments", tc.Arguments))

		// Emit tool execution start
		o.emit(NewEvent(EventToolExecutionStart).WithToolExecution(tc.ID, tc.Name, tc.Arguments))

		// Find tool
		var tool Tool
		for _, t := range state.Tools {
			if t.Name() == tc.Name {
				tool = t
				break
			}
		}

		var result ToolResult
		var err error

		if tool == nil {
			err = fmt.Errorf("tool %s not found", tc.Name)
			result = ToolResult{
				Content: []ContentBlock{TextContent{Text: fmt.Sprintf("Tool not found: %s", tc.Name)}},
				Details: map[string]any{"error": err.Error()},
			}
			logger.Error("Tool not found",
				zap.String("tool_name", tc.Name),
				zap.String("tool_id", tc.ID))
		} else {
			state.AddPendingTool(tc.ID)

			// Create context with session key and agent_id for tools to access
			toolCtx := context.WithValue(ctx, SessionKeyContextKey, state.SessionKey)
			toolCtx = context.WithValue(toolCtx, AgentIDContextKey, state.AgentID)
			toolCtx = context.WithValue(toolCtx, BootstrapOwnerContextKey, state.BootstrapOwnerID)
			toolCtx = context.WithValue(toolCtx, "session_key", state.SessionKey)
			toolCtx = context.WithValue(toolCtx, "agent_id", state.AgentID)
			toolCtx = context.WithValue(toolCtx, "bootstrap_owner_id", state.BootstrapOwnerID)

			// Add timeout for tool execution (safety net in case tool doesn't handle its own timeout)
			toolTimeout := o.config.ToolTimeout
			if toolTimeout <= 0 {
				toolTimeout = 3 * time.Minute // default 3 minutes
			}
			execCtx, execCancel := context.WithTimeout(toolCtx, toolTimeout)

			// Execute tool with streaming support in a goroutine to handle timeout properly
			resultCh := make(chan *toolResultPair, 1)
			go func() {
				r, e := tool.Execute(execCtx, tc.Arguments, func(partial ToolResult) {
					// Emit update event
					o.emit(NewEvent(EventToolExecutionUpdate).
						WithToolExecution(tc.ID, tc.Name, tc.Arguments).
						WithToolResult(&partial, false))
				})
				resultCh <- &toolResultPair{result: &r, err: e}
			}()

			// Wait for result or timeout
			select {
			case pair := <-resultCh:
				if pair.result != nil {
					result = *pair.result
				}
				err = pair.err
			case <-execCtx.Done():
				if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
					err = fmt.Errorf("tool execution timed out after %v", toolTimeout)
					logger.Error("Tool execution timeout",
						zap.String("tool_id", tc.ID),
						zap.String("tool_name", tc.Name),
						zap.Duration("timeout", toolTimeout))
				} else {
					err = execCtx.Err()
					logger.Warn("Tool execution canceled",
						zap.String("tool_id", tc.ID),
						zap.String("tool_name", tc.Name),
						zap.String("reason", err.Error()))
				}
			}

			// Cancel context immediately after use to avoid resource leak in loop
			execCancel()

			state.RemovePendingTool(tc.ID)
		}

		// Log tool execution result
		if err != nil {
			logger.Error("[❌Tool execution failed]",
				zap.String("tool_id", tc.ID),
				zap.String("tool_name", tc.Name),
				zap.Any("arguments", tc.Arguments),
				zap.Error(err))
		} else {
			// Extract content for logging
			contentText := extractToolResultContent(result.Content)
			logger.Info("[✅Tool execution success]",
				zap.String("tool_id", tc.ID),
				zap.String("tool_name", tc.Name),
				zap.Any("arguments", tc.Arguments),
				zap.Int("result_length", len(contentText)),
				zap.String("result_preview", truncateString(contentText, 200)))
		}

		// Convert result to message
		resultMsg := AgentMessage{
			Role:      RoleToolResult,
			Content:   result.Content,
			Timestamp: time.Now().UnixMilli(),
			Metadata:  map[string]any{"tool_call_id": tc.ID, "tool_name": tc.Name},
		}

		if err != nil {
			resultMsg.Metadata["error"] = err.Error()
			// 保留原始 content（可能已有部分结果），追加错误信息
			resultMsg.Content = []ContentBlock{TextContent{Text: fmt.Sprintf("[Tool Error] %s", err.Error())}}
		}

		results = append(results, resultMsg)

		// Check for use_skill and update LoadedSkills
		if tc.Name == "use_skill" && err == nil {
			if skillName, ok := tc.Arguments["skill_name"].(string); ok && skillName != "" {
				// Add to LoadedSkills if not already present
				alreadyLoaded := false
				for _, loaded := range state.LoadedSkills {
					if loaded == skillName {
						alreadyLoaded = true
						break
					}
				}
				if !alreadyLoaded {
					state.LoadedSkills = append(state.LoadedSkills, skillName)
					logger.Debug("=== Skill Loaded ===",
						zap.String("skill_name", skillName),
						zap.Int("total_loaded", len(state.LoadedSkills)),
						zap.Strings("loaded_skills", state.LoadedSkills))
				}
			}
		}

		// sessions_spawn 成功：计入待完成子 agent
		// runLoop 将在 outerCheck 持续等待，直到所有子 agent 结果通过 follow-up 队列返回
		if tc.Name == "sessions_spawn" && err == nil {
			state.AddPendingSubagent()
			logger.Info("Subagent spawned, pending count +1",
				zap.Int64("pending_subagents", atomic.LoadInt64(&state.PendingSubagents)))
		}

		// Emit tool execution end
		event := NewEvent(EventToolExecutionEnd).
			WithToolExecution(tc.ID, tc.Name, tc.Arguments).
			WithToolResult(&result, err != nil)
		o.emit(event)

		// Check for steering messages (interruption)
		steering := o.fetchSteeringMessages()
		if len(steering) > 0 {
			return results, steering
		}
	}

	logger.Debug("=== Execute Tool Calls End ===",
		zap.Int("count", len(results)))
	return results, nil
}

// emit sends an event to the event channel (non-blocking)
// If the channel is full or closed, the event is dropped to avoid blocking/panic
func (o *Orchestrator) emit(event *Event) {
	o.mu.Lock()
	ch := o.eventChan
	o.mu.Unlock()

	if ch != nil {
		select {
		case ch <- event:
			// Event sent successfully
		default:
			// Channel full, drop event to avoid blocking
			// This is acceptable as events are primarily for streaming/logging
		}
	}
}

// emitErrorEnd emits an error end event
func (o *Orchestrator) emitErrorEnd(state *AgentState, err error) {
	event := NewEvent(EventTurnEnd).WithStopReason(err.Error())
	o.emit(event)
}

// fetchSteeringMessages gets steering messages from config
func (o *Orchestrator) fetchSteeringMessages() []AgentMessage {
	if o.config.GetSteeringMessages != nil {
		msgs, _ := o.config.GetSteeringMessages()
		return msgs
	}
	// Fall back to state queue
	return o.state.DequeueSteeringMessages()
}

// fetchFollowUpMessages gets follow-up messages from config
func (o *Orchestrator) fetchFollowUpMessages() []AgentMessage {
	if o.config.GetFollowUpMessages != nil {
		msgs, _ := o.config.GetFollowUpMessages()
		return msgs
	}
	// Fall back to state queue
	return o.state.DequeueFollowUpMessages()
}

// Stop stops the orchestrator
// Safe to call multiple times
func (o *Orchestrator) Stop() {
	o.mu.Lock()
	defer o.mu.Unlock()

	if o.cancelFunc != nil {
		o.cancelFunc()
		o.cancelFunc = nil
	}
	if o.eventChan != nil {
		ch := o.eventChan
		o.eventChan = nil
		close(ch)
	}
}

// Subscribe returns the event channel
func (o *Orchestrator) Subscribe() <-chan *Event {
	return o.eventChan
}

// Helper functions

// convertToProviderMessages converts agent messages to provider messages
func convertToProviderMessages(messages []AgentMessage) []providers.Message {
	result := make([]providers.Message, 0, len(messages))

	for _, msg := range messages {
		// Skip system messages
		if msg.Role == RoleSystem {
			continue
		}

		// Skip tool messages that don't have a matching tool_call_id
		if msg.Role == RoleToolResult {
			toolCallID, hasID := msg.Metadata["tool_call_id"].(string)
			toolName, hasName := msg.Metadata["tool_name"].(string)
			if !hasID || !hasName || toolCallID == "" || toolName == "" {
				logger.Warn("Skipping tool message without tool_call_id or tool_name",
					zap.String("role", string(msg.Role)),
					zap.Bool("has_id", hasID),
					zap.Bool("has_name", hasName),
					zap.String("tool_call_id", toolCallID),
					zap.String("tool_name", toolName))
				continue
			}
		}

		providerMsg := providers.Message{
			Role: string(msg.Role),
		}

		// Extract content
		for _, block := range msg.Content {
			switch b := block.(type) {
			case TextContent:
				if providerMsg.Content != "" {
					providerMsg.Content += "\n" + b.Text
				} else {
					providerMsg.Content = b.Text
				}
			case ImageContent:
				if b.Data != "" {
					providerMsg.Images = append(providerMsg.Images, formatProviderImageDataURL(b.MimeType, b.Data))
				} else if b.URL != "" {
					providerMsg.Images = append(providerMsg.Images, b.URL)
				}
			}
		}

		// Handle tool calls for assistant messages
		if msg.Role == RoleAssistant {
			var toolCalls []providers.ToolCall
			for _, block := range msg.Content {
				if tc, ok := block.(ToolCallContent); ok {
					toolCalls = append(toolCalls, providers.ToolCall{
						ID:     tc.ID,
						Name:   tc.Name,
						Params: convertMapAnyToInterface(tc.Arguments),
					})
				}
			}
			providerMsg.ToolCalls = toolCalls
		}

		// Handle tool_call_id and tool_name for tool result messages
		if msg.Role == RoleToolResult {
			if toolCallID, ok := msg.Metadata["tool_call_id"].(string); ok {
				providerMsg.ToolCallID = toolCallID
			}
			if toolName, ok := msg.Metadata["tool_name"].(string); ok {
				providerMsg.ToolName = toolName
			}
		}

		result = append(result, providerMsg)
	}

	return result
}

func formatProviderImageDataURL(mimeType, data string) string {
	if data == "" {
		return ""
	}
	if strings.HasPrefix(data, "data:") || strings.HasPrefix(data, "http://") || strings.HasPrefix(data, "https://") {
		return data
	}

	mimeType = strings.TrimSpace(mimeType)
	if mimeType == "" {
		mimeType = "image/jpeg"
	}
	return "data:" + mimeType + ";base64," + data
}

// convertFromProviderResponse converts provider response to agent message
func convertFromProviderResponse(response *providers.Response) AgentMessage {
	content := []ContentBlock{}
	if response.Content != "" {
		content = append(content, TextContent{Text: response.Content})
	}

	// Handle tool calls
	for _, tc := range response.ToolCalls {
		content = append(content, ToolCallContent{
			ID:        tc.ID,
			Name:      tc.Name,
			Arguments: convertInterfaceToAny(tc.Params),
		})
	}

	return AgentMessage{
		Role:      RoleAssistant,
		Content:   content,
		Timestamp: time.Now().UnixMilli(),
		Metadata:  map[string]any{"stop_reason": response.FinishReason},
	}
}

// convertToToolDefinitions converts agent tools to provider tool definitions
func convertToToolDefinitions(tools []Tool) []providers.ToolDefinition {
	result := make([]providers.ToolDefinition, 0, len(tools))

	for _, tool := range tools {
		result = append(result, providers.ToolDefinition{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  convertMapAnyToInterface(tool.Parameters()),
		})
	}

	return result
}

// extractToolCalls extracts tool calls from a message
func extractToolCalls(msg AgentMessage) []ToolCallContent {
	var toolCalls []ToolCallContent

	for _, block := range msg.Content {
		if tc, ok := block.(ToolCallContent); ok {
			toolCalls = append(toolCalls, tc)
		}
	}

	return toolCalls
}

// convertInterfaceToAny converts map[string]interface{} to map[string]any
func convertInterfaceToAny(m map[string]interface{}) map[string]any {
	result := make(map[string]any)
	for k, v := range m {
		result[k] = v
	}
	return result
}

// extractToolResultContent extracts text content from tool result
func extractToolResultContent(content []ContentBlock) string {
	var result strings.Builder
	for _, block := range content {
		if text, ok := block.(TextContent); ok {
			if result.Len() > 0 {
				result.WriteString("\n")
			}
			result.WriteString(text.Text)
		}
	}
	return result.String()
}

// truncateString truncates a string to a maximum length
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen > 3 {
		return s[:maxLen-3] + "..."
	}
	return s[:maxLen]
}
