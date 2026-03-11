package ops

import "strings"

// ParseIntent converts free text to Action.
func ParseIntent(command string) Action {
	cmd := strings.TrimSpace(strings.ToLower(command))
	if cmd == "" {
		return ActionUnknown
	}
	if containsAny(cmd, []string{"help", "帮助", "命令"}) {
		return ActionHelp
	}
	if containsAny(cmd, []string{"restart", "重启"}) {
		return ActionRestart
	}
	if containsAny(cmd, []string{"upgrade", "升级", "更新", "deploy latest"}) {
		return ActionUpgrade
	}
	if containsAny(cmd, []string{"rollback", "回滚"}) {
		return ActionRollback
	}
	if containsAny(cmd, []string{"status", "状态", "健康", "version", "版本"}) {
		return ActionStatus
	}
	return ActionUnknown
}

func containsAny(s string, keys []string) bool {
	for _, k := range keys {
		if strings.Contains(s, k) {
			return true
		}
	}
	return false
}
