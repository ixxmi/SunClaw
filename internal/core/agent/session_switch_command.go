package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/smallnest/goclaw/internal/core/bus"
)

type sessionSwitchResult struct {
	IsSwitch bool
	Action   string
	Alias    string
	NewAlias string
	ShowAll  bool
}

func parseSessionSwitchCommand(content string) *sessionSwitchResult {
	trimmed := sanitizeSlashCommandContent(content)
	if trimmed == "" {
		return &sessionSwitchResult{}
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 || parts[0] != "/session" {
		return &sessionSwitchResult{}
	}
	if len(parts) == 1 {
		return &sessionSwitchResult{IsSwitch: true, Action: "current"}
	}
	sub := strings.ToLower(parts[1])
	switch sub {
	case "list":
		showAll := len(parts) >= 3 && strings.EqualFold(parts[2], "--all")
		return &sessionSwitchResult{IsSwitch: true, Action: "list", ShowAll: showAll}
	case "current":
		return &sessionSwitchResult{IsSwitch: true, Action: "current"}
	case "clear":
		return &sessionSwitchResult{IsSwitch: true, Action: "clear"}
	case "rename":
		if len(parts) >= 4 {
			return &sessionSwitchResult{IsSwitch: true, Action: "rename", Alias: parts[2], NewAlias: parts[3]}
		}
		return &sessionSwitchResult{IsSwitch: true, Action: "help"}
	case "archive":
		if len(parts) >= 3 {
			return &sessionSwitchResult{IsSwitch: true, Action: "archive", Alias: parts[2]}
		}
		return &sessionSwitchResult{IsSwitch: true, Action: "help"}
	case "unarchive":
		if len(parts) >= 3 {
			return &sessionSwitchResult{IsSwitch: true, Action: "unarchive", Alias: parts[2]}
		}
		return &sessionSwitchResult{IsSwitch: true, Action: "help"}
	case "switch":
		if len(parts) >= 3 {
			return &sessionSwitchResult{IsSwitch: true, Action: "switch", Alias: parts[2]}
		}
		return &sessionSwitchResult{IsSwitch: true, Action: "help"}
	default:
		return &sessionSwitchResult{IsSwitch: true, Action: "switch", Alias: parts[1]}
	}
}

func (m *AgentManager) handleSessionSwitchCommand(ctx context.Context, cmd *sessionSwitchResult, msg *bus.InboundMessage) error {
	baseSessionKey := m.buildBaseSessionKey(msg)
	activeSessionKey := m.buildSessionKey(msg)
	activeAlias := "default"
	if m.sessionContextRouter != nil {
		if alias := m.sessionContextRouter.CurrentAlias(baseSessionKey); alias != "" {
			activeAlias = alias
		}
	}

	var replyText string
	switch cmd.Action {
	case "list":
		replyText = m.buildSessionListReply(baseSessionKey, cmd.ShowAll)
	case "current":
		replyText = fmt.Sprintf("当前逻辑会话：`%s`\nSession Key：`%s`", activeAlias, activeSessionKey)
	case "clear":
		if m.sessionContextRouter != nil {
			m.sessionContextRouter.Clear(baseSessionKey)
		}
		replyText = fmt.Sprintf("已切回默认会话。\nSession Key：`%s`", baseSessionKey)
	case "switch":
		alias := normalizeSessionAlias(cmd.Alias)
		if alias == "" {
			replyText = sessionSwitchHelpText()
		} else {
			resolved := baseSessionKey
			if m.sessionContextRouter != nil {
				var err error
				resolved, err = m.sessionContextRouter.Switch(baseSessionKey, alias)
				if err != nil {
					if m.sessionContextRouter.IsArchived(baseSessionKey, alias) {
						replyText = fmt.Sprintf("逻辑会话 `%s` 已归档，无法切换。请先执行 `/session unarchive %s`。", alias, alias)
					} else {
						replyText = fmt.Sprintf("切换逻辑会话失败：%s", err.Error())
					}
					break
				}
			}
			replyText = m.buildSessionSwitchSuccessReply(alias, resolved)
		}
	case "rename":
		oldAlias := normalizeSessionAlias(cmd.Alias)
		newAlias := normalizeSessionAlias(cmd.NewAlias)
		if oldAlias == "" || newAlias == "" {
			replyText = sessionSwitchHelpText()
		} else if m.sessionContextRouter == nil {
			replyText = "逻辑会话路由不可用。"
		} else {
			resolved, err := m.sessionContextRouter.Rename(baseSessionKey, oldAlias, newAlias)
			if err != nil {
				replyText = fmt.Sprintf("重命名逻辑会话失败：%s", err.Error())
			} else {
				replyText = fmt.Sprintf("已重命名逻辑会话：`%s` → `%s`\nSession Key：`%s`", oldAlias, newAlias, resolved)
			}
		}
	case "archive":
		alias := normalizeSessionAlias(cmd.Alias)
		if alias == "" {
			replyText = sessionSwitchHelpText()
		} else if m.sessionContextRouter == nil {
			replyText = "逻辑会话路由不可用。"
		} else {
			wasActive := m.sessionContextRouter.CurrentAlias(baseSessionKey) == alias
			_, err := m.sessionContextRouter.Archive(baseSessionKey, alias)
			if err != nil {
				replyText = fmt.Sprintf("归档逻辑会话失败：%s", err.Error())
			} else if wasActive {
				replyText = fmt.Sprintf("已归档逻辑会话：`%s`，并已切回默认会话。\nSession Key：`%s`", alias, baseSessionKey)
			} else {
				replyText = fmt.Sprintf("已归档逻辑会话：`%s`", alias)
			}
		}
	case "unarchive":
		alias := normalizeSessionAlias(cmd.Alias)
		if alias == "" {
			replyText = sessionSwitchHelpText()
		} else if m.sessionContextRouter == nil {
			replyText = "逻辑会话路由不可用。"
		} else {
			_, err := m.sessionContextRouter.Unarchive(baseSessionKey, alias)
			if err != nil {
				replyText = fmt.Sprintf("恢复逻辑会话失败：%s", err.Error())
			} else {
				replyText = fmt.Sprintf("已恢复逻辑会话：`%s`", alias)
			}
		}
	default:
		replyText = sessionSwitchHelpText()
	}

	outbound := &bus.OutboundMessage{
		Channel:   msg.Channel,
		AccountID: msg.AccountID,
		ChatID:    msg.ChatID,
		Content:   replyText,
		ReplyTo:   outboundReplyTarget(msg),
		Timestamp: msg.Timestamp,
	}
	return m.bus.PublishOutbound(ctx, outbound)
}

func (m *AgentManager) buildSessionListReply(baseSessionKey string, showAll bool) string {
	if m.sessionContextRouter == nil {
		preview := m.GetSessionRecentPreview(baseSessionKey)
		return fmt.Sprintf("当前可用逻辑会话：\n- `default` ← 当前\n  `%s`\n  最近预览：%s", baseSessionKey, preview)
	}
	entries := m.sessionContextRouter.List(baseSessionKey)
	lines := []string{"当前可用逻辑会话："}
	for _, entry := range entries {
		if entry.IsArchived && !showAll {
			continue
		}
		mark := ""
		if entry.IsActive {
			mark = " ← 当前"
		}
		status := ""
		if entry.IsArchived {
			status = " [archived]"
		}
		preview := m.GetSessionRecentPreview(entry.SessionKey)
		lines = append(lines, fmt.Sprintf("- `%s`%s%s\n  `%s`\n  最近预览：%s", entry.Alias, status, mark, entry.SessionKey, preview))
	}
	lines = append(lines, "\n使用 `/session switch <alias>` 切换，`/session rename <old> <new>` 重命名，`/session archive <alias>` 归档，`/session unarchive <alias>` 恢复。")
	return strings.Join(lines, "\n")
}

func (m *AgentManager) buildSessionSwitchSuccessReply(alias, sessionKey string) string {
	preview := m.GetSessionRecentPreview(sessionKey)
	return fmt.Sprintf("已切换到逻辑会话：`%s`\nSession Key：`%s`\n最近预览：%s", alias, sessionKey, preview)
}

func sessionSwitchHelpText() string {
	return "用法：`/session list`、`/session list --all`、`/session current`、`/session switch <alias>`、`/session rename <old> <new>`、`/session archive <alias>`、`/session unarchive <alias>`、`/session clear`"
}
