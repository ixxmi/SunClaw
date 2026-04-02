package execution

import (
	"context"
	"strings"

	"github.com/smallnest/goclaw/internal/core/bus"
)

type toolUseContextKey struct{}

// ToolUseContext carries stable runtime metadata for tool execution.
//
// It intentionally mirrors the legacy string-based context values already used
// across the codebase so we can migrate callers incrementally without breaking
// existing tools or tests.
type ToolUseContext struct {
	SessionKey       string
	AgentID          string
	BootstrapOwnerID string
	WorkspaceRoot    string
	LoopIteration    int

	Channel   string
	AccountID string
	ChatID    string
	SenderID  string
	TenantID  string
	ChatType  string
	ThreadID  string
}

// WithToolUseContext merges execution metadata into ctx and also mirrors the
// values back onto the legacy string keys for backwards compatibility.
func WithToolUseContext(ctx context.Context, values ToolUseContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}

	current := GetToolUseContext(ctx)
	current.merge(values)

	ctx = context.WithValue(ctx, toolUseContextKey{}, current)

	for _, item := range []struct {
		key   string
		value string
	}{
		{key: "session_key", value: current.SessionKey},
		{key: "agent_id", value: current.AgentID},
		{key: "bootstrap_owner_id", value: current.BootstrapOwnerID},
		{key: "workspace_root", value: current.WorkspaceRoot},
		{key: "channel", value: current.Channel},
		{key: "account_id", value: current.AccountID},
		{key: "chat_id", value: current.ChatID},
		{key: "sender_id", value: current.SenderID},
		{key: "tenant_id", value: current.TenantID},
		{key: "chat_type", value: current.ChatType},
		{key: "thread_id", value: current.ThreadID},
	} {
		if item.value != "" {
			ctx = context.WithValue(ctx, item.key, item.value)
		}
	}

	if current.LoopIteration > 0 {
		ctx = context.WithValue(ctx, "loop_iteration", current.LoopIteration)
	}

	return ctx
}

// WithInboundMessage injects routing-related metadata from an inbound message.
func WithInboundMessage(ctx context.Context, msg *bus.InboundMessage) context.Context {
	if msg == nil {
		if ctx == nil {
			return context.Background()
		}
		return ctx
	}

	values := ToolUseContext{
		Channel:   strings.TrimSpace(msg.Channel),
		AccountID: strings.TrimSpace(msg.AccountID),
		ChatID:    strings.TrimSpace(msg.ChatID),
		SenderID:  strings.TrimSpace(msg.SenderID),
	}

	if msg.Metadata != nil {
		for _, key := range []string{"tenant_id", "tenantId", "org_id", "orgId", "enterprise_id", "enterpriseId", "corp_id", "corpId"} {
			if tenantID, ok := msg.Metadata[key].(string); ok && strings.TrimSpace(tenantID) != "" {
				values.TenantID = strings.TrimSpace(tenantID)
				break
			}
		}
		if chatType, ok := msg.Metadata["chat_type"].(string); ok && strings.TrimSpace(chatType) != "" {
			values.ChatType = strings.TrimSpace(chatType)
		}
		for _, key := range []string{"thread_id", "thread_ts", "message_thread_id"} {
			if threadID, ok := msg.Metadata[key].(string); ok && strings.TrimSpace(threadID) != "" {
				values.ThreadID = strings.TrimSpace(threadID)
				break
			}
		}
	}

	return WithToolUseContext(ctx, values)
}

