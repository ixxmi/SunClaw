package plan

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Manager struct {
	mu      sync.RWMutex
	records map[string]*Record
	store   Store
}

func NewManager(dataDir string) *Manager {
	return NewManagerWithStore(NewFileStore(dataDir))
}

func NewManagerWithStore(store Store) *Manager {
	return &Manager{
		records: make(map[string]*Record),
		store:   store,
	}
}

func (m *Manager) Load() error {
	if m == nil || m.store == nil {
		return nil
	}

	loaded, err := m.store.Load()
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.records = make(map[string]*Record, len(loaded))
	for id, record := range loaded {
		m.records[id] = cloneRecord(record)
	}

	return nil
}

func (m *Manager) UpsertActive(record *Record) (*Record, error) {
	if m == nil {
		return nil, fmt.Errorf("plan manager is nil")
	}
	if record == nil {
		return nil, fmt.Errorf("plan record is nil")
	}

	now := time.Now().UnixMilli()
	normalized := normalizeRecord(record, now)

	m.mu.Lock()
	defer m.mu.Unlock()

	if existing, ok := m.records[normalized.ID]; ok && existing != nil && existing.SessionKey != normalized.SessionKey {
		return nil, fmt.Errorf("plan %s does not belong to session %s", normalized.ID, normalized.SessionKey)
	}

	for id, existing := range m.records {
		if existing == nil || existing.SessionKey != normalized.SessionKey || id == normalized.ID {
			continue
		}
		if existing.Status == StatusActive || existing.Status == StatusDraft {
			existing.Status = StatusCanceled
			existing.UpdatedAt = now
		}
	}

	m.records[normalized.ID] = normalized
	if err := m.saveLocked(); err != nil {
		return nil, err
	}

	return cloneRecord(normalized), nil
}

