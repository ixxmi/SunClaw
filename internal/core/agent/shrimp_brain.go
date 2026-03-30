package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

const (
	defaultShrimpBrainMaxRuns   = 12
	defaultShrimpBrainMaxEvents = 80
)

// ShrimpBrainSnapshot 是“虾脑”团队协作观测面板的数据快照。
type ShrimpBrainSnapshot struct {
	Available   bool             `json:"available"`
	GeneratedAt string           `json:"generatedAt"`
	TeamName    string           `json:"teamName"`
	ActiveRuns  int              `json:"activeRuns"`
	Note        string           `json:"note,omitempty"`
	Runs        []ShrimpBrainRun `json:"runs"`
}

// ShrimpBrainRun 表示一次用户任务在团队中的完整处理链路。
type ShrimpBrainRun struct {
	ID           string                `json:"id"`
	BlockKey     string                `json:"blockKey"`
	UserKey      string                `json:"userKey"`
	SessionKey   string                `json:"sessionKey"`
	Channel      string                `json:"channel"`
	ChatID       string                `json:"chatId"`
	MainAgentID  string                `json:"mainAgentId"`
	UserRequest  string                `json:"userRequest"`
	Status       string                `json:"status"`
	StartedAt    int64                 `json:"startedAt"`
	UpdatedAt    int64                 `json:"updatedAt"`
	CompletedAt  *int64                `json:"completedAt,omitempty"`
	MainPrompt   string                `json:"mainPrompt,omitempty"`
	MainPromptAt int64                 `json:"mainPromptAt,omitempty"`
	MainReply    string                `json:"mainReply,omitempty"`
	MainReplyAt  int64                 `json:"mainReplyAt,omitempty"`
	MainLayers   []string              `json:"mainLayers,omitempty"`
	MainSources  []string              `json:"mainSources,omitempty"`
	MainLoops    []ShrimpBrainLoopNode `json:"mainLoops,omitempty"`
	Members      []ShrimpBrainMember   `json:"members"`
	Events       []ShrimpBrainEvent    `json:"events"`
}

// ShrimpBrainLoopNode 表示 agent runloop 中的一次循环节点。
type ShrimpBrainLoopNode struct {
	ID         string                `json:"id"`
	Iteration  int                   `json:"iteration"`
	AgentID    string                `json:"agentId"`
	SessionKey string                `json:"sessionKey"`
	Status     string                `json:"status"`
	StopReason string                `json:"stopReason,omitempty"`
	Summary    string                `json:"summary,omitempty"`
	Reply      string                `json:"reply,omitempty"`
	UpdatedAt  int64                 `json:"updatedAt"`
	ToolCalls  []ShrimpBrainToolCall `json:"toolCalls,omitempty"`
}

// ShrimpBrainToolCall 表示一次工具调用或派发链路。
type ShrimpBrainToolCall struct {
	ID                 string                `json:"id"`
	ToolName           string                `json:"toolName"`
	Status             string                `json:"status"`
	UpdatedAt          int64                 `json:"updatedAt"`
	Summary            string                `json:"summary,omitempty"`
	Arguments          string                `json:"arguments,omitempty"`
	Result             string                `json:"result,omitempty"`
	Error              string                `json:"error,omitempty"`
	Label              string                `json:"label,omitempty"`
	Task               string                `json:"task,omitempty"`
	ChildAgentID       string                `json:"childAgentId,omitempty"`
	ChildSessionKey    string                `json:"childSessionKey,omitempty"`
	ChildPrompt        string                `json:"childPrompt,omitempty"`
	ChildPromptLayers  []string              `json:"childPromptLayers,omitempty"`
	ChildPromptSources []string              `json:"childPromptSources,omitempty"`
	ChildReply         string                `json:"childReply,omitempty"`
	ChildStatus        string                `json:"childStatus,omitempty"`
	ChildLoops         []ShrimpBrainLoopNode `json:"childLoops,omitempty"`
}

// ShrimpBrainMember 是团队视角下的一个 agent 成员。
type ShrimpBrainMember struct {
	AgentID    string `json:"agentId"`
	Role       string `json:"role"`
	SessionKey string `json:"sessionKey"`
	Status     string `json:"status"`
	UpdatedAt  int64  `json:"updatedAt"`
}

