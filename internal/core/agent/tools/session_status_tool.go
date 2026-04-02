package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/smallnest/goclaw/internal/core/execution"
	"github.com/smallnest/goclaw/internal/core/namespaces"
	"github.com/smallnest/goclaw/internal/core/session"
)

// SessionStatusTool exposes basic session status to the model.
type SessionStatusTool struct {
	sessionMgr    *session.Manager
	managerPool   *session.ManagerPool
	baseWorkspace string
}

// NewSessionStatusTool creates a session status tool.
func NewSessionStatusTool(sessionMgr *session.Manager) *SessionStatusTool {
	return &SessionStatusTool{sessionMgr: sessionMgr}
}

// NewNamespacedSessionStatusTool creates a session status tool that resolves managers per workspace namespace.
func NewNamespacedSessionStatusTool(baseWorkspace string, managerPool *session.ManagerPool) *SessionStatusTool {
	return &SessionStatusTool{
		managerPool:   managerPool,
		baseWorkspace: strings.TrimSpace(baseWorkspace),
	}
}

func (t *SessionStatusTool) Name() string {
	return "session_status"
}

func (t *SessionStatusTool) Description() string {
	return "Show the current session's message count and timestamps."
}

func (t *SessionStatusTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"session_key": map[string]interface{}{
				"type":        "string",
				"description": "Optional explicit session key. Defaults to current session.",
			},
		},
	}
}

func (t *SessionStatusTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.sessionMgr == nil && t.managerPool == nil {
		return "", fmt.Errorf("session manager is unavailable")
	}

	sessionKey := ""
	if raw, ok := params["session_key"].(string); ok {
		sessionKey = strings.TrimSpace(raw)
	}
	if sessionKey == "" {
		sessionKey = execution.SessionKey(ctx)
	}
	if sessionKey == "" {
		sessionKey = "main"
	}

	sessionMgr := t.sessionMgr
	if sessionMgr == nil && t.managerPool != nil {
		workspaceRoot := execution.WorkspaceRoot(ctx)
		if workspaceRoot == "" {
			if identity, ok := namespaces.FromSessionKey(sessionKey); ok {
				workspaceRoot = identity.WorkspaceDir(t.baseWorkspace)
			}
		}
		if workspaceRoot == "" {
			workspaceRoot = t.baseWorkspace
		}
		var err error
		sessionMgr, err = t.managerPool.Get(filepath.Join(workspaceRoot, "sessions"))
		if err != nil {
			return "", fmt.Errorf("failed to resolve session manager: %w", err)
		}
	}

	sess, err := sessionMgr.GetOrCreate(sessionKey)
	if err != nil {
		return "", fmt.Errorf("failed to get session %q: %w", sessionKey, err)
	}

	lastMessageAt := ""
	if n := len(sess.Messages); n > 0 {
		lastMessageAt = sess.Messages[n-1].Timestamp.Format("2006-01-02 15:04:05 MST")
	}

	payload := map[string]interface{}{
		"session_key":     sess.Key,
		"message_count":   len(sess.Messages),
		"created_at":      sess.CreatedAt.Format("2006-01-02 15:04:05 MST"),
		"updated_at":      sess.UpdatedAt.Format("2006-01-02 15:04:05 MST"),
		"last_message_at": lastMessageAt,
		"has_metadata":    len(sess.Metadata) > 0,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", fmt.Errorf("failed to marshal session status: %w", err)
	}
	return string(data), nil
}
