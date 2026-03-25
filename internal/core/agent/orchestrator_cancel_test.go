package agent

import (
	"context"
	"strings"
	"testing"
	"time"
)

type cancelAwareTool struct{}

func (t *cancelAwareTool) Name() string { return "cancel_aware" }

func (t *cancelAwareTool) Description() string { return "waits for context cancellation" }

func (t *cancelAwareTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *cancelAwareTool) Label() string { return "cancel_aware" }

func (t *cancelAwareTool) Execute(ctx context.Context, params map[string]any, onUpdate func(ToolResult)) (ToolResult, error) {
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)
	return ToolResult{}, ctx.Err()
}

func TestExecuteToolCalls_ReportsContextCanceledInsteadOfTimeout(t *testing.T) {
	state := NewAgentState()
	state.Tools = []Tool{&cancelAwareTool{}}

	orchestrator := NewOrchestrator(&LoopConfig{ToolTimeout: time.Minute}, state)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan []AgentMessage, 1)
	go func() {
		results, _ := orchestrator.executeToolCalls(ctx, []ToolCallContent{
			{ID: "call-1", Name: "cancel_aware", Arguments: map[string]any{}},
		}, state)
		done <- results
	}()

	time.Sleep(10 * time.Millisecond)
	cancel()

	select {
	case results := <-done:
		if len(results) != 1 {
			t.Fatalf("expected 1 tool result, got %d", len(results))
		}
		got := extractTextContent(results[0])
		if !strings.Contains(got, "context canceled") {
			t.Fatalf("expected context canceled error, got %q", got)
		}
		if strings.Contains(got, "timed out") {
			t.Fatalf("did not expect timeout wording, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for executeToolCalls to finish")
	}
}
