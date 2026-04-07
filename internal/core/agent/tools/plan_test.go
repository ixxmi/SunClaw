package tools

import (
	"context"
	"strings"
	"testing"

	"github.com/smallnest/goclaw/internal/core/execution"
	"github.com/smallnest/goclaw/internal/core/plan"
	"github.com/smallnest/goclaw/internal/core/task"
)

type planMemoryStore struct {
	records map[string]*plan.Record
}

func (s *planMemoryStore) Load() (map[string]*plan.Record, error) {
	loaded := make(map[string]*plan.Record, len(s.records))
	for id, record := range s.records {
		loaded[id] = clonePlanRecord(record)
	}
	return loaded, nil
}

func (s *planMemoryStore) Save(records map[string]*plan.Record) error {
	s.records = make(map[string]*plan.Record, len(records))
	for id, record := range records {
		s.records[id] = clonePlanRecord(record)
	}
	return nil
}

func clonePlanRecord(record *plan.Record) *plan.Record {
	if record == nil {
		return nil
	}
	cloned := *record
	if len(record.Steps) > 0 {
		cloned.Steps = append([]plan.Step(nil), record.Steps...)
	}
	return &cloned
}

type taskMemoryStore struct {
	records map[string]*task.Record
}

func (s *taskMemoryStore) Load() (map[string]*task.Record, error) {
	loaded := make(map[string]*task.Record, len(s.records))
	for id, record := range s.records {
		loaded[id] = cloneTaskRecord(record)
	}
	return loaded, nil
}

func (s *taskMemoryStore) Save(records map[string]*task.Record) error {
	s.records = make(map[string]*task.Record, len(records))
	for id, record := range records {
		s.records[id] = cloneTaskRecord(record)
	}
	return nil
}

func cloneTaskRecord(record *task.Record) *task.Record {
	if record == nil {
		return nil
	}
	cloned := *record
	if record.StartedAt != nil {
		startedAt := *record.StartedAt
		cloned.StartedAt = &startedAt
	}
	if record.EndedAt != nil {
		endedAt := *record.EndedAt
		cloned.EndedAt = &endedAt
	}
	if record.Result != nil {
		result := *record.Result
		cloned.Result = &result
	}
	if record.Subagent != nil {
		subagent := *record.Subagent
		if record.Subagent.RequesterOrigin != nil {
			origin := *record.Subagent.RequesterOrigin
			subagent.RequesterOrigin = &origin
		}
		cloned.Subagent = &subagent
	}
	return &cloned
}

func TestPlanUpdateToolCreatesActivePlan(t *testing.T) {
	manager := plan.NewManagerWithStore(&planMemoryStore{})
	tool := NewPlanUpdateTool(manager)
	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{
		SessionKey: "session-main",
		AgentID:    "vibecoding",
	})

	out, err := tool.Execute(ctx, map[string]interface{}{
		"goal": "完成一次多步协作",
		"steps": []interface{}{
			map[string]interface{}{
				"title": "理解代码",
				"kind":  "research",
				"goal":  "梳理现状",
			},
			map[string]interface{}{
				"title": "实现改动",
				"kind":  "implementation",
				"goal":  "完成当前实现",
			},
		},
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, `"status": "saved"`) {
		t.Fatalf("unexpected output: %s", out)
	}

	record, ok := manager.GetActiveBySession("session-main")
	if !ok {
		t.Fatalf("expected active plan")
	}
	if record.AgentID != "vibecoding" {
		t.Fatalf("unexpected agent id: %s", record.AgentID)
	}
	if len(record.Steps) != 2 {
		t.Fatalf("expected 2 steps, got %d", len(record.Steps))
	}
	if record.CurrentStepID == "" {
		t.Fatalf("expected current step")
	}
}

