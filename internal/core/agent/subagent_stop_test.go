package agent

import (
	"context"
	"testing"

	"github.com/smallnest/goclaw/internal/core/task"
)

func TestStopSubagentTaskCancelsRunningTask(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	if err := taskMgr.Create(&task.Record{
		ID:      "task-1",
		Backend: task.BackendSubagent,
		Type:    "subagent",
		Status:  task.StatusRunning,
		Subagent: &task.SubagentPayload{
			ChildSessionKey: "agent:coder:subagent:1",
		},
	}); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	called := false
	manager := &AgentManager{
		taskManager:          taskMgr,
		subagentTaskControls: map[string]*subagentTaskControl{},
	}
	manager.registerSubagentTaskControl("task-1", &subagentTaskControl{
		cancel: func() {
			called = true
		},
	})

	result, err := manager.stopSubagentTask(context.Background(), "task-1")
	if err != nil {
		t.Fatalf("stopSubagentTask error: %v", err)
	}
	if !called {
		t.Fatalf("expected cancel to be called")
	}
	if result.Status != "stop_requested" || result.TaskID != "task-1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestStopSubagentTaskRejectsNonRunningTask(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	if err := taskMgr.Create(&task.Record{
		ID:      "task-2",
		Backend: task.BackendSubagent,
		Type:    "subagent",
		Status:  task.StatusDone,
		Subagent: &task.SubagentPayload{
			ChildSessionKey: "agent:coder:subagent:2",
		},
	}); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	manager := &AgentManager{
		taskManager:          taskMgr,
		subagentTaskControls: map[string]*subagentTaskControl{},
	}

	if _, err := manager.stopSubagentTask(context.Background(), "task-2"); err == nil {
		t.Fatalf("expected error for non-running task")
	}
}
