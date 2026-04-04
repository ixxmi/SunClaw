package agent

import (
	"testing"

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
		cloned.Steps = make([]plan.Step, len(record.Steps))
		copy(cloned.Steps, record.Steps)
	}
	return &cloned
}

func TestRecoverInterruptedSubagentTasksMarksPlanStepBlocked(t *testing.T) {
	taskMgr := task.NewManagerWithStore(&taskMemoryStore{})
	planMgr := plan.NewManagerWithStore(&planMemoryStore{})

	if err := taskMgr.Create(&task.Record{
		ID:      "task-1",
		Backend: task.BackendSubagent,
		Type:    "subagent",
		Status:  task.StatusRunning,
		PlanID:  "plan-1",
		StepID:  "step-1",
	}); err != nil {
		t.Fatalf("Create task error: %v", err)
	}

	if _, err := planMgr.UpsertActive(&plan.Record{
		ID:         "plan-1",
		SessionKey: "session-main",
		AgentID:    "vibecoding",
		Goal:       "finish task",
		Steps: []plan.Step{
			{
				ID:     "step-1",
				Title:  "Implement",
				Kind:   plan.StepKindImplementation,
				Goal:   "finish current step",
				Status: plan.StepRunning,
				TaskID: "task-1",
			},
		},
		CurrentStepID: "step-1",
	}); err != nil {
		t.Fatalf("UpsertActive error: %v", err)
	}

	manager := &AgentManager{
		taskManager: taskMgr,
		planManager: planMgr,
	}

	manager.recoverInterruptedSubagentTasks()

	record, ok := taskMgr.Get("task-1")
	if !ok {
		t.Fatalf("expected recovered task")
	}
	if record.Status != task.StatusInterrupted {
		t.Fatalf("expected interrupted task status, got %s", record.Status)
	}
	if record.Result == nil || record.Result.Error != recoveredTaskInterruptReason {
		t.Fatalf("unexpected task result: %+v", record.Result)
	}

	planRecord, ok := planMgr.Get("plan-1")
	if !ok {
		t.Fatalf("expected plan record")
	}
	if planRecord.Status != plan.StatusBlocked {
		t.Fatalf("expected blocked plan status, got %s", planRecord.Status)
	}
	if len(planRecord.Steps) != 1 || planRecord.Steps[0].Status != plan.StepBlocked {
		t.Fatalf("expected blocked step, got %+v", planRecord.Steps)
	}
	if planRecord.Steps[0].Notes != recoveredTaskInterruptReason {
		t.Fatalf("unexpected step note: %q", planRecord.Steps[0].Notes)
	}
}
