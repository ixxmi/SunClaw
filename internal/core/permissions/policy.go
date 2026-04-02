package permissions

import (
	"strings"

	"github.com/smallnest/goclaw/internal/core/config"
)

func CompilePolicy(cfg *config.Config) *Policy {
	if cfg == nil {
		return &Policy{Mode: "manual", allowlist: map[string]struct{}{}}
	}

	policy := &Policy{
		Mode:         normalizeMode(cfg.Approvals.Behavior),
		allowlist:    make(map[string]struct{}, len(cfg.Approvals.Allowlist)),
		shellAllowed: normalizeList(cfg.Tools.Shell.AllowedCmds),
		shellDenied:  normalizeList(cfg.Tools.Shell.DeniedCmds),
	}

	for _, tool := range normalizeList(cfg.Approvals.Allowlist) {
		policy.allowlist[tool] = struct{}{}
	}

	return policy
}

func (p *Policy) IsToolAllowlisted(toolName string) bool {
	if p == nil {
		return false
	}
	_, ok := p.allowlist[strings.TrimSpace(toolName)]
	return ok
}

func (p *Policy) AutoApprove(toolName string) bool {
	if p == nil {
		return false
	}
	if normalizeMode(p.Mode) == "auto" {
		return true
	}
	return p.IsToolAllowlisted(toolName)
}

func normalizeMode(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "auto":
		return "auto"
	case "prompt":
		return "prompt"
	default:
		return "manual"
	}
}

func normalizeList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
