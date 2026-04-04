package agent

import (
	"context"
	"testing"
)

type scriptedTool struct {
	name   string
	result ToolResult
	err    error
}

func (s *scriptedTool) Name() string               { return s.name }
func (s *scriptedTool) Description() string        { return "" }
func (s *scriptedTool) Parameters() map[string]any { return map[string]any{} }
func (s *scriptedTool) Label() string              { return s.name }
func (s *scriptedTool) Execute(ctx context.Context, params map[string]any, onUpdate func(ToolResult)) (ToolResult, error) {
	return s.result, s.err
}

func TestShouldTrackPendingSubagent(t *testing.T) {
	success := ToolResult{Content: []ContentBlock{TextContent{Text: "Subagent spawned successfully. Agent: coder. Run ID: r1, Session: s1"}}}
	continued := ToolResult{Content: []ContentBlock{TextContent{Text: "{\n  \"status\": \"continued\",\n  \"previous_task_id\": \"t1\",\n  \"task_id\": \"t2\"\n}"}}}
	failed := ToolResult{Content: []ContentBlock{TextContent{Text: "Error: failed to start subagent run: boom"}}}

	if !shouldTrackPendingSubagent("sessions_spawn", success, nil) {
		t.Fatalf("expected accepted sessions_spawn result to track pending subagent")
	}
	if !shouldTrackPendingSubagent("task_continue", continued, nil) {
		t.Fatalf("expected continued task to track pending subagent")
	}
	if shouldTrackPendingSubagent("sessions_spawn", failed, nil) {
		t.Fatalf("did not expect failed sessions_spawn text result to track pending subagent")
	}
	if shouldTrackPendingSubagent("read_file", success, nil) {
		t.Fatalf("did not expect non-sessions_spawn tool to track pending subagent")
	}
	if shouldTrackPendingSubagent("sessions_spawn", success, context.Canceled) {
		t.Fatalf("did not expect errored sessions_spawn call to track pending subagent")
	}
}

func TestExecuteToolCalls_DoesNotIncrementPendingForFailedSpawnTextResult(t *testing.T) {
	state := NewAgentState()
	state.Tools = []Tool{
		&scriptedTool{
			name: "sessions_spawn",
			result: ToolResult{
				Content: []ContentBlock{TextContent{Text: "Error: failed to start subagent run: boom"}},
			},
		},
	}

	o := NewOrchestrator(&LoopConfig{}, state)
	toolCalls := []ToolCallContent{
		{ID: "call-1", Name: "sessions_spawn", Arguments: map[string]any{"task": "x"}},
	}

	results, steering := o.executeToolCalls(context.Background(), toolCalls, state)
	if len(steering) != 0 {
		t.Fatalf("expected no steering messages, got %d", len(steering))
	}
	if len(results) != 1 {
		t.Fatalf("expected one tool result, got %d", len(results))
	}
	if state.HasPendingSubagents() {
		t.Fatalf("expected no pending subagents after failed spawn text result")
	}
}

func TestExecuteToolCalls_IncrementsPendingForAcceptedSpawn(t *testing.T) {
	state := NewAgentState()
	state.Tools = []Tool{
		&scriptedTool{
			name: "sessions_spawn",
			result: ToolResult{
				Content: []ContentBlock{TextContent{Text: "Subagent spawned successfully. Agent: coder. Run ID: r1, Session: s1"}},
			},
		},
	}

	o := NewOrchestrator(&LoopConfig{}, state)
	toolCalls := []ToolCallContent{
		{ID: "call-1", Name: "sessions_spawn", Arguments: map[string]any{"task": "x"}},
	}

	_, _ = o.executeToolCalls(context.Background(), toolCalls, state)
	if !state.HasPendingSubagents() {
		t.Fatalf("expected pending subagent after accepted spawn")
	}
}
