package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/smallnest/goclaw/internal/core/execution"
	"github.com/smallnest/goclaw/internal/core/permissions"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// executeToolCalls executes tool calls with interruption support.
//
// Extracted from orchestrator.go so the main query loop can stay focused on
// turn-state transitions. Behavior is intentionally unchanged.
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
			spec := ResolveToolSpec(tool)
			policyDecision := permissions.EvaluateToolCall(o.config.PermissionPolicy, tc.Name, tc.Arguments, spec)
			if !policyDecision.Allowed() {
				err = fmt.Errorf("%s", policyDecision.Reason)
				result = ToolResult{
					Content: []ContentBlock{TextContent{Text: fmt.Sprintf("[Permission Denied] %s", policyDecision.Reason)}},
					Details: map[string]any{
						"error":             err.Error(),
						"permission_denied": true,
						"matched_rule":      policyDecision.MatchedRule,
						"tool_risk":         string(policyDecision.Spec.Risk),
						"tool_mutation":     string(policyDecision.Spec.Mutation),
					},
				}
				logger.Warn("Tool execution denied by policy",
					zap.String("tool_id", tc.ID),
					zap.String("tool_name", tc.Name),
					zap.String("reason", policyDecision.Reason),
					zap.String("matched_rule", policyDecision.MatchedRule))
			} else {
				if policyDecision.RequiresApproval {
					logger.Info("Tool marked as requiring approval by policy",
						zap.String("tool_id", tc.ID),
						zap.String("tool_name", tc.Name),
						zap.String("reason", policyDecision.Reason))
				}

				state.AddPendingTool(tc.ID)

				toolCtx := execution.WithToolUseContext(ctx, execution.ToolUseContext{
					SessionKey:       state.SessionKey,
					AgentID:          state.AgentID,
					BootstrapOwnerID: state.BootstrapOwnerID,
					WorkspaceRoot:    state.WorkspaceRoot,
					LoopIteration:    state.LLMCallCount,
				})
				toolCtx = context.WithValue(toolCtx, SessionKeyContextKey, state.SessionKey)
				toolCtx = context.WithValue(toolCtx, AgentIDContextKey, state.AgentID)
				toolCtx = context.WithValue(toolCtx, BootstrapOwnerContextKey, state.BootstrapOwnerID)

				toolTimeout := o.config.ToolTimeout
				if toolTimeout <= 0 {
					toolTimeout = 3 * time.Minute
				}
				execCtx, execCancel := context.WithTimeout(toolCtx, toolTimeout)

				resultCh := make(chan *toolResultPair, 1)
				go func() {
					r, e := tool.Execute(execCtx, tc.Arguments, func(partial ToolResult) {
						o.emit(NewEvent(EventToolExecutionUpdate).
							WithToolExecution(tc.ID, tc.Name, tc.Arguments).
							WithToolResult(&partial, false))
					})
					resultCh <- &toolResultPair{result: &r, err: e}
				}()

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

				execCancel()
				state.RemovePendingTool(tc.ID)
			}
		}

		result.Content = truncateToolResultBlocks(result.Content, toolResultCharBudget(tc.Name))
		if err != nil {
			logger.Error("[❌Tool execution failed]",
				zap.String("tool_id", tc.ID),
				zap.String("tool_name", tc.Name),
				zap.Any("arguments", tc.Arguments),
				zap.Error(err))
		} else {
			contentText := extractToolResultContent(result.Content)
			logger.Info("[✅Tool execution success]",
				zap.String("tool_id", tc.ID),
				zap.String("tool_name", tc.Name),
				zap.Any("arguments", tc.Arguments),
				zap.Int("result_length", len(contentText)),
				zap.String("result_preview", truncateString(contentText, 200)))
		}

		resultMsg := AgentMessage{
			Role:      RoleToolResult,
			Content:   result.Content,
			Timestamp: time.Now().UnixMilli(),
			Metadata:  map[string]any{"tool_call_id": tc.ID, "tool_name": tc.Name},
		}

		if err != nil {
			resultMsg.Metadata["error"] = err.Error()
			if denied, _ := result.Details["permission_denied"].(bool); denied {
				resultMsg.Metadata["permission_denied"] = true
				if matchedRule, _ := result.Details["matched_rule"].(string); matchedRule != "" {
					resultMsg.Metadata["matched_rule"] = matchedRule
				}
			} else {
				resultMsg.Content = truncateToolResultBlocks([]ContentBlock{
					TextContent{Text: fmt.Sprintf("[Tool Error] %s", err.Error())},
				}, toolResultCharBudget(tc.Name))
			}
		}

		if o.config.ShrimpBrain != nil && strings.TrimSpace(state.SessionKey) != "" {
			if tc.Name != "sessions_spawn" || err != nil {
				o.config.ShrimpBrain.RecordToolCall(
					state.SessionKey,
					state.AgentID,
					state.IsSubagent,
					state.LLMCallCount,
					tc.ID,
					tc.Name,
					tc.Arguments,
					extractToolResultContent(resultMsg.Content),
					func() string {
						if err != nil {
							return err.Error()
						}
						return ""
					}(),
				)
			}
		}

		results = append(results, resultMsg)

		if tc.Name == "use_skill" && err == nil {
			if skillName, ok := tc.Arguments["skill_name"].(string); ok && skillName != "" {
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

		if shouldTrackPendingSubagent(tc.Name, result, err) {
			state.AddPendingSubagent()
			logger.Info("Subagent spawned, pending count +1",
				zap.Int64("pending_subagents", atomic.LoadInt64(&state.PendingSubagents)))
		}

		event := NewEvent(EventToolExecutionEnd).
			WithToolExecution(tc.ID, tc.Name, tc.Arguments).
			WithToolResult(&result, err != nil)
		o.emit(event)

		steering := o.fetchSteeringMessages()
		if len(steering) > 0 {
			return results, steering
		}
	}

	logger.Debug("=== Execute Tool Calls End ===",
		zap.Int("count", len(results)))
	return results, nil
}

func shouldTrackPendingSubagent(toolName string, result ToolResult, err error) bool {
	if (toolName != "sessions_spawn" && toolName != "task_continue") || err != nil {
		return false
	}

	text := strings.TrimSpace(extractToolResultContent(result.Content))
	return strings.HasPrefix(text, "Subagent spawned successfully.") || strings.Contains(text, `"status": "continued"`) || strings.Contains(text, `"status":"continued"`)
}
