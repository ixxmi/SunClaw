package namespaces

import (
	"context"
	"path/filepath"
	"strings"

	"github.com/smallnest/goclaw/internal/core/bus"
)

const defaultSegment = "default"

// Identity describes the runtime identity of a channel user.
type Identity struct {
	TenantID  string
	Channel   string
	AccountID string
	SenderID  string
}

// FromInboundMessage resolves identity fields from an inbound message.
func FromInboundMessage(msg *bus.InboundMessage) Identity {
	if msg == nil {
		return Identity{}
	}

	return Identity{
		TenantID:  normalizeSegment(resolveTenantID(msg.Metadata)),
		Channel:   normalizeSegment(msg.Channel),
		AccountID: normalizeSegment(msg.AccountID),
		SenderID:  normalizeSegment(msg.SenderID),
	}
}

// FromContext resolves identity fields from tool/runtime context.
func FromContext(ctx context.Context) Identity {
	if ctx == nil {
		return Identity{}
	}

	return Identity{
		TenantID:  normalizeSegment(stringContextValue(ctx, "tenant_id")),
		Channel:   normalizeSegment(stringContextValue(ctx, "channel")),
		AccountID: normalizeSegment(stringContextValue(ctx, "account_id")),
		SenderID:  normalizeSegment(stringContextValue(ctx, "sender_id")),
	}
}

// FromSessionKey attempts to resolve identity fields from a structured session key.
func FromSessionKey(sessionKey string) (Identity, bool) {
	parts := strings.Split(strings.TrimSpace(sessionKey), ":")
	if len(parts) < 8 {
		return Identity{}, false
	}

	values := make(map[string]string, len(parts)/2)
	for i := 0; i+1 < len(parts); i += 2 {
		key := strings.TrimSpace(parts[i])
		value := strings.TrimSpace(parts[i+1])
		switch key {
		case "tenant", "channel", "account", "sender", "chat", "thread", "agent", "subagent", "session":
			values[key] = value
		default:
			// The structured prefix is contiguous; stop once it ends.
			i = len(parts)
		}
	}

	if values["channel"] == "" || values["account"] == "" || values["sender"] == "" {
		return Identity{}, false
	}

	return Identity{
		TenantID:  normalizeSegment(values["tenant"]),
		Channel:   normalizeSegment(values["channel"]),
		AccountID: normalizeSegment(values["account"]),
		SenderID:  normalizeSegment(values["sender"]),
	}, true
}

// NamespaceKey returns the stable namespace prefix used by sessions and storage.
func (i Identity) NamespaceKey() string {
	if strings.TrimSpace(i.Channel) == "" || strings.TrimSpace(i.SenderID) == "" {
		return ""
	}

	return strings.Join([]string{
		"tenant", normalizeSegment(i.TenantID),
		"channel", normalizeSegment(i.Channel),
		"account", normalizeSegment(i.AccountID),
		"sender", normalizeSegment(i.SenderID),
	}, ":")
}

// WorkspaceDir returns the isolated workspace directory for the identity.
// When sender is empty, it falls back to the base workspace for compatibility.
func (i Identity) WorkspaceDir(baseWorkspace string) string {
	baseWorkspace = strings.TrimSpace(baseWorkspace)
	if baseWorkspace == "" {
		return ""
	}
	if strings.TrimSpace(i.SenderID) == "" {
		return baseWorkspace
	}

	return filepath.Join(
		baseWorkspace,
		"users",
		sanitizePathSegment(normalizeSegment(i.TenantID)),
		sanitizePathSegment(normalizeSegment(i.Channel)),
		sanitizePathSegment(normalizeSegment(i.AccountID)),
		sanitizePathSegment(normalizeSegment(i.SenderID)),
	)
}

// BuildConversationSessionKey builds a structured per-user session key.
func BuildConversationSessionKey(identity Identity, chatID, threadID string) string {
	base := identity.NamespaceKey()
	if base == "" {
		base = strings.Join([]string{
			"tenant", normalizeSegment(identity.TenantID),
			"channel", normalizeSegment(identity.Channel),
			"account", normalizeSegment(identity.AccountID),
		}, ":")
	}

	parts := []string{base}
	if trimmedChat := strings.TrimSpace(chatID); trimmedChat != "" && trimmedChat != defaultSegment {
		parts = append(parts, "chat", trimmedChat)
	}
	if trimmedThread := strings.TrimSpace(threadID); trimmedThread != "" {
		parts = append(parts, "thread", trimmedThread)
	}
	return strings.Join(parts, ":")
}

// BuildSubagentSessionKey builds a structured child session key within the same namespace.
func BuildSubagentSessionKey(identity Identity, agentID, runID string) string {
	parts := []string{}
	if prefix := identity.NamespaceKey(); prefix != "" {
		parts = append(parts, prefix)
	}
	parts = append(parts, "agent", strings.TrimSpace(agentID), "subagent", strings.TrimSpace(runID))
	return strings.Join(parts, ":")
}

func resolveTenantID(metadata map[string]interface{}) string {
	if metadata == nil {
		return ""
	}

	for _, key := range []string{
		"tenant_id",
		"tenantId",
		"org_id",
		"orgId",
		"enterprise_id",
		"enterpriseId",
		"corp_id",
		"corpId",
	} {
		if value, ok := metadata[key]; ok {
			switch typed := value.(type) {
			case string:
				if strings.TrimSpace(typed) != "" {
					return typed
				}
			case interface{ String() string }:
				if strings.TrimSpace(typed.String()) != "" {
					return typed.String()
				}
			}
		}
	}

	return ""
}

func stringContextValue(ctx context.Context, key string) string {
	value, _ := ctx.Value(key).(string)
	return strings.TrimSpace(value)
}

func normalizeSegment(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return defaultSegment
	}
	return value
}

func sanitizePathSegment(value string) string {
	if strings.TrimSpace(value) == "" {
		return defaultSegment
	}

	replacer := strings.NewReplacer(
		"/", "_",
		"\\", "_",
		":", "_",
		"*", "_",
		"?", "_",
		"\"", "_",
		"<", "_",
		">", "_",
		"|", "_",
	)
	return replacer.Replace(value)
}