// ShrimpBrainEvent 表示任务处理过程中的一个关键事件。
type ShrimpBrainEvent struct {
	ID         string   `json:"id"`
	Timestamp  int64    `json:"timestamp"`
	Kind       string   `json:"kind"`
	AgentID    string   `json:"agentId,omitempty"`
	SessionKey string   `json:"sessionKey,omitempty"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary,omitempty"`
	Label      string   `json:"label,omitempty"`
	Status     string   `json:"status,omitempty"`
	Task       string   `json:"task,omitempty"`
	Prompt     string   `json:"prompt,omitempty"`
	Reply      string   `json:"reply,omitempty"`
	Error      string   `json:"error,omitempty"`
	Layers     []string `json:"layers,omitempty"`
	Sources    []string `json:"sources,omitempty"`
}

// ShrimpBrainTracker 维护主/子 agent 团队协作的结构化轨迹。
type ShrimpBrainTracker struct {
	mu         sync.RWMutex
	runs       []*ShrimpBrainRun
	runIndex   map[string]*ShrimpBrainRun
	sessionRun map[string]string
	listeners  []chan ShrimpBrainSnapshot
	maxRuns    int
	maxEvents  int
	teamName   string
	storePath  string
}

// NewShrimpBrainTracker 创建追踪器。
func NewShrimpBrainTracker(dataDir string) *ShrimpBrainTracker {
	tracker := &ShrimpBrainTracker{
		runs:       make([]*ShrimpBrainRun, 0, defaultShrimpBrainMaxRuns),
		runIndex:   make(map[string]*ShrimpBrainRun),
		sessionRun: make(map[string]string),
		listeners:  make([]chan ShrimpBrainSnapshot, 0),
		maxRuns:    defaultShrimpBrainMaxRuns,
		maxEvents:  defaultShrimpBrainMaxEvents,
		teamName:   "虾脑",
	}
	if strings.TrimSpace(dataDir) != "" {
		tracker.storePath = filepath.Join(dataDir, "shrimp_brain.json")
		if err := tracker.loadFromDisk(); err != nil {
			logger.Warn("Failed to load shrimp brain from disk",
				zap.String("path", tracker.storePath),
				zap.Error(err))
		}
	}
	return tracker
}

// Snapshot 返回当前快照。
func (t *ShrimpBrainTracker) Snapshot() ShrimpBrainSnapshot {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.snapshotLocked()
}

// DeleteRun 删除一条历史 run，并持久化更新。
func (t *ShrimpBrainTracker) DeleteRun(runID string) bool {
	if t == nil || strings.TrimSpace(runID) == "" {
		return false
	}

	t.mu.Lock()
	runID = strings.TrimSpace(runID)
	target, ok := t.runIndex[runID]
	if !ok || target == nil {
		t.mu.Unlock()
		return false
	}

	delete(t.runIndex, runID)
	filtered := make([]*ShrimpBrainRun, 0, len(t.runs))
	for _, run := range t.runs {
		if run == nil || run.ID == runID {
			continue
		}
		filtered = append(filtered, run)
	}
	t.runs = filtered
	for sessionKey, mappedRunID := range t.sessionRun {
		if mappedRunID == runID {
			delete(t.sessionRun, sessionKey)
		}
	}

	snapshot, listeners := t.finishMutationLocked()
	t.mu.Unlock()
	t.broadcastSnapshot(snapshot, listeners)
	return true
}

// EmptyShrimpBrainSnapshot 返回无可用追踪器时的空快照。
func EmptyShrimpBrainSnapshot(note string) ShrimpBrainSnapshot {
	return ShrimpBrainSnapshot{
		Available:   false,
		GeneratedAt: time.Now().Format(time.RFC3339),
		TeamName:    "虾脑",
		Note:        note,
		Runs:        []ShrimpBrainRun{},
	}
}

// Subscribe 订阅快照变化。
func (t *ShrimpBrainTracker) Subscribe() chan ShrimpBrainSnapshot {
	t.mu.Lock()
	defer t.mu.Unlock()

	ch := make(chan ShrimpBrainSnapshot, 8)
	t.listeners = append(t.listeners, ch)
	current := t.snapshotLocked()
	select {
	case ch <- current:
	default:
	}
	return ch
}

// Unsubscribe 取消订阅。
func (t *ShrimpBrainTracker) Unsubscribe(ch chan ShrimpBrainSnapshot) {
	t.mu.Lock()
	defer t.mu.Unlock()

	for i, listener := range t.listeners {
		if listener == ch {
			t.listeners = append(t.listeners[:i], t.listeners[i+1:]...)
			close(ch)
			break
		}
	}
}

