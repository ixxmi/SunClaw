package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// SubagentRunOutcome 分身运行结果
type SubagentRunOutcome struct {
	Status string `json:"status"` // ok, error, timeout, unknown
	Error  string `json:"error,omitempty"`
	Result string `json:"result,omitempty"`
}

// DeliveryContext 传递上下文
type DeliveryContext struct {
	Channel   string `json:"channel,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	To        string `json:"to,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
}

// SubagentRunRecord 分身运行记录
type SubagentRunRecord struct {
	RunID               string              `json:"run_id"`
	ChildSessionKey     string              `json:"child_session_key"`
	RequesterSessionKey string              `json:"requester_session_key"`
	RequesterOrigin     *DeliveryContext    `json:"requester_origin,omitempty"`
	RequesterDisplayKey string              `json:"requester_display_key"`
	Task                string              `json:"task"`
	Cleanup             string              `json:"cleanup"` // delete, keep
	Label               string              `json:"label,omitempty"`
	CreatedAt           int64               `json:"created_at"`
	StartedAt           *int64              `json:"started_at,omitempty"`
	EndedAt             *int64              `json:"ended_at,omitempty"`
	Outcome             *SubagentRunOutcome `json:"outcome,omitempty"`
	ArchiveAtMs         *int64              `json:"archive_at_ms,omitempty"`
	CleanupCompletedAt  *int64              `json:"cleanup_completed_at,omitempty"`
	CleanupHandled      bool                `json:"cleanup_handled"`
}

// SubagentRegistry 分身注册表
type SubagentRegistry struct {
	runs        map[string]*SubagentRunRecord
	mu          sync.RWMutex
	dataDir     string
	storeFile   string
	sweeperStop chan struct{}
	// 事件回调
	onRunComplete func(runID string, record *SubagentRunRecord)
	onDeleteChild func(sessionKey string) error
}

// NewSubagentRegistry 创建分身注册表
func NewSubagentRegistry(dataDir string) *SubagentRegistry {
	storeFile := filepath.Join(dataDir, "subagent_registry.json")
	return &SubagentRegistry{
		runs:      make(map[string]*SubagentRunRecord),
		dataDir:   dataDir,
		storeFile: storeFile,
	}
}

// RegisterRun 注册分身运行
func (r *SubagentRegistry) RegisterRun(params *SubagentRunParams) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UnixMilli()
	archiveAfterMs := int64(params.ArchiveAfterMinutes) * 60_000
	var archiveAtMs *int64
	if archiveAfterMs > 0 {
		archiveAtMs = new(int64)
		*archiveAtMs = now + archiveAfterMs
	}

	record := &SubagentRunRecord{
		RunID:               params.RunID,
		ChildSessionKey:     params.ChildSessionKey,
		RequesterSessionKey: params.RequesterSessionKey,
		RequesterOrigin:     params.RequesterOrigin,
		RequesterDisplayKey: params.RequesterDisplayKey,
		Task:                params.Task,
		Cleanup:             params.Cleanup,
		Label:               params.Label,
		CreatedAt:           now,
		StartedAt:           &now,
		ArchiveAtMs:         archiveAtMs,
		CleanupHandled:      false,
	}

	r.runs[params.RunID] = record

	// 启动清理器
	if archiveAtMs != nil {
		r.startSweeper()
	}

	// 保存到磁盘
	if err := r.saveToDisk(); err != nil {
		logger.Error("Failed to save subagent registry", zap.Error(err))
	}

	logger.Info("Subagent run registered",
		zap.String("run_id", params.RunID),
		zap.String("child_session_key", params.ChildSessionKey),
		zap.String("task", params.Task))

	return nil
}

// SubagentRunParams 注册参数
type SubagentRunParams struct {
	RunID               string
	ChildSessionKey     string
	RequesterSessionKey string
	RequesterOrigin     *DeliveryContext
	RequesterDisplayKey string
	RequesterAgentID    string
	TargetAgentID       string
	BootstrapOwnerID    string
	PlanID              string
	StepID              string
	ContinueOf          string
	Task                string
	Cleanup             string
	Label               string
	ArchiveAfterMinutes int
}

// GetRun 获取运行记录
func (r *SubagentRegistry) GetRun(runID string) (*SubagentRunRecord, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	record, ok := r.runs[runID]
	return record, ok
}

// ListRunsForRequester 列出请求者的所有分身运行
func (r *SubagentRegistry) ListRunsForRequester(requesterSessionKey string) []*SubagentRunRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*SubagentRunRecord
	key := requesterSessionKey
	if key == "" {
		return result
	}

	for _, record := range r.runs {
		if record.RequesterSessionKey == key {
			result = append(result, record)
		}
	}
	return result
}

