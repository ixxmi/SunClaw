package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/smallnest/goclaw/internal/core/agent/tools"
	"github.com/smallnest/goclaw/internal/core/execution"
	"github.com/smallnest/goclaw/internal/core/task"
)

func (m *AgentManager) continueSubagentTask(ctx context.Context, taskID, message string) (*tools.TaskContinueResult, error) {
	if m == nil || m.taskManager == nil {
		return nil, fmt.Errorf("task manager is unavailable")
	}

	taskID = strings.TrimSpace(taskID)
	message = strings.TrimSpace(message)
	if taskID == "" || message == "" {
		return nil, fmt.Errorf("task_id and message are required")
	}

	record, ok := m.taskManager.Get(taskID)
	if !ok || record == nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if record.Backend != task.BackendSubagent || record.Subagent == nil {
		return nil, fmt.Errorf("task %s is not a subagent task", taskID)
	}
	if !record.Status.IsTerminal() {
		return nil, fmt.Errorf("task %s is still running", taskID)
	}

	childSessionKey := strings.TrimSpace(record.Subagent.ChildSessionKey)
	if childSessionKey == "" {
		return nil, fmt.Errorf("task %s has no child session key", taskID)
	}

	targetAgentID := strings.TrimSpace(record.Subagent.TargetAgentID)
	if targetAgentID == "" {
		targetAgentID, _, _ = ParseAgentSessionKey(childSessionKey)
	}
	if targetAgentID == "" {
		return nil, fmt.Errorf("task %s has no target agent id", taskID)
	}

	requesterSessionKey := strings.TrimSpace(record.Subagent.RequesterSessionKey)
	if requesterSessionKey == "" {
		requesterSessionKey = strings.TrimSpace(execution.SessionKey(ctx))
	}
	requesterAgentID := strings.TrimSpace(record.Subagent.RequesterAgentID)
	if requesterAgentID == "" {
		requesterAgentID = strings.TrimSpace(execution.AgentID(ctx))
	}
	bootstrapOwnerID := strings.TrimSpace(record.Subagent.BootstrapOwnerID)
	if bootstrapOwnerID == "" {
		bootstrapOwnerID = targetAgentID
	}

	var requesterOrigin *tools.DeliveryContext
	if record.Subagent.RequesterOrigin != nil {
		requesterOrigin = &tools.DeliveryContext{
			Channel:   record.Subagent.RequesterOrigin.Channel,
			AccountID: record.Subagent.RequesterOrigin.AccountID,
			To:        record.Subagent.RequesterOrigin.To,
			ThreadID:  record.Subagent.RequesterOrigin.ThreadID,
		}
	}

	runID := GenerateRunID()
	timeoutSeconds := record.Subagent.TimeoutSeconds
	cleanup := strings.TrimSpace(record.Subagent.Cleanup)
	if cleanup == "" {
		cleanup = "keep"
	}

	childSystemPrompt := tools.BuildSubagentSystemPrompt(&tools.SubagentSystemPromptParams{
		RequesterSessionKey: requesterSessionKey,
		RequesterOrigin:     requesterOrigin,
		ChildSessionKey:     childSessionKey,
		Label:               record.Subagent.Label,
		Task:                message,
		TargetAgentID:       targetAgentID,
		AllowSubagentSpawn:  m.canTargetAgentSpawn(targetAgentID),
	})

	archiveAfterMinutes := 60
	if m.cfg != nil && m.cfg.Agents.Defaults.Subagents != nil && m.cfg.Agents.Defaults.Subagents.ArchiveAfterMinutes > 0 {
		archiveAfterMinutes = m.cfg.Agents.Defaults.Subagents.ArchiveAfterMinutes
	}

	adapter := &subagentRegistryAdapter{
		registry: m.subagentRegistry,
		tasks:    m.taskManager,
	}
	if err := adapter.RegisterRun(&tools.SubagentRunParams{
		RunID:               runID,
		ChildSessionKey:     childSessionKey,
		RequesterSessionKey: requesterSessionKey,
		RequesterOrigin:     requesterOrigin,
		RequesterDisplayKey: requesterSessionKey,
		RequesterAgentID:    requesterAgentID,
		TargetAgentID:       targetAgentID,
		BootstrapOwnerID:    bootstrapOwnerID,
		PlanID:              record.PlanID,
		StepID:              record.StepID,
		ContinueOf:          record.ID,
		Task:                message,
		Cleanup:             cleanup,
		Label:               record.Subagent.Label,
		ArchiveAfterMinutes: archiveAfterMinutes,
		RunTimeoutSeconds:   timeoutSeconds,
	}); err != nil {
		return nil, err
	}

	sessionMgr, _, err := m.sessionManagerForSessionKey(childSessionKey)
	if err != nil {
		return nil, err
	}
	sess, err := sessionMgr.GetOrCreate(childSessionKey)
	if err != nil {
		return nil, err
	}

	maxHistory := 100
	if m.cfg != nil && m.cfg.Agents.Defaults.MaxHistoryMessages > 0 {
		maxHistory = m.cfg.Agents.Defaults.MaxHistoryMessages
	}
	historyMessages := sessionMessagesToAgentMessages(sess.GetHistorySafe(maxHistory))

	if err := m.runSubagentTask(&tools.SubagentSpawnResult{
		Status:            "accepted",
		ChildSessionKey:   childSessionKey,
		RunID:             runID,
		RunTimeoutSeconds: timeoutSeconds,
		TargetAgentID:     targetAgentID,
		ChildSystemPrompt: childSystemPrompt,
		Task:              message,
		BootstrapOwnerID:  bootstrapOwnerID,
		PlanID:            record.PlanID,
		StepID:            record.StepID,
		ContinueOf:        record.ID,
	}, historyMessages); err != nil {
		return nil, err
	}

	return &tools.TaskContinueResult{
		Status:          "continued",
		PreviousTaskID:  record.ID,
		TaskID:          runID,
		ChildSessionKey: childSessionKey,
	}, nil
}

