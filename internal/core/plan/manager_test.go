package plan

import "testing"

type memoryStore struct {
	records map[string]*Record
}

func (s *memoryStore) Load() (map[string]*Record, error) {
	loaded := make(map[string]*Record, len(s.records))
	for id, record := range s.records {
		loaded[id] = cloneRecord(record)
	}
	return loaded, nil
}

func (s *memoryStore) Save(records map[string]*Record) error {
	s.records = make(map[string]*Record, len(records))
	for id, record := range records {
		s.records[id] = cloneRecord(record)
	}
	return nil
}

func TestManagerUpsertActiveAndAdvance(t *testing.T) {
	manager := NewManagerWithStore(&memoryStore{})

	plan, err := manager.UpsertActive(&Record{
		SessionKey: "session-main",
		AgentID:    "vibecoding",
		Goal:       "完成一个多步任务",
		Steps: []Step{
			{Title: "读代码", Goal: "理解现状", Kind: StepKindResearch},
			{Title: "实现", Goal: "改代码", Kind: StepKindImplementation},
		},
	})
	if err != nil {
		t.Fatalf("UpsertActive error: %v", err)
	}

	if plan.CurrentStepID == "" {
		t.Fatalf("expected current step")
	}
	if plan.Steps[0].Status != StepReady {
		t.Fatalf("expected first step ready, got %s", plan.Steps[0].Status)
	}

	if err := manager.MarkStepRunning(plan.ID, plan.Steps[0].ID, "task-1"); err != nil {
		t.Fatalf("MarkStepRunning error: %v", err)
	}
	if err := manager.MarkStepCompleted(plan.ID, plan.Steps[0].ID, "done"); err != nil {
		t.Fatalf("MarkStepCompleted error: %v", err)
	}

	updated, ok := manager.Get(plan.ID)
	if !ok {
		t.Fatalf("expected plan")
	}
	if updated.CurrentStepID != updated.Steps[1].ID {
		t.Fatalf("expected second step current, got %q", updated.CurrentStepID)
	}
	if updated.Steps[1].Status != StepReady {
		t.Fatalf("expected second step ready, got %s", updated.Steps[1].Status)
	}
}

func TestManagerGetActiveBySessionReturnsLatestActive(t *testing.T) {
	store := &memoryStore{
		records: map[string]*Record{
			"plan-1": {
				ID:         "plan-1",
				SessionKey: "session-main",
				Status:     StatusCanceled,
				UpdatedAt:  1,
			},
			"plan-2": {
				ID:         "plan-2",
				SessionKey: "session-main",
				Status:     StatusActive,
				UpdatedAt:  2,
			},
		},
	}
	manager := NewManagerWithStore(store)
	if err := manager.Load(); err != nil {
		t.Fatalf("Load error: %v", err)
	}

	record, ok := manager.GetActiveBySession("session-main")
	if !ok {
		t.Fatalf("expected active plan")
	}
	if record.ID != "plan-2" {
		t.Fatalf("expected latest active plan, got %s", record.ID)
	}
}

func TestManagerUpsertActiveRejectsCrossSessionPlanIDReuse(t *testing.T) {
	store := &memoryStore{
		records: map[string]*Record{
			"plan-1": {
				ID:         "plan-1",
				SessionKey: "session-a",
				Status:     StatusActive,
			},
		},
	}
	manager := NewManagerWithStore(store)
	if err := manager.Load(); err != nil {
		t.Fatalf("Load error: %v", err)
	}

	if _, err := manager.UpsertActive(&Record{
		ID:         "plan-1",
		SessionKey: "session-b",
		AgentID:    "vibecoding",
		Goal:       "other session overwrite attempt",
		Steps: []Step{
			{Title: "step", Goal: "goal", Kind: StepKindTask},
		},
	}); err == nil {
		t.Fatalf("expected cross-session overwrite to be rejected")
	}
}