// StartMainTask 为一次用户请求创建主任务 run。
func (t *ShrimpBrainTracker) StartMainTask(messageID, blockKey, userKey, sessionKey, mainAgentID, channel, chatID, userRequest string) string {
	if t == nil {
		return ""
	}

	runID := strings.TrimSpace(messageID)
	if runID == "" {
		runID = uuid.NewString()
	}

	now := time.Now().UnixMilli()
	run := &ShrimpBrainRun{
		ID:          runID,
		BlockKey:    strings.TrimSpace(blockKey),
		UserKey:     strings.TrimSpace(userKey),
		SessionKey:  strings.TrimSpace(sessionKey),
		Channel:     strings.TrimSpace(channel),
		ChatID:      strings.TrimSpace(chatID),
		MainAgentID: strings.TrimSpace(mainAgentID),
		UserRequest: strings.TrimSpace(userRequest),
		Status:      "planning",
		StartedAt:   now,
		UpdatedAt:   now,
		Members: []ShrimpBrainMember{
			{
				AgentID:    strings.TrimSpace(mainAgentID),
				Role:       "main",
				SessionKey: strings.TrimSpace(sessionKey),
				Status:     "planning",
				UpdatedAt:  now,
			},
		},
		Events: []ShrimpBrainEvent{
			{
				ID:         uuid.NewString(),
				Timestamp:  now,
				Kind:       "user_task_received",
				AgentID:    strings.TrimSpace(mainAgentID),
				SessionKey: strings.TrimSpace(sessionKey),
				Title:      "用户任务进入主编排",
				Summary:    truncateShrimpBrain(strings.TrimSpace(userRequest), 180),
				Task:       strings.TrimSpace(userRequest),
			},
		},
	}

	t.mu.Lock()
	t.runIndex[runID] = run
	if run.SessionKey != "" {
		t.sessionRun[run.SessionKey] = runID
	}
	t.runs = append([]*ShrimpBrainRun{run}, t.runs...)
	t.pruneRunsLocked()
	snapshot, listeners := t.finishMutationLocked()
	t.mu.Unlock()

	t.broadcastSnapshot(snapshot, listeners)
	return runID
}

// RecordPrompt 记录主/子 agent 的完整提示词。
func (t *ShrimpBrainTracker) RecordPrompt(sessionKey, agentID string, isSubagent bool, prompt string, layers []PromptLayerSnapshot) {
	if t == nil || strings.TrimSpace(sessionKey) == "" || strings.TrimSpace(prompt) == "" {
		return
	}

	now := time.Now().UnixMilli()
	layerNames := make([]string, 0, len(layers))
	layerSources := make([]string, 0, len(layers))
	for _, layer := range layers {
		if !layer.Enabled {
			continue
		}
		layerNames = append(layerNames, layer.Name)
		layerSources = append(layerSources, fmt.Sprintf("%s=%s", layer.Name, layer.Source))
	}

	t.mu.Lock()
	run := t.lookupRunBySessionLocked(sessionKey)
	if run == nil || t.hasDuplicatePromptLocked(run, sessionKey, agentID, prompt, isSubagent) {
		t.mu.Unlock()
		return
	}

	event := ShrimpBrainEvent{
		ID:         uuid.NewString(),
		Timestamp:  now,
		Kind:       "main_prompt",
		AgentID:    strings.TrimSpace(agentID),
		SessionKey: strings.TrimSpace(sessionKey),
		Title:      "主 Agent 完整提示词",
		Summary:    strings.Join(layerNames, " > "),
		Prompt:     strings.TrimSpace(prompt),
		Layers:     layerNames,
		Sources:    layerSources,
	}
	if isSubagent {
		event.Kind = "subagent_prompt"
		event.Title = "子 Agent 完整提示词"
		if dispatch := t.lookupDispatchByChildSessionLocked(run, sessionKey); dispatch != nil {
			dispatch.ChildPrompt = strings.TrimSpace(prompt)
			dispatch.ChildPromptLayers = append([]string{}, layerNames...)
			dispatch.ChildPromptSources = append([]string{}, layerSources...)
			dispatch.UpdatedAt = now
		}
	} else {
		run.MainPrompt = strings.TrimSpace(prompt)
		run.MainPromptAt = now
		run.MainLayers = append([]string{}, layerNames...)
		run.MainSources = append([]string{}, layerSources...)
	}
	t.appendEventLocked(run, event)
	t.updateMemberLocked(run, agentID, memberRoleForPrompt(isSubagent), sessionKey, "ready", now)
	snapshot, listeners := t.finishMutationLocked()
	t.mu.Unlock()

	t.broadcastSnapshot(snapshot, listeners)
}