// MarkCompleted 标记分身运行完成
func (r *SubagentRegistry) MarkCompleted(runID string, outcome *SubagentRunOutcome, endedAt *int64) error {
	r.mu.Lock()

	record, ok := r.runs[runID]
	if !ok {
		r.mu.Unlock()
		return fmt.Errorf("run not found: %s", runID)
	}

	record.EndedAt = endedAt
	record.Outcome = outcome
	callback := r.onRunComplete
	recordSnapshot := cloneSubagentRunRecord(record)

	// 保存到磁盘
	if err := r.saveToDisk(); err != nil {
		logger.Error("Failed to save subagent registry", zap.Error(err))
	}

	r.mu.Unlock()

	// 触发回调（在锁外执行，避免把共享 record 指针暴露给异步 goroutine）
	if callback != nil && outcome != nil {
		go callback(runID, recordSnapshot)
	}

	return nil
}

// ReleaseRun 释放运行记录
func (r *SubagentRegistry) ReleaseRun(runID string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.runs, runID)

	// 如果没有运行记录了，停止清理器
	// 先置 nil 再关闭，防止多次关闭导致 panic
	if len(r.runs) == 0 && r.sweeperStop != nil {
		ch := r.sweeperStop
		r.sweeperStop = nil
		close(ch)
	}

	_ = r.saveToDisk()
}

// DeleteChildSession 删除子会话
func (r *SubagentRegistry) DeleteChildSession(sessionKey string) error {
	logger.Info("Deleting child session", zap.String("session_key", sessionKey))
	if r.onDeleteChild != nil {
		return r.onDeleteChild(sessionKey)
	}
	return nil
}

// SetOnRunComplete 设置运行完成回调
func (r *SubagentRegistry) SetOnRunComplete(fn func(runID string, record *SubagentRunRecord)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onRunComplete = fn
}

// SetOnDeleteChildSession sets the deletion callback used by sweeper/cleanup.
func (r *SubagentRegistry) SetOnDeleteChildSession(fn func(sessionKey string) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.onDeleteChild = fn
}

// LoadFromDisk 从磁盘加载
func (r *SubagentRegistry) LoadFromDisk() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	data, err := os.ReadFile(r.storeFile)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	var loaded map[string]*SubagentRunRecord
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}

	r.runs = loaded

	// 恢复有归档时间的运行记录的清理器
	for _, record := range r.runs {
		if record.ArchiveAtMs != nil {
			r.startSweeper()
			break
		}
	}

	logger.Info("Subagent registry loaded from disk",
		zap.Int("runs", len(r.runs)))
	return nil
}

// saveToDisk 保存到磁盘
func (r *SubagentRegistry) saveToDisk() error {
	data, err := json.MarshalIndent(r.runs, "", "  ")
	if err != nil {
		return err
	}

	// 确保目录存在
	if err := os.MkdirAll(r.dataDir, 0755); err != nil {
		return err
	}

	return os.WriteFile(r.storeFile, data, 0644)
}

func cloneSubagentRunRecord(record *SubagentRunRecord) *SubagentRunRecord {
	if record == nil {
		return nil
	}
	cloned := *record
	if record.RequesterOrigin != nil {
		origin := *record.RequesterOrigin
		cloned.RequesterOrigin = &origin
	}
	if record.Outcome != nil {
		outcome := *record.Outcome
		cloned.Outcome = &outcome
	}
	if record.StartedAt != nil {
		startedAt := *record.StartedAt
		cloned.StartedAt = &startedAt
	}
	if record.EndedAt != nil {
		endedAt := *record.EndedAt
		cloned.EndedAt = &endedAt
	}
	if record.ArchiveAtMs != nil {
		archiveAtMs := *record.ArchiveAtMs
		cloned.ArchiveAtMs = &archiveAtMs
	}
	if record.CleanupCompletedAt != nil {
		cleanupCompletedAt := *record.CleanupCompletedAt
		cloned.CleanupCompletedAt = &cleanupCompletedAt
	}
	return &cloned
}

// startSweeper 启动清理器
func (r *SubagentRegistry) startSweeper() {
	if r.sweeperStop != nil {
		return
	}
	r.sweeperStop = make(chan struct{})
	go r.runSweeper()
}

// runSweeper 运行清理器
func (r *SubagentRegistry) runSweeper() {
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.sweep()
		case <-r.sweeperStop:
			logger.Info("Subagent registry sweeper stopped")
			return
		}
	}
}

