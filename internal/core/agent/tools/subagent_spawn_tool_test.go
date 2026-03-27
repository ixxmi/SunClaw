package tools

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/smallnest/goclaw/internal/core/config"
)

type fakeSubagentRegistry struct {
	last *SubagentRunParams
}

func (f *fakeSubagentRegistry) RegisterRun(params *SubagentRunParams) error {
	f.last = params
	return nil
}

func TestSubagentSpawnTool_RunTimeoutOverrideAndOriginFromContext(t *testing.T) {
	reg := &fakeSubagentRegistry{}
	tool := NewSubagentSpawnTool(reg)

	tool.SetDefaultConfigGetter(func() *config.AgentDefaults {
		return &config.AgentDefaults{Subagents: &config.SubagentsConfig{TimeoutSeconds: 120}}
	})

	var spawned *SubagentSpawnResult
	tool.SetOnSpawn(func(spawnParams *SubagentSpawnResult) error {
		spawned = spawnParams
		return nil
	})

	ctx := context.Background()
	ctx = context.WithValue(ctx, "session_key", "agent:main:chat1")
	ctx = context.WithValue(ctx, "agent_id", "architect")
	ctx = context.WithValue(ctx, "bootstrap_owner_id", "vibecoding")
	ctx = context.WithValue(ctx, "channel", "feishu")
	ctx = context.WithValue(ctx, "account_id", "acc-1")
	ctx = context.WithValue(ctx, "chat_id", "chat-xyz")

	out, err := tool.Execute(ctx, map[string]interface{}{
		"task":                "collect status",
		"run_timeout_seconds": 42,
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "Subagent spawned successfully") {
		t.Fatalf("unexpected output: %s", out)
	}
	if spawned == nil {
		t.Fatalf("expected onSpawn callback to be called")
	}
	if spawned.RunTimeoutSeconds != 42 {
		t.Fatalf("expected timeout override 42, got %d", spawned.RunTimeoutSeconds)
	}
	if spawned.BootstrapOwnerID != "vibecoding" {
		t.Fatalf("expected bootstrap owner vibecoding, got %q", spawned.BootstrapOwnerID)
	}
	if reg.last == nil || reg.last.RequesterOrigin == nil {
		t.Fatalf("expected requester origin to be registered")
	}
	if reg.last.RequesterOrigin.Channel != "feishu" || reg.last.RequesterOrigin.AccountID != "acc-1" || reg.last.RequesterOrigin.To != "chat-xyz" {
		t.Fatalf("unexpected requester origin: %+v", reg.last.RequesterOrigin)
	}
}

func TestSubagentSpawnTool_OnSpawnFailureReturnsError(t *testing.T) {
	reg := &fakeSubagentRegistry{}
	tool := NewSubagentSpawnTool(reg)
	tool.SetOnSpawn(func(spawnParams *SubagentSpawnResult) error {
		return errors.New("boom")
	})

	out, err := tool.Execute(context.Background(), map[string]interface{}{"task": "x"})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.HasPrefix(out, "Error: failed to start subagent run:") {
		t.Fatalf("unexpected output: %s", out)
	}
}

func TestSubagentSpawnTool_CrossAgentUsesTargetBootstrapOwner(t *testing.T) {
	reg := &fakeSubagentRegistry{}
	tool := NewSubagentSpawnTool(reg)

	tool.SetAgentConfigGetter(func(agentID string) *config.AgentConfig {
		if agentID == "vibecoding" {
			return &config.AgentConfig{
				ID: "vibecoding",
				Subagents: &config.AgentSubagentConfig{
					AllowAgents: []string{"coder"},
				},
			}
		}
		return &config.AgentConfig{ID: agentID}
	})

	var spawned *SubagentSpawnResult
	tool.SetOnSpawn(func(spawnParams *SubagentSpawnResult) error {
		spawned = spawnParams
		return nil
	})

	ctx := context.Background()
	ctx = context.WithValue(ctx, "agent_id", "vibecoding")
	ctx = context.WithValue(ctx, "bootstrap_owner_id", "vibecoding")

	out, err := tool.Execute(ctx, map[string]interface{}{
		"task":     "implement current step",
		"agent_id": "coder",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "Subagent spawned successfully") {
		t.Fatalf("unexpected output: %s", out)
	}
	if spawned == nil {
		t.Fatalf("expected onSpawn callback to be called")
	}
	if spawned.BootstrapOwnerID != "coder" {
		t.Fatalf("expected bootstrap owner to switch to target agent, got %q", spawned.BootstrapOwnerID)
	}
}

func TestSubagentSpawnTool_RequiresExplicitAgentIDWhenAllowAgentsConfigured(t *testing.T) {
	reg := &fakeSubagentRegistry{}
	tool := NewSubagentSpawnTool(reg)

	tool.SetAgentConfigGetter(func(agentID string) *config.AgentConfig {
		if agentID == "vibecoding" {
			return &config.AgentConfig{
				ID: "vibecoding",
				Subagents: &config.AgentSubagentConfig{
					AllowAgents: []string{"coder", "frontend"},
				},
			}
		}
		return &config.AgentConfig{ID: agentID}
	})

	ctx := context.Background()
	ctx = context.WithValue(ctx, "agent_id", "vibecoding")

	out, err := tool.Execute(ctx, map[string]interface{}{
		"task": "implement current step",
	})
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if !strings.Contains(out, "Forbidden:") {
		t.Fatalf("expected forbidden output, got %s", out)
	}
	if !strings.Contains(out, "requires explicit agent_id") {
		t.Fatalf("expected explicit agent_id error, got %s", out)
	}
}
