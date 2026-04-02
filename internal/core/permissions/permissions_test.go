package permissions

import (
	"testing"

	"github.com/smallnest/goclaw/internal/core/agent/tooltypes"
	"github.com/smallnest/goclaw/internal/core/config"
)

func TestEvaluateToolCall_DeniesShellCommandFromDeniedList(t *testing.T) {
	policy := CompilePolicy(&config.Config{
		Tools: config.ToolsConfig{
			Shell: config.ShellToolConfig{
				DeniedCmds: []string{"rm -rf"},
			},
		},
	})

	decision := EvaluateToolCall(policy, "run_shell", map[string]any{
		"command": "rm -rf /tmp/demo",
	}, tooltypes.ToolSpec{
		Name:     "run_shell",
		Mutation: tooltypes.MutationSideEffect,
		Risk:     tooltypes.RiskHigh,
	})

	if decision.Allowed() {
		t.Fatalf("expected denied decision, got %+v", decision)
	}
	if decision.MatchedRule != "rm -rf" {
		t.Fatalf("unexpected matched rule: %q", decision.MatchedRule)
	}
}

func TestEvaluateToolCall_DeniesShellCommandOutsideAllowlist(t *testing.T) {
	policy := CompilePolicy(&config.Config{
		Tools: config.ToolsConfig{
			Shell: config.ShellToolConfig{
				AllowedCmds: []string{"git", "rg"},
			},
		},
	})

	decision := EvaluateToolCall(policy, "run_shell", map[string]any{
		"command": "npm test",
	}, tooltypes.ToolSpec{Name: "run_shell"})

	if decision.Allowed() {
		t.Fatalf("expected denied decision, got %+v", decision)
	}
}

func TestEvaluateToolCall_MarksApprovalRequiredWithoutBlocking(t *testing.T) {
	policy := CompilePolicy(&config.Config{
		Approvals: config.ApprovalsConfig{Behavior: "manual"},
	})

	decision := EvaluateToolCall(policy, "sessions_spawn", map[string]any{
		"task": "analyze",
	}, tooltypes.ToolSpec{
		Name:             "sessions_spawn",
		RequiresApproval: true,
		Risk:             tooltypes.RiskHigh,
	})

	if !decision.Allowed() {
		t.Fatalf("expected non-blocking decision, got %+v", decision)
	}
	if !decision.RequiresApproval {
		t.Fatalf("expected requires approval marker, got %+v", decision)
	}
}
