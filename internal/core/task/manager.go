package task

import (
	"fmt"
	"strings"
	"sync"
	"time"
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

func (m *Manager) Create(record *Record) error {
	if m == nil {
		return fmt.Errorf("task manager is nil")
	}
	if record == nil {
		return fmt.Errorf("task record is nil")
	}

	id := strings.TrimSpace(record.ID)
	if id == "" {
		return fmt.Errorf("task id is required")
	}

	cloned := cloneRecord(record)
	cloned.ID = id
	if cloned.Status == "" {
		cloned.Status = StatusAccepted
	}
	if cloned.CreatedAt == 0 {
		cloned.CreatedAt = time.Now().UnixMilli()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.records[id]; exists {
		return fmt.Errorf("task already exists: %s", id)
	}
	m.records[id] = cloned

	return m.saveLocked()
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

func (m *Manager) ListByPlan(planID string) []*Record {
	if m == nil {
		return nil
	}

	planID = strings.TrimSpace(planID)
	if planID == "" {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Record, 0)
	for _, record := range m.records {
		if strings.TrimSpace(record.PlanID) == planID {
			result = append(result, cloneRecord(record))
		}
	}

	return result
}

func (m *Manager) ListByRequester(sessionKey string) []*Record {
	if m == nil {
		return nil
	}

	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*Record, 0)
	for _, record := range m.records {
		if record.Subagent != nil && record.Subagent.RequesterSessionKey == sessionKey {
			result = append(result, cloneRecord(record))
		}
	}

	return result
}

func (m *Manager) ListBySession(sessionKey string) []*Record {
	return m.ListByRequester(sessionKey)
}

func (m *Manager) MarkRunning(id string, startedAt int64) error {
	if m == nil {
		return fmt.Errorf("task manager is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.records[strings.TrimSpace(id)]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	if startedAt == 0 {
		startedAt = time.Now().UnixMilli()
	}
	record.Status = StatusRunning
	record.StartedAt = &startedAt

	return m.saveLocked()
}

func (m *Manager) MarkFinished(id string, status Status, output string, errorText string, endedAt int64) error {
	if m == nil {
		return fmt.Errorf("task manager is nil")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	record, ok := m.records[strings.TrimSpace(id)]
	if !ok {
		return fmt.Errorf("task not found: %s", id)
	}

	if endedAt == 0 {
		endedAt = time.Now().UnixMilli()
	}
	if record.StartedAt == nil {
		record.StartedAt = &endedAt
	}

	record.Status = status
	record.EndedAt = &endedAt
	record.Result = &Result{
		Output: strings.TrimSpace(output),
		Error:  strings.TrimSpace(errorText),
	}

	return m.saveLocked()
}

func (m *Manager) RecoverInterrupted(reason string, endedAt int64) ([]*Record, error) {
	if m == nil {
		return nil, fmt.Errorf("task manager is nil")
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "Task was interrupted before completion."
	}
	if endedAt == 0 {
		endedAt = time.Now().UnixMilli()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	recovered := make([]*Record, 0)
	for _, record := range m.records {
		if record == nil {
			continue
		}

		switch record.Status {
		case StatusAccepted, StatusRunning:
			previousStatus := record.Status
			record.Status = StatusInterrupted
			record.EndedAt = &endedAt
			if record.StartedAt == nil && previousStatus == StatusRunning {
				record.StartedAt = &endedAt
			}
			record.Result = &Result{
				Error: reason,
			}
			recovered = append(recovered, cloneRecord(record))
		}
	}

	if len(recovered) == 0 {
		return nil, nil
	}
	if err := m.saveLocked(); err != nil {
		return nil, err
	}

	return recovered, nil
}

func (m *Manager) Count() int {
	if m == nil {
		return 0
	}

	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.records)
}

func (m *Manager) saveLocked() error {
	if m.store == nil {
		return nil
	}
	return m.store.Save(m.records)
}
