package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/smallnest/goclaw/internal/core/providers"
	"github.com/smallnest/goclaw/internal/core/session"
)

type summaryProvider struct {
	response string
}

func (p *summaryProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, options ...providers.ChatOption) (*providers.Response, error) {
	return &providers.Response{Content: p.response, FinishReason: "stop"}, nil
}

func (p *summaryProvider) ChatWithTools(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, options ...providers.ChatOption) (*providers.Response, error) {
	return p.Chat(ctx, messages, tools, options...)
}

func (p *summaryProvider) Close() error { return nil }

func TestFindSafeBoundaryPrefersPreviousUserTurn(t *testing.T) {
	history := []providers.Message{
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1"},
		{Role: "tool", Content: "tool1"},
		{Role: "user", Content: "u2"},
		{Role: "assistant", Content: "a2"},
		{Role: "user", Content: "u3"},
		{Role: "assistant", Content: "a3"},
	}

	if got := findSafeBoundary(history, 4); got != 3 {
		t.Fatalf("findSafeBoundary() = %d, want 3", got)
	}
}

func TestForceCompressionKeepsRecentTurnAndBuildsSummary(t *testing.T) {
	state := NewAgentState()
	state.Messages = []AgentMessage{
		{Role: RoleUser, Content: []ContentBlock{TextContent{Text: "u1"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "a1"}}},
		{Role: RoleUser, Content: []ContentBlock{TextContent{Text: "u2"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "a2"}}},
		{Role: RoleUser, Content: []ContentBlock{TextContent{Text: "u3"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "a3"}}},
	}

	orchestrator := NewOrchestrator(&LoopConfig{
		Provider:  &summaryProvider{response: "older summary"},
		MaxTokens: 512,
	}, state)

	result, ok := orchestrator.forceCompression(context.Background(), state)
	if !ok {
		t.Fatal("expected compression to happen")
	}
	if result.DroppedMessages != 2 {
		t.Fatalf("dropped = %d, want 2", result.DroppedMessages)
	}
	if len(state.Messages) != 4 {
		t.Fatalf("remaining messages = %d, want 4", len(state.Messages))
	}
	if state.Messages[0].Role != RoleUser || extractTextContent(state.Messages[0]) != "u2" {
		t.Fatalf("expected latest user turn to remain, got %#v", state.Messages[0])
	}
	if state.ContextSummary != "older summary" {
		t.Fatalf("summary = %q, want %q", state.ContextSummary, "older summary")
	}
}

func TestMaybeCompactSessionPersistsSummaryAndRecentMessages(t *testing.T) {
	sess := &session.Session{
		Key:      "chat-1",
		Messages: []session.Message{},
	}
	sess.AddMessage(session.Message{Role: "user", Content: "u1"})
	sess.AddMessage(session.Message{Role: "assistant", Content: "a1"})
	sess.AddMessage(session.Message{Role: "user", Content: "u2"})
	sess.AddMessage(session.Message{Role: "assistant", Content: "a2"})
	sess.AddMessage(session.Message{Role: "user", Content: "u3"})
	sess.AddMessage(session.Message{Role: "assistant", Content: "a3"})

	ok := maybeCompactSession(context.Background(), &summaryProvider{response: "session summary"}, sess, 2, 0, 512)
	if !ok {
		t.Fatal("expected session compaction")
	}

	history := sess.GetHistory(0)
	if len(history) != 2 {
		t.Fatalf("history len = %d, want 2", len(history))
	}
	if got := sess.GetSummary(); got != "session summary" {
		t.Fatalf("summary = %q, want %q", got, "session summary")
	}
	if history[0].Role != "user" || history[0].Content != "u3" {
		t.Fatalf("expected latest turn to remain, got %#v", history[0])
	}
}

func TestIsContextOverflowErrorRecognizesProviderContextMessage(t *testing.T) {
	err := errors.New("failed to generate content: HTTP 422 (Unprocessable Entity): {\"error\":{\"message\":\"请提供请求的上下文（traceid: abcdef）\"}}")
	if !isContextOverflowError(err) {
		t.Fatal("expected provider context-missing message to be treated as context overflow")
	}
}

func TestTruncateToolResultBlocksCapsOversizedOutput(t *testing.T) {
	got := truncateToolResultBlocks([]ContentBlock{
		TextContent{Text: strings.Repeat("a", maxToolResultChars+500)},
	}, maxToolResultChars)

	if len(got) != 1 {
		t.Fatalf("expected 1 content block, got %d", len(got))
	}
	text, ok := got[0].(TextContent)
	if !ok {
		t.Fatalf("expected text content block, got %#v", got[0])
	}
	if !strings.Contains(text.Text, "tool output truncated") {
		t.Fatalf("expected truncation note in output")
	}
	if !strings.HasPrefix(text.Text, strings.Repeat("a", 128)) {
		t.Fatalf("expected original prefix to be preserved")
	}
}