// GetToolUseContext resolves the typed execution metadata from ctx and falls
// back to legacy string keys when needed.
func GetToolUseContext(ctx context.Context) ToolUseContext {
	if ctx == nil {
		return ToolUseContext{}
	}

	var values ToolUseContext
	if existing, ok := ctx.Value(toolUseContextKey{}).(ToolUseContext); ok {
		values = existing
	}

	if values.SessionKey == "" {
		values.SessionKey = legacyStringValue(ctx, "session_key")
	}
	if values.AgentID == "" {
		values.AgentID = legacyStringValue(ctx, "agent_id")
	}
	if values.BootstrapOwnerID == "" {
		values.BootstrapOwnerID = legacyStringValue(ctx, "bootstrap_owner_id")
	}
	if values.WorkspaceRoot == "" {
		values.WorkspaceRoot = legacyStringValue(ctx, "workspace_root")
	}
	if values.Channel == "" {
		values.Channel = legacyStringValue(ctx, "channel")
	}
	if values.AccountID == "" {
		values.AccountID = legacyStringValue(ctx, "account_id")
	}
	if values.ChatID == "" {
		values.ChatID = legacyStringValue(ctx, "chat_id")
	}
	if values.SenderID == "" {
		values.SenderID = legacyStringValue(ctx, "sender_id")
	}
	if values.TenantID == "" {
		values.TenantID = legacyStringValue(ctx, "tenant_id")
	}
	if values.ChatType == "" {
		values.ChatType = legacyStringValue(ctx, "chat_type")
	}
	if values.ThreadID == "" {
		values.ThreadID = legacyStringValue(ctx, "thread_id")
	}
	if values.LoopIteration == 0 {
		values.LoopIteration = legacyIntValue(ctx, "loop_iteration")
	}

	return values
}

func SessionKey(ctx context.Context) string       { return GetToolUseContext(ctx).SessionKey }
func AgentID(ctx context.Context) string          { return GetToolUseContext(ctx).AgentID }
func BootstrapOwnerID(ctx context.Context) string { return GetToolUseContext(ctx).BootstrapOwnerID }
func WorkspaceRoot(ctx context.Context) string    { return GetToolUseContext(ctx).WorkspaceRoot }
func LoopIteration(ctx context.Context) int       { return GetToolUseContext(ctx).LoopIteration }
func Channel(ctx context.Context) string          { return GetToolUseContext(ctx).Channel }
func AccountID(ctx context.Context) string        { return GetToolUseContext(ctx).AccountID }
func ChatID(ctx context.Context) string           { return GetToolUseContext(ctx).ChatID }
func SenderID(ctx context.Context) string         { return GetToolUseContext(ctx).SenderID }
func TenantID(ctx context.Context) string         { return GetToolUseContext(ctx).TenantID }
func ChatType(ctx context.Context) string         { return GetToolUseContext(ctx).ChatType }
func ThreadID(ctx context.Context) string         { return GetToolUseContext(ctx).ThreadID }

func legacyStringValue(ctx context.Context, key string) string {
	value, ok := ctx.Value(key).(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func legacyIntValue(ctx context.Context, key string) int {
	switch value := ctx.Value(key).(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func (c *ToolUseContext) merge(update ToolUseContext) {
	if update.SessionKey != "" {
		c.SessionKey = strings.TrimSpace(update.SessionKey)
	}
	if update.AgentID != "" {
		c.AgentID = strings.TrimSpace(update.AgentID)
	}
	if update.BootstrapOwnerID != "" {
		c.BootstrapOwnerID = strings.TrimSpace(update.BootstrapOwnerID)
	}
	if update.WorkspaceRoot != "" {
		c.WorkspaceRoot = strings.TrimSpace(update.WorkspaceRoot)
	}
	if update.LoopIteration > 0 {
		c.LoopIteration = update.LoopIteration
	}
	if update.Channel != "" {
		c.Channel = strings.TrimSpace(update.Channel)
	}
	if update.AccountID != "" {
		c.AccountID = strings.TrimSpace(update.AccountID)
	}
	if update.ChatID != "" {
		c.ChatID = strings.TrimSpace(update.ChatID)
	}
	if update.SenderID != "" {
		c.SenderID = strings.TrimSpace(update.SenderID)
	}
	if update.TenantID != "" {
		c.TenantID = strings.TrimSpace(update.TenantID)
	}
	if update.ChatType != "" {
		c.ChatType = strings.TrimSpace(update.ChatType)
	}
	if update.ThreadID != "" {
		c.ThreadID = strings.TrimSpace(update.ThreadID)
	}
}
