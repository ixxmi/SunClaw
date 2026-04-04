package agent

import (
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/plan"
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
	m.updatePlanStepForTask(taskID, func(planID, stepID string) error {
		if m.planManager == nil {
			return nil
		}
		return m.planManager.MarkStepRunning(planID, stepID, taskID)
	})
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
	m.updatePlanStepForTask(taskID, func(planID, stepID string) error {
		if m.planManager == nil {
			return nil
		}
		note := strings.TrimSpace(outcome.Error)
		if note == "" {
			note = strings.TrimSpace(outcome.Result)
		}
		switch mapPlanStepStatus(outcome) {
		case plan.StepDone:
			return m.planManager.MarkStepCompleted(planID, stepID, note)
		case plan.StepBlocked:
			return m.planManager.MarkStepBlocked(planID, stepID, note)
		case plan.StepFailed:
			return m.planManager.MarkStepFailed(planID, stepID, note)
		default:
			return nil
		}
	})
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
	m.updatePlanStepForTask(taskID, func(planID, stepID string) error {
		if m.planManager == nil {
			return nil
		}
		return m.planManager.MarkStepFailed(planID, stepID, err.Error())
	})
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
	case "canceled":
		return task.StatusCanceled
	case "error":
		return task.StatusFailed
	default:
		return task.StatusFailed
	}
}

func mapPlanStepStatus(outcome *SubagentRunOutcome) plan.StepStatus {
	if outcome == nil {
		return plan.StepFailed
	}

	switch strings.TrimSpace(outcome.Status) {
	case "ok":
		return plan.StepDone
	case "timeout":
		return plan.StepBlocked
	case "canceled":
		return plan.StepBlocked
	case "error":
		return plan.StepFailed
	default:
		return plan.StepFailed
	}
}

func (m *AgentManager) updatePlanStepForTask(taskID string, fn func(planID, stepID string) error) {
	if m == nil || m.taskManager == nil || fn == nil || strings.TrimSpace(taskID) == "" {
		return
	}
	record, ok := m.taskManager.Get(taskID)
	if !ok || record == nil {
		return
	}
	planID := strings.TrimSpace(record.PlanID)
	stepID := strings.TrimSpace(record.StepID)
	if planID == "" || stepID == "" {
		return
	}
	if err := fn(planID, stepID); err != nil {
		logger.Warn("Failed to update plan step from subagent task",
			zap.String("task_id", taskID),
			zap.String("plan_id", planID),
			zap.String("step_id", stepID),
			zap.Error(err))
	}
}
