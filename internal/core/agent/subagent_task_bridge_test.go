package agent

import (
	"errors"
	"testing"

	"github.com/smallnest/goclaw/internal/core/agent/tools"
	"github.com/smallnest/goclaw/internal/core/task"
)

type fakeRegistryAdapter struct {
	registerErr error
	last        *SubagentRunParams
}

func (f *fakeRegistryAdapter) RegisterRun(params *SubagentRunParams) error {
	f.last = params
	return f.registerErr
}

func TestSubagentRegistryAdapterRegisterRunCreatesTaskRecord(t *testing.T) {
	manager := task.NewManagerWithStore(&taskMemoryStore{})
	adapter := &subagentRegistryAdapter{
		registry: &fakeRegistryAdapter{},
		tasks:    manager,
	}

	params := (&toolsSubagentRunParamsAlias{
		RunID:               "run-1",
		ChildSessionKey:     "agent:coder:subagent:1",
		RequesterSessionKey: "session-main",
		RequesterDisplayKey: "session-main",
		RequesterAgentID:    "orchestrator",
		TargetAgentID:       "coder",
		BootstrapOwnerID:    "coder",
		PlanID:              "plan-1",
		StepID:              "step-2",
		Task:                "implement current step",
		Label:               "implement-api",
		Cleanup:             "keep",
		RunTimeoutSeconds:   120,
	}).toToolParams()
	err := adapter.RegisterRun(params)
	if err != nil {
		t.Fatalf("RegisterRun error: %v", err)
	}

	record, ok := manager.Get("run-1")
	if !ok {
		t.Fatalf("expected task record")
	}
	if record.Status != task.StatusAccepted {
		t.Fatalf("unexpected status: %s", record.Status)
	}
	if record.Subagent == nil || record.Subagent.TargetAgentID != "coder" {
		t.Fatalf("unexpected subagent payload: %+v", record.Subagent)
	}
	if record.PlanID != "plan-1" || record.StepID != "step-2" {
		t.Fatalf("unexpected plan binding: %+v", record)
	}
}

func TestSubagentRegistryAdapterRegisterRunMarksTaskFailedWhenRegistryFails(t *testing.T) {
	manager := task.NewManagerWithStore(&taskMemoryStore{})
	adapter := &subagentRegistryAdapter{
		registry: &fakeRegistryAdapter{registerErr: errors.New("boom")},
		tasks:    manager,
	}

	err := adapter.RegisterRun((&toolsSubagentRunParamsAlias{
		RunID:               "run-2",
		ChildSessionKey:     "agent:coder:subagent:2",
		RequesterSessionKey: "session-main",
		RequesterDisplayKey: "session-main",
		Task:                "implement current step",
		Cleanup:             "keep",
	}).toToolParams())
	if err == nil {
		t.Fatalf("expected error")
	}

	record, ok := manager.Get("run-2")
	if !ok {
		t.Fatalf("expected failed task record")
	}
	if record.Status != task.StatusFailed {
		t.Fatalf("expected failed status, got %s", record.Status)
	}
	if record.Result == nil || record.Result.Error == "" {
		t.Fatalf("expected failure error, got %+v", record.Result)
	}
}

func TestHandleSubagentSpawnMarksTaskFailedOnEarlyError(t *testing.T) {
	manager := task.NewManagerWithStore(&taskMemoryStore{})
	if err := manager.Create(&task.Record{
		ID:      "run-3",
		Backend: task.BackendSubagent,
		Status:  task.StatusAccepted,
	}); err != nil {
		t.Fatalf("Create error: %v", err)
	}

	registry := NewSubagentRegistry(t.TempDir())
	agentManager := &AgentManager{
		taskManager:       manager,
		subagentRegistry:  registry,
		subagentAnnouncer: NewSubagentAnnouncer(nil),
	}

	if err := registry.RegisterRun(&SubagentRunParams{
		RunID:               "run-3",
		ChildSessionKey:     "bad-session-key",
		RequesterSessionKey: "session-main",
		Task:                "broken spawn",
	}); err != nil {
		t.Fatalf("RegisterRun error: %v", err)
	}

	err := agentManager.handleSubagentSpawn(&tools.SubagentSpawnResult{
		RunID:           "run-3",
		ChildSessionKey: "bad-session-key",
		Task:            "broken spawn",
	})
	if err == nil {
		t.Fatalf("expected error")
	}

	record, ok := manager.Get("run-3")
	if !ok {
		t.Fatalf("expected task record")
	}
	if record.Status != task.StatusFailed {
		t.Fatalf("expected failed task status, got %s", record.Status)
	}
	if _, ok := registry.GetRun("run-3"); ok {
		t.Fatalf("expected registry run to be released")
	}
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

type toolsSubagentRunParamsAlias struct {
	RunID               string
	ChildSessionKey     string
	RequesterSessionKey string
	RequesterDisplayKey string
	RequesterAgentID    string
	TargetAgentID       string
	BootstrapOwnerID    string
	PlanID              string
	StepID              string
	Task                string
	Label               string
	Cleanup             string
	RunTimeoutSeconds   int
}

func (p *toolsSubagentRunParamsAlias) toToolParams() *tools.SubagentRunParams {
	return &tools.SubagentRunParams{
		RunID:               p.RunID,
		ChildSessionKey:     p.ChildSessionKey,
		RequesterSessionKey: p.RequesterSessionKey,
		RequesterDisplayKey: p.RequesterDisplayKey,
		RequesterAgentID:    p.RequesterAgentID,
		TargetAgentID:       p.TargetAgentID,
		BootstrapOwnerID:    p.BootstrapOwnerID,
		PlanID:              p.PlanID,
		StepID:              p.StepID,
		Task:                p.Task,
		Label:               p.Label,
		Cleanup:             p.Cleanup,
		RunTimeoutSeconds:   p.RunTimeoutSeconds,
	}
}