// RecordLoopNode 记录某个 agent runloop 的一次循环节点。
func (t *ShrimpBrainTracker) RecordLoopNode(sessionKey, agentID string, isSubagent bool, iteration int, stopReason, reply string, toolCalls int) {
	if t == nil || strings.TrimSpace(sessionKey) == "" || iteration <= 0 {
		return
	}

	now := time.Now().UnixMilli()
	t.mu.Lock()
	run := t.lookupRunBySessionLocked(sessionKey)
	if run == nil {
		t.mu.Unlock()
		return
	}

	loop := t.upsertLoopNodeLocked(run, sessionKey, agentID, iteration, isSubagent)
	loop.StopReason = strings.TrimSpace(stopReason)
	loop.Reply = strings.TrimSpace(reply)
	loop.Status = "completed"
	loop.UpdatedAt = now
	loop.Summary = fmt.Sprintf("Loop %d · %s · %d tools", iteration, firstNonEmpty(loop.StopReason, "stop"), toolCalls)

	if !isSubagent {
		run.Status = "running"
	}

	snapshot, listeners := t.finishMutationLocked()
	t.mu.Unlock()
	t.broadcastSnapshot(snapshot, listeners)
}

// RecordToolCall 记录某个循环节点内的工具调用。
func (t *ShrimpBrainTracker) RecordToolCall(sessionKey, agentID string, isSubagent bool, iteration int, toolID, toolName string, arguments map[string]any, result, errText string) {
	if t == nil || strings.TrimSpace(sessionKey) == "" || iteration <= 0 || strings.TrimSpace(toolName) == "" {
		return
	}

	now := time.Now().UnixMilli()
	t.mu.Lock()
	run := t.lookupRunBySessionLocked(sessionKey)
	if run == nil {
		t.mu.Unlock()
		return
	}

	loop := t.upsertLoopNodeLocked(run, sessionKey, agentID, iteration, isSubagent)
	call := t.upsertToolCallLocked(loop, toolID, toolName)
	call.Arguments = stringifyShrimpBrain(arguments)
	call.Result = strings.TrimSpace(result)
	call.Error = strings.TrimSpace(errText)
	call.Status = "ok"
	call.UpdatedAt = now
	if call.Error != "" {
		call.Status = "error"
	}
	call.Summary = truncateShrimpBrain(firstNonEmpty(call.Error, call.Result), 140)
	loop.UpdatedAt = now
	snapshot, listeners := t.finishMutationLocked()
	t.mu.Unlock()
	t.broadcastSnapshot(snapshot, listeners)
}

// RecordSubagentDispatch 记录主 agent 向子 agent 的派发任务。
func (t *ShrimpBrainTracker) RecordSubagentDispatch(requesterSessionKey, childSessionKey, targetAgentID, label, task string) {
	t.RecordSubagentDispatchAt(requesterSessionKey, childSessionKey, targetAgentID, label, task, 0)
}

// RecordSubagentDispatchAt 记录派发，并可绑定到指定 loop iteration。
func (t *ShrimpBrainTracker) RecordSubagentDispatchAt(requesterSessionKey, childSessionKey, targetAgentID, label, task string, iteration int) {
	if t == nil || strings.TrimSpace(requesterSessionKey) == "" {
		return
	}

	now := time.Now().UnixMilli()
	t.mu.Lock()
	run := t.lookupRunBySessionLocked(requesterSessionKey)
	if run == nil {
		t.mu.Unlock()
		return
	}

	if childSessionKey != "" {
		t.sessionRun[strings.TrimSpace(childSessionKey)] = run.ID
	}
	run.Status = "running"
	loop := t.upsertLoopNodeLocked(run, requesterSessionKey, run.MainAgentID, maxShrimpInt(iteration, 1), false)
	call := t.upsertToolCallLocked(loop, childSessionKey, "sessions_spawn")
	call.Status = "ok"
	call.Label = strings.TrimSpace(label)
	call.Task = strings.TrimSpace(task)
	call.ChildAgentID = strings.TrimSpace(targetAgentID)
	call.ChildSessionKey = strings.TrimSpace(childSessionKey)
	call.Summary = truncateShrimpBrain(firstNonEmpty(call.Task, call.Label), 140)
	call.UpdatedAt = now
	t.appendEventLocked(run, ShrimpBrainEvent{
		ID:         uuid.NewString(),
		Timestamp:  now,
		Kind:       "subagent_dispatch",
		AgentID:    strings.TrimSpace(targetAgentID),
		SessionKey: strings.TrimSpace(childSessionKey),
		Title:      fmt.Sprintf("派发给 %s", fallbackShrimpAgent(targetAgentID)),
		Summary:    truncateShrimpBrain(strings.TrimSpace(task), 180),
		Label:      strings.TrimSpace(label),
		Task:       strings.TrimSpace(task),
	})
	t.updateMemberLocked(run, targetAgentID, "subagent", childSessionKey, "assigned", now)
	snapshot, listeners := t.finishMutationLocked()
	t.mu.Unlock()

	t.broadcastSnapshot(snapshot, listeners)
}