func (m *AgentManager) canTargetAgentSpawn(agentID string) bool {
	if m == nil || m.cfg == nil {
		return false
	}
	agentCfg := m.lookupAgentConfig(agentID)
	if agentCfg == nil || agentCfg.Subagents == nil {
		return false
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

func (m *AgentManager) registerSubagentTaskControl(taskID string, control *subagentTaskControl) {
	if m == nil || strings.TrimSpace(taskID) == "" || control == nil {
		return
	}
	m.subagentTaskControlsMu.Lock()
	defer m.subagentTaskControlsMu.Unlock()
	m.subagentTaskControls[strings.TrimSpace(taskID)] = control
}

func (m *AgentManager) releaseSubagentTaskControl(taskID string) {
	if m == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	m.subagentTaskControlsMu.Lock()
	defer m.subagentTaskControlsMu.Unlock()
	delete(m.subagentTaskControls, strings.TrimSpace(taskID))
}

func (m *AgentManager) stopSubagentTask(ctx context.Context, taskID string) (*tools.TaskStopResult, error) {
	if m == nil || m.taskManager == nil {
		return nil, fmt.Errorf("task manager is unavailable")
	}

	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	record, ok := m.taskManager.Get(taskID)
	if !ok || record == nil {
		return nil, fmt.Errorf("task not found: %s", taskID)
	}
	if record.Backend != task.BackendSubagent || record.Subagent == nil {
		return nil, fmt.Errorf("task %s is not a subagent task", taskID)
	}
	if record.Status != task.StatusRunning {
		return nil, fmt.Errorf("task %s is not running", taskID)
	}

	m.subagentTaskControlsMu.Lock()
	control := m.subagentTaskControls[taskID]
	m.subagentTaskControlsMu.Unlock()
	if control == nil || control.cancel == nil {
		return nil, fmt.Errorf("task %s has no active stop handle", taskID)
	}

	control.cancel()

	return &tools.TaskStopResult{
		Status:          "stop_requested",
		TaskID:          taskID,
		Backend:         string(record.Backend),
		ChildSessionKey: strings.TrimSpace(record.Subagent.ChildSessionKey),
	}, nil
}
