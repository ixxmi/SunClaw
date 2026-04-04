package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/smallnest/goclaw/internal/core/execution"
	"github.com/smallnest/goclaw/internal/core/task"
)

type TaskGetTool struct {
	taskManager *task.Manager
}

func NewTaskGetTool(taskManager *task.Manager) *TaskGetTool {
	return &TaskGetTool{taskManager: taskManager}
}

func (t *TaskGetTool) Name() string {
	return "task_get"
}

func (t *TaskGetTool) Description() string {
	return "Get a tracked task by ID, including backend, status, bindings, and result."
}

func (t *TaskGetTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "Tracked task ID.",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskGetTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.taskManager == nil {
		return "", fmt.Errorf("task manager is unavailable")
	}

	taskID, _ := params["task_id"].(string)
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	record, ok := t.taskManager.Get(taskID)
	if !ok || record == nil {
		return `{"status":"not_found"}`, nil
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type TaskListTool struct {
	taskManager *task.Manager
}

func NewTaskListTool(taskManager *task.Manager) *TaskListTool {
	return &TaskListTool{taskManager: taskManager}
}

func (t *TaskListTool) Name() string {
	return "task_list"
}

func (t *TaskListTool) Description() string {
	return "List tracked tasks for the current session, optionally filtered by plan ID or status."
}

func (t *TaskListTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"plan_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional plan ID filter.",
			},
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Optional explicit session key filter. Defaults to current session.",
			},
			"status": map[string]interface{}{
				"type":        "string",
				"description": "Optional task status filter.",
			},
		},
	}
}

func (t *TaskListTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.taskManager == nil {
		return "", fmt.Errorf("task manager is unavailable")
	}

	planID, _ := params["plan_id"].(string)
	sessionKey, _ := params["session_key"].(string)
	statusFilter, _ := params["status"].(string)
	planID = strings.TrimSpace(planID)
	sessionKey = strings.TrimSpace(sessionKey)
	statusFilter = strings.TrimSpace(statusFilter)

	var records []*task.Record
	if planID != "" {
		records = t.taskManager.ListByPlan(planID)
	} else {
		if sessionKey == "" {
			sessionKey = strings.TrimSpace(execution.SessionKey(ctx))
		}
		if sessionKey == "" {
			sessionKey = "main"
		}
		records = t.taskManager.ListBySession(sessionKey)
	}

	if statusFilter != "" {
		filtered := make([]*task.Record, 0, len(records))
		for _, record := range records {
			if record != nil && strings.EqualFold(string(record.Status), statusFilter) {
				filtered = append(filtered, record)
			}
		}
		records = filtered
	}

	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type TaskStopResult struct {
	Status          string `json:"status"`
	TaskID          string `json:"task_id"`
	Backend         string `json:"backend"`
	ChildSessionKey string `json:"child_session_key,omitempty"`
}

type TaskStopTool struct {
	taskManager *task.Manager
	stopFunc    func(ctx context.Context, taskID string) (*TaskStopResult, error)
}

func NewTaskStopTool(taskManager *task.Manager) *TaskStopTool {
	return &TaskStopTool{taskManager: taskManager}
}

func (t *TaskStopTool) SetStopHandler(fn func(ctx context.Context, taskID string) (*TaskStopResult, error)) {
	t.stopFunc = fn
}

func (t *TaskStopTool) Name() string {
	return "task_stop"
}

func (t *TaskStopTool) Description() string {
	return "Request cancellation of a running tracked task."
}

func (t *TaskStopTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "Tracked task ID.",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *TaskStopTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.taskManager == nil {
		return "", fmt.Errorf("task manager is unavailable")
	}
	if t.stopFunc == nil {
		return "", fmt.Errorf("task stop handler is unavailable")
	}

	taskID, _ := params["task_id"].(string)
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	result, err := t.stopFunc(ctx, taskID)
	if err != nil {
		return "", err
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}