// RecordSubagentResult 记录子 agent 的最终回复。
func (t *ShrimpBrainTracker) RecordSubagentResult(childSessionKey, agentID, status, reply, errText string) {
	if t == nil || strings.TrimSpace(childSessionKey) == "" {
		return
	}

	now := time.Now().UnixMilli()
	t.mu.Lock()
	run := t.lookupRunBySessionLocked(childSessionKey)
	if run == nil {
		t.mu.Unlock()
		return
	}

	memberStatus := strings.TrimSpace(status)
	if memberStatus == "" {
		memberStatus = "completed"
	}
	if dispatch := t.lookupDispatchByChildSessionLocked(run, childSessionKey); dispatch != nil {
		dispatch.ChildStatus = memberStatus
		dispatch.ChildReply = strings.TrimSpace(reply)
		dispatch.UpdatedAt = now
		if strings.TrimSpace(errText) != "" {
			dispatch.Error = strings.TrimSpace(errText)
			dispatch.Status = "error"
		}
	}

	t.appendEventLocked(run, ShrimpBrainEvent{
		ID:         uuid.NewString(),
		Timestamp:  now,
		Kind:       "subagent_result",
		AgentID:    strings.TrimSpace(agentID),
		SessionKey: strings.TrimSpace(childSessionKey),
		Title:      fmt.Sprintf("%s 返回结果", fallbackShrimpAgent(agentID)),
		Status:     memberStatus,
		Summary:    truncateShrimpBrain(firstNonEmpty(strings.TrimSpace(reply), strings.TrimSpace(errText)), 180),
		Reply:      strings.TrimSpace(reply),
		Error:      strings.TrimSpace(errText),
	})
	t.updateMemberLocked(run, agentID, "subagent", childSessionKey, memberStatus, now)
	snapshot, listeners := t.finishMutationLocked()
	t.mu.Unlock()

	t.broadcastSnapshot(snapshot, listeners)
}

// RecordMainReply 记录主 agent 的最终收束回复。
func (t *ShrimpBrainTracker) RecordMainReply(sessionKey, agentID, reply string) {
	if t == nil || strings.TrimSpace(sessionKey) == "" || strings.TrimSpace(reply) == "" {
		return
	}

	now := time.Now().UnixMilli()
	t.mu.Lock()
	run := t.lookupRunBySessionLocked(sessionKey)
	if run == nil {
		t.mu.Unlock()
		return
	}

	run.Status = "completed"
	run.CompletedAt = &now
	run.MainReply = strings.TrimSpace(reply)
	run.MainReplyAt = now
	t.appendEventLocked(run, ShrimpBrainEvent{
		ID:         uuid.NewString(),
		Timestamp:  now,
		Kind:       "main_reply",
		AgentID:    strings.TrimSpace(agentID),
		SessionKey: strings.TrimSpace(sessionKey),
		Title:      "主 Agent 最终回复",
		Summary:    truncateShrimpBrain(strings.TrimSpace(reply), 180),
		Reply:      strings.TrimSpace(reply),
	})
	t.updateMemberLocked(run, agentID, "main", sessionKey, "completed", now)
	snapshot, listeners := t.finishMutationLocked()
	t.mu.Unlock()

	t.broadcastSnapshot(snapshot, listeners)
}

// RecordRunError 记录主/子 agent 的错误事件。
func (t *ShrimpBrainTracker) RecordRunError(sessionKey, agentID string, isSubagent bool, errText string) {
	if t == nil || strings.TrimSpace(sessionKey) == "" || strings.TrimSpace(errText) == "" {
		return
	}

	now := time.Now().UnixMilli()
	t.mu.Lock()
	run := t.lookupRunBySessionLocked(sessionKey)
	if run == nil {
		t.mu.Unlock()
		return
	}

	run.Status = "error"
	title := "主 Agent 执行报错"
	role := "main"
	kind := "main_error"
	if isSubagent {
		title = "子 Agent 执行报错"
		role = "subagent"
		kind = "subagent_error"
	}
	t.appendEventLocked(run, ShrimpBrainEvent{
		ID:         uuid.NewString(),
		Timestamp:  now,
		Kind:       kind,
		AgentID:    strings.TrimSpace(agentID),
		SessionKey: strings.TrimSpace(sessionKey),
		Title:      title,
		Summary:    truncateShrimpBrain(strings.TrimSpace(errText), 180),
		Error:      strings.TrimSpace(errText),
		Status:     "error",
	})
	t.updateMemberLocked(run, agentID, role, sessionKey, "error", now)
	snapshot, listeners := t.finishMutationLocked()
	t.mu.Unlock()

	t.broadcastSnapshot(snapshot, listeners)
}

