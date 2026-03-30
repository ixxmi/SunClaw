package agent

import (
	"testing"

	"github.com/smallnest/goclaw/internal/core/config"
)

func TestResolveSubagentTimeoutSeconds(t *testing.T) {
	manager := &AgentManager{
		cfg: &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Subagents: &config.SubagentsConfig{TimeoutSeconds: 300},
				},
				List: []config.AgentConfig{
					{
						ID: "coder",
						Subagents: &config.AgentSubagentConfig{
							TimeoutSeconds: 120,
						},
					},
				},
			},
		},
	}

	if got := manager.resolveSubagentTimeoutSeconds("coder", 42); got != 42 {
		t.Fatalf("expected explicit timeout override 42, got %d", got)
	}
	if got := manager.resolveSubagentTimeoutSeconds("coder", 0); got != 120 {
		t.Fatalf("expected agent-specific timeout 120, got %d", got)
	}
	if got := manager.resolveSubagentTimeoutSeconds("tester", 0); got != 300 {
		t.Fatalf("expected default timeout 300, got %d", got)
	}
}
