package agent

import (
	"strings"

	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

const recoveredTaskInterruptReason = "Task was interrupted because the SunClaw process stopped before completion."

func (m *AgentManager) recoverInterruptedSubagentTasks() {
	if m == nil || m.taskManager == nil {
		return
	}

	recovered, err := m.taskManager.RecoverInterrupted(recoveredTaskInterruptReason, 0)
	if err != nil {
		logger.Warn("Failed to recover interrupted subagent tasks", zap.Error(err))
		return
	}
	if len(recovered) == 0 {
		return
	}

	for _, record := range recovered {
		if record == nil || m.planManager == nil {
			continue
		}
		planID := strings.TrimSpace(record.PlanID)
		stepID := strings.TrimSpace(record.StepID)
		if planID == "" || stepID == "" {
			continue
		}
		if err := m.planManager.MarkStepBlocked(planID, stepID, recoveredTaskInterruptReason); err != nil {
			logger.Warn("Failed to mark plan step blocked for recovered task",
				zap.String("task_id", record.ID),
				zap.String("plan_id", planID),
				zap.String("step_id", stepID),
				zap.Error(err))
		}
	}

	logger.Info("Recovered interrupted subagent tasks",
		zap.Int("count", len(recovered)))
}