func (t *ShrimpBrainTracker) hasDuplicatePromptLocked(run *ShrimpBrainRun, sessionKey, agentID, prompt string, isSubagent bool) bool {
	if run == nil || len(run.Events) == 0 {
		return false
	}
	last := run.Events[len(run.Events)-1]
	expectedKind := "main_prompt"
	if isSubagent {
		expectedKind = "subagent_prompt"
	}
	return last.Kind == expectedKind &&
		last.SessionKey == strings.TrimSpace(sessionKey) &&
		last.AgentID == strings.TrimSpace(agentID) &&
		last.Prompt == strings.TrimSpace(prompt)
}

func memberRoleForPrompt(isSubagent bool) string {
	if isSubagent {
		return "subagent"
	}
	return "main"
}

func (t *ShrimpBrainTracker) lookupRunBySessionLocked(sessionKey string) *ShrimpBrainRun {
	runID := t.sessionRun[strings.TrimSpace(sessionKey)]
	if runID == "" {
		return nil
	}
	return t.runIndex[runID]
}

func (t *ShrimpBrainTracker) appendEventLocked(run *ShrimpBrainRun, event ShrimpBrainEvent) {
	run.Events = append(run.Events, event)
	if len(run.Events) > t.maxEvents {
		run.Events = append([]ShrimpBrainEvent{}, run.Events[len(run.Events)-t.maxEvents:]...)
	}
	run.UpdatedAt = event.Timestamp
}

func (t *ShrimpBrainTracker) upsertLoopNodeLocked(run *ShrimpBrainRun, sessionKey, agentID string, iteration int, isSubagent bool) *ShrimpBrainLoopNode {
	loops := t.loopSliceForSessionLocked(run, sessionKey)
	if loops == nil {
		return nil
	}

	for i := range *loops {
		if (*loops)[i].Iteration == iteration {
			return &(*loops)[i]
		}
	}

	*loops = append(*loops, ShrimpBrainLoopNode{
		ID:         uuid.NewString(),
		Iteration:  iteration,
		AgentID:    strings.TrimSpace(agentID),
		SessionKey: strings.TrimSpace(sessionKey),
		Status:     "running",
		UpdatedAt:  time.Now().UnixMilli(),
		ToolCalls:  []ShrimpBrainToolCall{},
	})
	return &(*loops)[len(*loops)-1]
}

func (t *ShrimpBrainTracker) upsertToolCallLocked(loop *ShrimpBrainLoopNode, toolID, toolName string) *ShrimpBrainToolCall {
	if loop == nil {
		return nil
	}
	normalizedID := strings.TrimSpace(toolID)
	normalizedName := strings.TrimSpace(toolName)
	for i := range loop.ToolCalls {
		call := &loop.ToolCalls[i]
		if normalizedID != "" && strings.TrimSpace(call.ID) == normalizedID {
			return call
		}
		if normalizedID == "" && strings.TrimSpace(call.ToolName) == normalizedName && strings.TrimSpace(call.ChildSessionKey) == "" {
			return call
		}
	}
	loop.ToolCalls = append(loop.ToolCalls, ShrimpBrainToolCall{
		ID:       normalizedID,
		ToolName: normalizedName,
		Status:   "running",
	})
	return &loop.ToolCalls[len(loop.ToolCalls)-1]
}

func (t *ShrimpBrainTracker) loopSliceForSessionLocked(run *ShrimpBrainRun, sessionKey string) *[]ShrimpBrainLoopNode {
	sessionKey = strings.TrimSpace(sessionKey)
	if run == nil || sessionKey == "" {
		return nil
	}
	if run.SessionKey == sessionKey {
		return &run.MainLoops
	}
	for i := range run.MainLoops {
		for j := range run.MainLoops[i].ToolCalls {
			if strings.TrimSpace(run.MainLoops[i].ToolCalls[j].ChildSessionKey) == sessionKey {
				return &run.MainLoops[i].ToolCalls[j].ChildLoops
			}
		}
	}
	return nil
}