// sweep 清理过期的运行记录
func (r *SubagentRegistry) sweep() {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().UnixMilli()
	var toDelete []string

	for runID, record := range r.runs {
		if record.ArchiveAtMs != nil && *record.ArchiveAtMs <= now {
			toDelete = append(toDelete, runID)
		}
	}

	if len(toDelete) == 0 {
		return
	}

	for _, runID := range toDelete {
		record := r.runs[runID]
		// 删除子会话
		if err := r.DeleteChildSession(record.ChildSessionKey); err != nil {
			logger.Error("Failed to delete child session",
				zap.String("run_id", runID),
				zap.Error(err))
		}
		delete(r.runs, runID)
		logger.Info("Subagent run archived and deleted",
			zap.String("run_id", runID))
	}

	_ = r.saveToDisk()

	// 如果没有运行记录了，停止清理器
	// 先置 nil 再关闭，防止多次关闭导致 panic
	if len(r.runs) == 0 && r.sweeperStop != nil {
		ch := r.sweeperStop
		r.sweeperStop = nil
		close(ch)
	}
}

// Cleanup 标记清理已完成
func (r *SubagentRegistry) Cleanup(runID string, cleanup string, didAnnounce bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.runs[runID]
	if !ok {
		return
	}

	if !didAnnounce {
		// 允许重试
		record.CleanupHandled = false
		_ = r.saveToDisk()
		return
	}

	if cleanup == "delete" {
		delete(r.runs, runID)
		// 先置 nil 再关闭，防止多次关闭导致 panic
		if len(r.runs) == 0 && r.sweeperStop != nil {
			ch := r.sweeperStop
			r.sweeperStop = nil
			close(ch)
		}
	} else {
		now := time.Now().UnixMilli()
		record.CleanupCompletedAt = &now
	}

	_ = r.saveToDisk()
}

// BeginCleanup 开始清理流程
func (r *SubagentRegistry) BeginCleanup(runID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	record, ok := r.runs[runID]
	if !ok {
		return false
	}

	if record.CleanupCompletedAt != nil {
		return false
	}

	if record.CleanupHandled {
		return false
	}

	record.CleanupHandled = true
	_ = r.saveToDisk()
	return true
}

// Count 获取运行数量
func (r *SubagentRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.runs)
}

// GenerateRunID 生成运行ID
func GenerateRunID() string {
	return uuid.New().String()
}

// IsSubagentSessionKey 判断是否为分身会话密钥
func IsSubagentSessionKey(sessionKey string) bool {
	// 分身会话格式: agent:<agentId>:subagent:<uuid>
	// 或: subagent:<uuid>
	if sessionKey == "" {
		return false
	}
	return containsSubagentMarker(sessionKey)
}

// containsSubagentMarker 检查是否包含分身标记
func containsSubagentMarker(s string) bool {
	marker := ":subagent:"
	for i := 0; i <= len(s)-len(marker); i++ {
		if s[i:i+len(marker)] == marker {
			return true
		}
	}
	return false
}

// GenerateChildSessionKey 生成子会话密钥
func GenerateChildSessionKey(agentID string) string {
	u := uuid.New()
	return fmt.Sprintf("agent:%s:subagent:%s", agentID, u.String())
}

// ParseAgentSessionKey 解析 Agent 会话密钥
func ParseAgentSessionKey(sessionKey string) (agentID string, subagentID string, isSubagent bool) {
	if sessionKey == "" {
		return "", "", false
	}

	parts := splitSessionKey(sessionKey)
	for i := 0; i+1 < len(parts); i++ {
		switch parts[i] {
		case "agent":
			agentID = parts[i+1]
		case "subagent":
			subagentID = parts[i+1]
			return agentID, subagentID, true
		}
	}

	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "agent" {
			return parts[i+1], "", false
		}
	}

	return "", "", containsSubagentMarker(sessionKey)
}

// findSubagentMarkerIndex 查找分身标记位置
func findSubagentMarkerIndex(s string) int {
	marker := ":subagent:"
	for i := 0; i <= len(s)-len(marker); i++ {
		if s[i:i+len(marker)] == marker {
			return i
		}
	}
	return -1
}

// splitSessionKey 分割会话密钥
func splitSessionKey(s string) []string {
	var parts []string
	var current strings.Builder

	for i := 0; i < len(s); i++ {
		if s[i] == ':' {
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		} else {
			current.WriteByte(s[i])
		}
	}
	if current.Len() > 0 {
		parts = append(parts, current.String())
	}
	return parts
}
