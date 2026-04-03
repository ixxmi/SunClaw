package agent

import (
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/task"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

func buildSubagentTaskSummary(label, delegatedTask string) string {
	if trimmed := strings.TrimSpace(label); trimmed != "" {
		return trimmed
	}
	return strings.TrimSpace(delegatedTask)
}

func (m *AgentManager) markSubagentTaskRunning(taskID string, startedAt int64) {
	if m == nil || m.taskManager == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	if err := m.taskManager.MarkRunning(taskID, startedAt); err != nil {
		logger.Warn("Failed to mark subagent task running",
			zap.String("task_id", taskID),
			zap.Error(err))
	}
}

func (m *AgentManager) markSubagentTaskFinished(taskID string, outcome *SubagentRunOutcome, endedAt int64) {
	if m == nil || m.taskManager == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	if outcome == nil {
		return
	}
	if err := m.taskManager.MarkFinished(taskID, mapSubagentOutcomeStatus(outcome), outcome.Result, outcome.Error, endedAt); err != nil {
		logger.Warn("Failed to mark subagent task finished",
			zap.String("task_id", taskID),
			zap.Error(err))
	}
}

func (m *AgentManager) markSubagentTaskSpawnFailure(taskID string, err error) {
	if m == nil || m.taskManager == nil || strings.TrimSpace(taskID) == "" || err == nil {
		return
	}
	if finishErr := m.taskManager.MarkFinished(taskID, task.StatusFailed, "", err.Error(), time.Now().UnixMilli()); finishErr != nil {
		logger.Warn("Failed to mark subagent task spawn failure",
			zap.String("task_id", taskID),
			zap.Error(finishErr))
	}
}

func mapSubagentOutcomeStatus(outcome *SubagentRunOutcome) task.Status {
	if outcome == nil {
		return task.StatusFailed
	}

	switch strings.TrimSpace(outcome.Status) {
	case "ok":
		return task.StatusDone
	case "timeout":
		return task.StatusTimedOut
	case "error":
		return task.StatusFailed
	default:
		return task.StatusFailed
	}
}