func (t *ShrimpBrainTracker) lookupDispatchByChildSessionLocked(run *ShrimpBrainRun, childSessionKey string) *ShrimpBrainToolCall {
	childSessionKey = strings.TrimSpace(childSessionKey)
	if run == nil || childSessionKey == "" {
		return nil
	}
	for i := range run.MainLoops {
		for j := range run.MainLoops[i].ToolCalls {
			if strings.TrimSpace(run.MainLoops[i].ToolCalls[j].ChildSessionKey) == childSessionKey {
				return &run.MainLoops[i].ToolCalls[j]
			}
		}
	}
	return nil
}

func (t *ShrimpBrainTracker) updateMemberLocked(run *ShrimpBrainRun, agentID, role, sessionKey, status string, ts int64) {
	agentID = strings.TrimSpace(agentID)
	sessionKey = strings.TrimSpace(sessionKey)
	role = strings.TrimSpace(role)
	status = strings.TrimSpace(status)

	for i := range run.Members {
		member := &run.Members[i]
		if member.AgentID == agentID && member.SessionKey == sessionKey {
			member.Role = fallbackShrimpRole(role, member.Role)
			member.Status = status
			member.UpdatedAt = ts
			return
		}
	}

	run.Members = append(run.Members, ShrimpBrainMember{
		AgentID:    agentID,
		Role:       role,
		SessionKey: sessionKey,
		Status:     status,
		UpdatedAt:  ts,
	})
}

func fallbackShrimpRole(next, fallback string) string {
	if strings.TrimSpace(next) != "" {
		return strings.TrimSpace(next)
	}
	return strings.TrimSpace(fallback)
}

func fallbackShrimpAgent(agentID string) string {
	if strings.TrimSpace(agentID) == "" {
		return "未命名 Agent"
	}
	return strings.TrimSpace(agentID)
}

func stringifyShrimpBrain(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", value)
	}
	return string(data)
}

func (t *ShrimpBrainTracker) pruneRunsLocked() {
	if len(t.runs) <= t.maxRuns {
		return
	}

	pruned := t.runs[t.maxRuns:]
	t.runs = t.runs[:t.maxRuns]
	for _, run := range pruned {
		delete(t.runIndex, run.ID)
		for sessionKey, mappedRunID := range t.sessionRun {
			if mappedRunID == run.ID {
				delete(t.sessionRun, sessionKey)
			}
		}
	}
}

func (t *ShrimpBrainTracker) finishMutationLocked() (ShrimpBrainSnapshot, []chan ShrimpBrainSnapshot) {
	sort.SliceStable(t.runs, func(i, j int) bool {
		return t.runs[i].UpdatedAt > t.runs[j].UpdatedAt
	})
	if err := t.saveLocked(); err != nil {
		logger.Warn("Failed to persist shrimp brain snapshot",
			zap.String("path", t.storePath),
			zap.Error(err))
	}
	snapshot := t.snapshotLocked()
	listeners := append([]chan ShrimpBrainSnapshot{}, t.listeners...)
	return snapshot, listeners
}

func (t *ShrimpBrainTracker) snapshotLocked() ShrimpBrainSnapshot {
	runs := make([]ShrimpBrainRun, 0, len(t.runs))
	activeRuns := 0
	for _, run := range t.runs {
		if run == nil {
			continue
		}
		if run.Status != "completed" {
			activeRuns++
		}
		runs = append(runs, cloneShrimpBrainRun(run))
	}
	return ShrimpBrainSnapshot{
		Available:   true,
		GeneratedAt: time.Now().Format(time.RFC3339),
		TeamName:    t.teamName,
		ActiveRuns:  activeRuns,
		Runs:        runs,
	}
}

func (t *ShrimpBrainTracker) broadcastSnapshot(snapshot ShrimpBrainSnapshot, listeners []chan ShrimpBrainSnapshot) {
	for _, ch := range listeners {
		select {
		case ch <- snapshot:
		default:
		}
	}
}

func cloneShrimpBrainRun(run *ShrimpBrainRun) ShrimpBrainRun {
	copyRun := *run
	copyRun.Members = append([]ShrimpBrainMember{}, run.Members...)
	copyRun.Events = append([]ShrimpBrainEvent{}, run.Events...)
	copyRun.MainLayers = append([]string{}, run.MainLayers...)
	copyRun.MainSources = append([]string{}, run.MainSources...)
	copyRun.MainLoops = cloneShrimpBrainLoops(run.MainLoops)
	return copyRun
}

