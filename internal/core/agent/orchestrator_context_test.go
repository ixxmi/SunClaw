package agent

import (
	"context"
	"testing"
)

type contextCaptureTool struct {
	sessionKey       string
	agentID          string
	bootstrapOwnerID string
}

func (t *contextCaptureTool) Name() string { return "capture_context" }

func (t *contextCaptureTool) Description() string { return "capture tool context" }

func (t *contextCaptureTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *contextCaptureTool) Label() string { return "capture_context" }

func (t *contextCaptureTool) Execute(ctx context.Context, params map[string]any, onUpdate func(ToolResult)) (ToolResult, error) {
	if sk, ok := ctx.Value("session_key").(string); ok {
		t.sessionKey = sk
	}
	if aid, ok := ctx.Value("agent_id").(string); ok {
		t.agentID = aid
	}
	if bid, ok := ctx.Value("bootstrap_owner_id").(string); ok {
		t.bootstrapOwnerID = bid
	}
	return ToolResult{
		Content: []ContentBlock{TextContent{Text: "ok"}},
	}, nil
}

func TestExecuteToolCalls_InjectsStringContextKeysForTools(t *testing.T) {
	state := NewAgentState()
	state.SessionKey = "wework:default:chat-1"
	state.AgentID = "reviewer"
	state.BootstrapOwnerID = "vibecoding"

	tool := &contextCaptureTool{}
	state.Tools = []Tool{tool}

	orchestrator := NewOrchestrator(&LoopConfig{}, state)
	_, _ = orchestrator.executeToolCalls(context.Background(), []ToolCallContent{
		{ID: "call-1", Name: tool.Name(), Arguments: map[string]any{}},
	}, state)

	if tool.sessionKey != state.SessionKey {
		t.Fatalf("expected session_key %q, got %q", state.SessionKey, tool.sessionKey)
	}
	if tool.agentID != state.AgentID {
		t.Fatalf("expected agent_id %q, got %q", state.AgentID, tool.agentID)
	}
	if tool.bootstrapOwnerID != state.BootstrapOwnerID {
		t.Fatalf("expected bootstrap_owner_id %q, got %q", state.BootstrapOwnerID, tool.bootstrapOwnerID)
	}
}
