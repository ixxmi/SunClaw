package task

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

func TestManagerCreateRunAndFinish(t *testing.T) {
	store := &memoryStore{}
	manager := NewManagerWithStore(store)

	err := manager.Create(&Record{
		ID:      "task-1",
		Backend: BackendSubagent,
		Status:  StatusAccepted,
		Summary: "review current change",
		Subagent: &SubagentPayload{
			RequesterSessionKey: "session-main",
			ChildSessionKey:     "agent:reviewer:subagent:1",
			Task:                "review current change",
		},
	})
	if err != nil {
		t.Fatalf("Create error: %v", err)
	}

	if manager.Count() != 1 {
		t.Fatalf("expected 1 task, got %d", manager.Count())
	}

	if err := manager.MarkRunning("task-1", 1000); err != nil {
		t.Fatalf("MarkRunning error: %v", err)
	}
	if err := manager.MarkFinished("task-1", StatusDone, "done", "", 2000); err != nil {
		t.Fatalf("MarkFinished error: %v", err)
	}

	record, ok := manager.Get("task-1")
	if !ok {
		t.Fatalf("expected task to exist")
	}
	if record.Status != StatusDone {
		t.Fatalf("expected completed status, got %s", record.Status)
	}
	if record.StartedAt == nil || *record.StartedAt != 1000 {
		t.Fatalf("unexpected startedAt: %+v", record.StartedAt)
	}
	if record.EndedAt == nil || *record.EndedAt != 2000 {
		t.Fatalf("unexpected endedAt: %+v", record.EndedAt)
	}
	if record.Result == nil || record.Result.Output != "done" {
		t.Fatalf("unexpected result: %+v", record.Result)
	}
}

func TestManagerLoadRestoresSavedRecords(t *testing.T) {
	store := &memoryStore{
		records: map[string]*Record{
			"task-2": {
				ID:      "task-2",
				Backend: BackendSubagent,
				Status:  StatusFailed,
				Subagent: &SubagentPayload{
					RequesterSessionKey: "session-main",
				},
			},
		},
	}
	manager := NewManagerWithStore(store)

	if err := manager.Load(); err != nil {
		t.Fatalf("Load error: %v", err)
	}

	record, ok := manager.Get("task-2")
	if !ok {
		t.Fatalf("expected restored task")
	}
	if record.Status != StatusFailed {
		t.Fatalf("unexpected status: %s", record.Status)
	}

	list := manager.ListByRequester("session-main")
	if len(list) != 1 {
		t.Fatalf("expected 1 requester task, got %d", len(list))
	}
}