func cloneShrimpBrainLoops(loops []ShrimpBrainLoopNode) []ShrimpBrainLoopNode {
	if len(loops) == 0 {
		return nil
	}
	out := make([]ShrimpBrainLoopNode, len(loops))
	for i := range loops {
		out[i] = loops[i]
		out[i].ToolCalls = cloneShrimpBrainToolCalls(loops[i].ToolCalls)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Iteration == out[j].Iteration {
			return out[i].UpdatedAt < out[j].UpdatedAt
		}
		return out[i].Iteration < out[j].Iteration
	})
	return out
}

func cloneShrimpBrainToolCalls(calls []ShrimpBrainToolCall) []ShrimpBrainToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ShrimpBrainToolCall, len(calls))
	for i := range calls {
		out[i] = calls[i]
		out[i].ChildPromptLayers = append([]string{}, calls[i].ChildPromptLayers...)
		out[i].ChildPromptSources = append([]string{}, calls[i].ChildPromptSources...)
		out[i].ChildLoops = cloneShrimpBrainLoops(calls[i].ChildLoops)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].UpdatedAt == out[j].UpdatedAt {
			return out[i].ToolName < out[j].ToolName
		}
		return out[i].UpdatedAt < out[j].UpdatedAt
	})
	return out
}

func truncateShrimpBrain(value string, maxLen int) string {
	value = strings.TrimSpace(value)
	if len(value) <= maxLen {
		return value
	}
	return value[:maxLen] + "..."
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func maxShrimpInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func (t *ShrimpBrainTracker) saveLocked() error {
	if strings.TrimSpace(t.storePath) == "" {
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(t.storePath), 0755); err != nil {
		return err
	}

	payload := struct {
		TeamName string           `json:"teamName"`
		Runs     []ShrimpBrainRun `json:"runs"`
	}{
		TeamName: t.teamName,
		Runs:     make([]ShrimpBrainRun, 0, len(t.runs)),
	}
	for _, run := range t.runs {
		if run == nil {
			continue
		}
		payload.Runs = append(payload.Runs, cloneShrimpBrainRun(run))
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := t.storePath + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmpPath, t.storePath)
}

func (t *ShrimpBrainTracker) loadFromDisk() error {
	if strings.TrimSpace(t.storePath) == "" {
		return nil
	}

	data, err := os.ReadFile(t.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	payload := struct {
		TeamName string           `json:"teamName"`
		Runs     []ShrimpBrainRun `json:"runs"`
	}{}
	if err := json.Unmarshal(data, &payload); err != nil {
		return err
	}

	if strings.TrimSpace(payload.TeamName) != "" {
		t.teamName = strings.TrimSpace(payload.TeamName)
	}
	t.runs = make([]*ShrimpBrainRun, 0, len(payload.Runs))
	t.runIndex = make(map[string]*ShrimpBrainRun, len(payload.Runs))
	t.sessionRun = make(map[string]string, len(payload.Runs)*2)
	for i := range payload.Runs {
		runCopy := payload.Runs[i]
		run := runCopy
		t.runs = append(t.runs, &run)
		t.runIndex[run.ID] = &run
		if strings.TrimSpace(run.SessionKey) != "" {
			t.sessionRun[strings.TrimSpace(run.SessionKey)] = run.ID
		}
		for _, member := range run.Members {
			if strings.TrimSpace(member.SessionKey) != "" {
				t.sessionRun[strings.TrimSpace(member.SessionKey)] = run.ID
			}
		}
		t.restoreSessionMapFromLoopsLocked(run.ID, run.MainLoops)
	}
	sort.SliceStable(t.runs, func(i, j int) bool {
		return t.runs[i].UpdatedAt > t.runs[j].UpdatedAt
	})
	return nil
}

func (t *ShrimpBrainTracker) restoreSessionMapFromLoopsLocked(runID string, loops []ShrimpBrainLoopNode) {
	for _, loop := range loops {
		if strings.TrimSpace(loop.SessionKey) != "" {
			t.sessionRun[strings.TrimSpace(loop.SessionKey)] = runID
		}
		for _, call := range loop.ToolCalls {
			if strings.TrimSpace(call.ChildSessionKey) != "" {
				t.sessionRun[strings.TrimSpace(call.ChildSessionKey)] = runID
			}
			t.restoreSessionMapFromLoopsLocked(runID, call.ChildLoops)
		}
	}
}