func (m *Manager) Get(id string) (*Record, bool) {
	if m == nil {
		return nil, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	record, ok := m.records[strings.TrimSpace(id)]
	if !ok {
		return nil, false
	}
	return cloneRecord(record), true
}

func (m *Manager) GetActiveBySession(sessionKey string) (*Record, bool) {
	if m == nil {
		return nil, false
	}

	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil, false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var latest *Record
	for _, record := range m.records {
		if record == nil || record.SessionKey != sessionKey {
			continue
		}
		if record.Status != StatusActive && record.Status != StatusDraft {
			continue
		}
		if latest == nil || record.UpdatedAt > latest.UpdatedAt {
			latest = record
		}
	}
	if latest == nil {
		return nil, false
	}
	return cloneRecord(latest), true
}

func (m *Manager) MarkStepRunning(planID, stepID, taskID string) error {
	return m.updateStep(planID, stepID, func(record *Record, step *Step, now int64) {
		step.Status = StepRunning
		step.TaskID = strings.TrimSpace(taskID)
		record.Status = StatusActive
		record.CurrentStepID = step.ID
		record.LastDecision = "step_running"
	})
}

func (m *Manager) MarkStepCompleted(planID, stepID, note string) error {
	return m.updateStep(planID, stepID, func(record *Record, step *Step, now int64) {
		step.Status = StepDone
		step.Notes = strings.TrimSpace(note)
		record.LastDecision = "step_completed"
		advanceToNextStep(record)
	})
}

func (m *Manager) MarkStepBlocked(planID, stepID, note string) error {
	return m.updateStep(planID, stepID, func(record *Record, step *Step, now int64) {
		step.Status = StepBlocked
		step.Notes = strings.TrimSpace(note)
		record.Status = StatusBlocked
		record.CurrentStepID = step.ID
		record.LastDecision = "step_blocked"
	})
}

func (m *Manager) MarkStepFailed(planID, stepID, note string) error {
	return m.updateStep(planID, stepID, func(record *Record, step *Step, now int64) {
		step.Status = StepFailed
		step.Notes = strings.TrimSpace(note)
		record.Status = StatusBlocked
		record.CurrentStepID = step.ID
		record.LastDecision = "step_failed"
	})
}

func (m *Manager) updateStep(planID, stepID string, fn func(record *Record, step *Step, now int64)) error {
	if m == nil {
		return fmt.Errorf("plan manager is nil")
	}

	planID = strings.TrimSpace(planID)
	stepID = strings.TrimSpace(stepID)
	if planID == "" || stepID == "" {
		return fmt.Errorf("plan_id and step_id are required")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.records[planID]
	if !ok {
		return fmt.Errorf("plan not found: %s", planID)
	}

	for i := range record.Steps {
		if record.Steps[i].ID != stepID {
			continue
		}
		now := time.Now().UnixMilli()
		fn(record, &record.Steps[i], now)
		record.UpdatedAt = now
		return m.saveLocked()
	}

	return fmt.Errorf("step not found: %s", stepID)
}

func (m *Manager) saveLocked() error {
	if m.store == nil {
		return nil
	}
	return m.store.Save(m.records)
}

func normalizeRecord(record *Record, now int64) *Record {
	cloned := cloneRecord(record)
	cloned.ID = strings.TrimSpace(cloned.ID)
	if cloned.ID == "" {
		cloned.ID = uuid.NewString()
	}
	cloned.SessionKey = strings.TrimSpace(cloned.SessionKey)
	cloned.AgentID = strings.TrimSpace(cloned.AgentID)
	cloned.Goal = strings.TrimSpace(cloned.Goal)
	cloned.LastDecision = strings.TrimSpace(cloned.LastDecision)
	if cloned.Status == "" {
		cloned.Status = StatusActive
	}
	if cloned.CreatedAt == 0 {
		cloned.CreatedAt = now
	}
	cloned.UpdatedAt = now

	if len(cloned.Steps) == 0 {
		cloned.CurrentStepID = ""
		return cloned
	}

	for i := range cloned.Steps {
		if cloned.Steps[i].ID == "" {
			cloned.Steps[i].ID = fmt.Sprintf("step-%d", i+1)
		}
		if cloned.Steps[i].Kind == "" {
			cloned.Steps[i].Kind = StepKindTask
		}
	}

	current := strings.TrimSpace(cloned.CurrentStepID)
	if current == "" || !recordHasStep(cloned, current) {
		for i := range cloned.Steps {
			if !cloned.Steps[i].Status.IsTerminal() {
				current = cloned.Steps[i].ID
				break
			}
		}
		if current == "" {
			current = cloned.Steps[0].ID
		}
	}
	cloned.CurrentStepID = current

	hasExplicitStatus := false
	for i := range cloned.Steps {
		if cloned.Steps[i].Status != "" {
			hasExplicitStatus = true
			break
		}
	}

	for i := range cloned.Steps {
		if cloned.Steps[i].Status != "" {
			continue
		}
		if !hasExplicitStatus && cloned.Steps[i].ID == current {
			cloned.Steps[i].Status = StepReady
		} else {
			cloned.Steps[i].Status = StepPending
		}
	}

	if allStepsTerminal(cloned) {
		cloned.Status = StatusCompleted
		cloned.CurrentStepID = ""
	}

	return cloned
}

func recordHasStep(record *Record, stepID string) bool {
	for _, step := range record.Steps {
		if step.ID == stepID {
			return true
		}
	}
	return false
}

func allStepsTerminal(record *Record) bool {
	if record == nil || len(record.Steps) == 0 {
		return false
	}
	for _, step := range record.Steps {
		if !step.Status.IsTerminal() {
			return false
		}
	}
	return true
}

func advanceToNextStep(record *Record) {
	if record == nil {
		return
	}
	for i := range record.Steps {
		if record.Steps[i].Status == StepPending || record.Steps[i].Status == StepReady {
			record.Steps[i].Status = StepReady
			record.CurrentStepID = record.Steps[i].ID
			record.Status = StatusActive
			return
		}
	}
	record.CurrentStepID = ""
	record.Status = StatusCompleted
}
