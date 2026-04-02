package permissions

import (
	"strings"

	"github.com/smallnest/goclaw/internal/core/agent/tooltypes"
)

// EvaluateToolCall centralizes policy checks before a tool is executed.
//
// Current behavior is intentionally conservative:
//   - explicit deny rules are enforced immediately
//   - approval-required tools are flagged, but not blocked yet, because SunClaw
//     does not have a unified runtime approval prompt flow at this layer
func EvaluateToolCall(policy *Policy, toolName string, params map[string]any, spec tooltypes.ToolSpec) Decision {
	spec = spec.Normalized(toolName)
	if policy == nil {
		return Decision{Disposition: DispositionAllow, Spec: spec}
	}

	if toolName == "run_shell" {
		command, _ := params["command"].(string)
		if decision, matched := evaluateShellRules(command, policy); matched {
			decision.Spec = spec
			return decision
		}
	}

	decision := Decision{
		Disposition: DispositionAllow,
		Spec:        spec,
	}

	if spec.RequiresApproval && !policy.AutoApprove(toolName) {
		decision.RequiresApproval = true
		decision.Reason = approvalReason(policy.Mode, toolName)
	}

	return decision
}

func approvalReason(mode, toolName string) string {
	mode = strings.TrimSpace(mode)
	if mode == "" {
		mode = "manual"
	}
	return "tool requires approval under current policy mode: " + mode + " (" + toolName + ")"
}