func TestPlanGetToolReturnsActivePlan(t *testing.T) {
	manager := plan.NewManagerWithStore(&planMemoryStore{})
	if _, err := manager.UpsertActive(&plan.Record{
		SessionKey: "session-main",
		AgentID:    "vibecoding",
		Goal:       "目标",
		Steps: []plan.Step{
			{ID: "step-1", Title: "步骤1", Goal: "做事", Kind: plan.StepKindTask},
		},
	}); err != nil {
		t.Fatalf("UpsertActive error: %v", err)
	}

	tool := NewPlanGetTool(manager)
	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	out, err := tool.Execute(ctx, map[string]interface{}{})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, `"goal": "目标"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestPlanGetToolRejectsCrossSessionPlanID(t *testing.T) {
	manager := plan.NewManagerWithStore(&planMemoryStore{})
	if _, err := manager.UpsertActive(&plan.Record{
		ID:         "plan-foreign",
		SessionKey: "session-foreign",
		AgentID:    "vibecoding",
		Goal:       "foreign",
		Steps: []plan.Step{
			{ID: "step-1", Title: "步骤1", Goal: "做事", Kind: plan.StepKindTask},
		},
	}); err != nil {
		t.Fatalf("UpsertActive error: %v", err)
	}

	tool := NewPlanGetTool(manager)
	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	out, err := tool.Execute(ctx, map[string]interface{}{"plan_id": "plan-foreign"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.TrimSpace(out) != `{"status":"not_found"}` {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestPlanUpdateToolRejectsCrossSessionPlanID(t *testing.T) {
	manager := plan.NewManagerWithStore(&planMemoryStore{})
	if _, err := manager.UpsertActive(&plan.Record{
		ID:         "plan-foreign",
		SessionKey: "session-foreign",
		AgentID:    "vibecoding",
		Goal:       "foreign",
		Steps: []plan.Step{
			{ID: "step-1", Title: "步骤1", Goal: "做事", Kind: plan.StepKindTask},
		},
	}); err != nil {
		t.Fatalf("UpsertActive error: %v", err)
	}

	tool := NewPlanUpdateTool(manager)
	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{
		SessionKey: "session-main",
		AgentID:    "vibecoding",
	})
	_, err := tool.Execute(ctx, map[string]interface{}{
		"plan_id": "plan-foreign",
		"goal":    "attempt overwrite",
		"steps": []interface{}{
			map[string]interface{}{
				"title": "理解代码",
				"goal":  "梳理现状",
			},
		},
	})
	if err == nil {
		t.Fatalf("expected cross-session update to be rejected")
	}
}

func TestTaskContinueToolInvokesHandler(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	if err := taskMgr.Create(&task.Record{
		ID:          "task-1",
		Backend:     task.BackendSubagent,
		Type:        "subagent",
		Status:      task.StatusDone,
		CanContinue: true,
		SessionKey:  "session-main",
		Subagent: &task.SubagentPayload{
			RequesterSessionKey: "session-main",
			ChildSessionKey:     "agent:coder:subagent:1",
		},
	}); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	tool := NewTaskContinueTool(taskMgr)
	tool.SetContinueHandler(func(ctx context.Context, taskID, message string) (*TaskContinueResult, error) {
		return &TaskContinueResult{
			Status:          "continued",
			PreviousTaskID:  taskID,
			TaskID:          "task-2",
			ChildSessionKey: "agent:coder:subagent:1",
		}, nil
	})

	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	out, err := tool.Execute(ctx, map[string]interface{}{
		"task_id": "task-1",
		"message": "继续处理当前步骤",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, `"status": "continued"`) || !strings.Contains(out, `"task_id": "task-2"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestTaskContinueToolRejectsCrossSessionTask(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	if err := taskMgr.Create(&task.Record{
		ID:          "task-1",
		Backend:     task.BackendSubagent,
		Type:        "subagent",
		Status:      task.StatusDone,
		CanContinue: true,
		SessionKey:  "session-foreign",
		Subagent: &task.SubagentPayload{
			RequesterSessionKey: "session-foreign",
			ChildSessionKey:     "agent:coder:subagent:1",
		},
	}); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	tool := NewTaskContinueTool(taskMgr)
	tool.SetContinueHandler(func(ctx context.Context, taskID, message string) (*TaskContinueResult, error) {
		t.Fatalf("continue handler should not be called for foreign task")
		return nil, nil
	})

	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	if _, err := tool.Execute(ctx, map[string]interface{}{
		"task_id": "task-1",
		"message": "继续处理当前步骤",
	}); err == nil {
		t.Fatalf("expected foreign task continuation to be rejected")
	}
}

func TestTaskGetToolReturnsTask(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	if err := taskMgr.Create(&task.Record{
		ID:         "task-1",
		Backend:    task.BackendSubagent,
		Type:       "subagent",
		Status:     task.StatusDone,
		Summary:    "done",
		SessionKey: "session-main",
		Subagent: &task.SubagentPayload{
			RequesterSessionKey: "session-main",
		},
	}); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	tool := NewTaskGetTool(taskMgr)
	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	out, err := tool.Execute(ctx, map[string]interface{}{"task_id": "task-1"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, `"id": "task-1"`) || !strings.Contains(out, `"status": "completed"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestTaskGetToolReturnsNotFoundForCrossSessionTask(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	if err := taskMgr.Create(&task.Record{
		ID:         "task-1",
		Backend:    task.BackendSubagent,
		Type:       "subagent",
		Status:     task.StatusDone,
		SessionKey: "session-foreign",
		Subagent: &task.SubagentPayload{
			RequesterSessionKey: "session-foreign",
		},
	}); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	tool := NewTaskGetTool(taskMgr)
	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	out, err := tool.Execute(ctx, map[string]interface{}{"task_id": "task-1"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if strings.TrimSpace(out) != `{"status":"not_found"}` {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestTaskListToolFiltersByPlan(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	for _, record := range []*task.Record{
		{ID: "task-1", Backend: task.BackendSubagent, Type: "subagent", Status: task.StatusRunning, PlanID: "plan-1", SessionKey: "session-main", Subagent: &task.SubagentPayload{RequesterSessionKey: "session-main"}},
		{ID: "task-2", Backend: task.BackendSubagent, Type: "subagent", Status: task.StatusDone, PlanID: "plan-2", SessionKey: "session-main", Subagent: &task.SubagentPayload{RequesterSessionKey: "session-main"}},
		{ID: "task-3", Backend: task.BackendSubagent, Type: "subagent", Status: task.StatusDone, PlanID: "plan-1", SessionKey: "session-foreign", Subagent: &task.SubagentPayload{RequesterSessionKey: "session-foreign"}},
	} {
		if err := taskMgr.Create(record); err != nil {
			t.Fatalf("Create error: %v", err)
		}
	}

	tool := NewTaskListTool(taskMgr)
	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	out, err := tool.Execute(ctx, map[string]interface{}{"plan_id": "plan-1"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, `"id": "task-1"`) || strings.Contains(out, `"id": "task-2"`) || strings.Contains(out, `"id": "task-3"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestTaskListToolRejectsForeignSessionKeyParam(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	tool := NewTaskListTool(taskMgr)
	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	if _, err := tool.Execute(ctx, map[string]interface{}{"session_key": "session-foreign"}); err == nil {
		t.Fatalf("expected foreign session_key param to be rejected")
	}
}

func TestTaskStopToolInvokesHandler(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	if err := taskMgr.Create(&task.Record{
		ID:         "task-1",
		Backend:    task.BackendSubagent,
		Type:       "subagent",
		Status:     task.StatusRunning,
		SessionKey: "session-main",
		Subagent: &task.SubagentPayload{
			RequesterSessionKey: "session-main",
		},
	}); err != nil {
		t.Fatalf("Create error: %v", err)
	}
	tool := NewTaskStopTool(taskMgr)
	tool.SetStopHandler(func(ctx context.Context, taskID string) (*TaskStopResult, error) {
		return &TaskStopResult{
			Status:  "stop_requested",
			TaskID:  taskID,
			Backend: "subagent",
		}, nil
	})

	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	out, err := tool.Execute(ctx, map[string]interface{}{"task_id": "task-1"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, `"status": "stop_requested"`) || !strings.Contains(out, `"task_id": "task-1"`) {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestTaskStopToolRejectsCrossSessionTask(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	if err := taskMgr.Create(&task.Record{
		ID:         "task-1",
		Backend:    task.BackendSubagent,
		Type:       "subagent",
		Status:     task.StatusRunning,
		SessionKey: "session-foreign",
		Subagent: &task.SubagentPayload{
			RequesterSessionKey: "session-foreign",
		},
	}); err != nil {
		t.Fatalf("Create error: %v", err)
	}
	tool := NewTaskStopTool(taskMgr)
	tool.SetStopHandler(func(ctx context.Context, taskID string) (*TaskStopResult, error) {
		t.Fatalf("stop handler should not be called for foreign task")
		return nil, nil
	})

	ctx := execution.WithToolUseContext(context.Background(), execution.ToolUseContext{SessionKey: "session-main"})
	if _, err := tool.Execute(ctx, map[string]interface{}{"task_id": "task-1"}); err == nil {
		t.Fatalf("expected foreign task stop to be rejected")
	}
}
