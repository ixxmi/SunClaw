package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smallnest/goclaw/internal/core/task"
)

type TaskContinueResult struct {
	Status          string `json:"status"`
	PreviousTaskID  string `json:"previous_task_id"`
	TaskID          string `json:"task_id"`
	ChildSessionKey string `json:"child_session_key,omitempty"`
}

type TaskContinueTool struct {
	taskManager  *task.Manager
	continueFunc func(ctx context.Context, taskID, message string) (*TaskContinueResult, error)
}

func NewTaskContinueTool(taskManager *task.Manager) *TaskContinueTool {
	return &TaskContinueTool{taskManager: taskManager}
}

func (t *TaskContinueTool) SetContinueHandler(fn func(ctx context.Context, taskID, message string) (*TaskContinueResult, error)) {
	t.continueFunc = fn
}

func (t *TaskContinueTool) Name() string {
	return "task_continue"
}

func (t *TaskContinueTool) Description() string {
	return "Continue a finished subagent task by reusing its child session context as a new tracked run."
}

func (t *TaskContinueTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "Existing subagent task ID to continue.",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "Follow-up instruction for the continued subagent session.",
			},
		},
		"required": []string{"task_id", "message"},
	}
}

func (t *TaskContinueTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.taskManager == nil {
		return "", fmt.Errorf("task manager is unavailable")
	}
	if t.continueFunc == nil {
		return "", fmt.Errorf("task continuation handler is unavailable")
	}

	taskID, _ := params["task_id"].(string)
	message, _ := params["message"].(string)
	taskID = strings.TrimSpace(taskID)
	message = strings.TrimSpace(message)

	if taskID == "" || message == "" {
		return "", fmt.Errorf("task_id and message are required")
	}

	record, ok := t.taskManager.Get(taskID)
	if !ok || record == nil || !task.BelongsToSession(record, taskSessionKeyFromContext(ctx)) {
		return "", fmt.Errorf("task not found: %s", taskID)
	}
	if record.Backend != task.BackendSubagent || record.Subagent == nil {
		return "", fmt.Errorf("task %s is not a subagent task", taskID)
	}
	if !record.Status.IsTerminal() {
		return "", fmt.Errorf("task %s is still running", taskID)
	}
	if !record.CanContinue {
		return "", fmt.Errorf("task %s does not support continuation", taskID)
	}

	result, err := t.continueFunc(ctx, taskID, message)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
