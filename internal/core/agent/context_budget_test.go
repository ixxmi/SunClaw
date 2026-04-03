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

type capturingSummaryProvider struct {
	response   string
	lastPrompt string
}

func (p *capturingSummaryProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, options ...providers.ChatOption) (*providers.Response, error) {
	if len(messages) > 0 {
		p.lastPrompt = messages[0].Content
	}
	return &providers.Response{Content: p.response, FinishReason: "stop"}, nil
}

func (p *capturingSummaryProvider) ChatWithTools(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, options ...providers.ChatOption) (*providers.Response, error) {
	return p.Chat(ctx, messages, tools, options...)
}

func (p *capturingSummaryProvider) Close() error { return nil }

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

func TestSummarizeMessagesIncludesAssistantToolCallsAndToolResults(t *testing.T) {
	provider := &capturingSummaryProvider{response: "tool-aware summary"}
	got := summarizeMessages(context.Background(), provider, []providers.Message{
		{
			Role: "assistant",
			ToolCalls: []providers.ToolCall{
				{ID: "call-1", Name: "read_file", Params: map[string]interface{}{"path": "main.go"}},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call-1",
			ToolName:   "read_file",
			Content:    "package main\n\nfunc main() {}\n",
		},
	}, "", 512)

	if got != "tool-aware summary" {
		t.Fatalf("summary = %q, want %q", got, "tool-aware summary")
	}
	if !strings.Contains(provider.lastPrompt, "Tool calls: read_file(path)") {
		t.Fatalf("expected tool call digest in prompt, got %q", provider.lastPrompt)
	}
	if !strings.Contains(provider.lastPrompt, "Tool result from read_file: package main func main() {}") {
		t.Fatalf("expected tool result digest in prompt, got %q", provider.lastPrompt)
	}
}

func TestMaybeMicroCompactDropsOldestTurnAndBuildsSummary(t *testing.T) {
	state := NewAgentState()
	state.Messages = []AgentMessage{
		{Role: RoleUser, Content: []ContentBlock{TextContent{Text: "u1"}}},
		{Role: RoleAssistant, Content: []ContentBlock{ToolCallContent{ID: "call-1", Name: "read_file", Arguments: map[string]any{"path": "main.go"}}}},
		{Role: RoleToolResult, Content: []ContentBlock{TextContent{Text: "package main\nfunc main() {}"}}, Metadata: map[string]any{"tool_call_id": "call-1", "tool_name": "read_file"}},
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "a1"}}},
		{Role: RoleUser, Content: []ContentBlock{TextContent{Text: "u2"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "a2"}}},
		{Role: RoleUser, Content: []ContentBlock{TextContent{Text: "u3"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "a3"}}},
	}

	provider := &capturingSummaryProvider{response: "micro summary"}
	orchestrator := NewOrchestrator(&LoopConfig{
		Provider:      provider,
		ContextWindow: 1000,
		MaxTokens:     512,
	}, state)

	ok := orchestrator.maybeMicroCompact(context.Background(), state, 900)
	if !ok {
		t.Fatal("expected proactive micro compaction")
	}
	if state.ContextSummary != "micro summary" {
		t.Fatalf("summary = %q, want %q", state.ContextSummary, "micro summary")
	}
	if len(state.Messages) != 4 {
		t.Fatalf("remaining messages = %d, want 4", len(state.Messages))
	}
	if state.Messages[0].Role != RoleUser || extractTextContent(state.Messages[0]) != "u2" {
		t.Fatalf("expected oldest full turn to be compacted, got %#v", state.Messages[0])
	}
	if !strings.Contains(provider.lastPrompt, "Tool calls: read_file(path)") {
		t.Fatalf("expected tool call digest in micro compact prompt, got %q", provider.lastPrompt)
	}
	if !strings.Contains(provider.lastPrompt, "Tool result from read_file: package main func main() {}") {
		t.Fatalf("expected tool result digest in micro compact prompt, got %q", provider.lastPrompt)
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
		TextContent{Text: strings.Repeat("a", defaultToolResultChars+500)},
	}, defaultToolResultChars)

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

func TestToolResultCharBudgetUsesPerToolOverrides(t *testing.T) {
	if got := toolResultCharBudget("read_file"); got != readFileToolResultChars {
		t.Fatalf("read_file budget = %d, want %d", got, readFileToolResultChars)
	}
	if got := toolResultCharBudget("run_shell"); got != runShellToolResultChars {
		t.Fatalf("run_shell budget = %d, want %d", got, runShellToolResultChars)
	}
	if got := toolResultCharBudget("web_search"); got != defaultToolResultChars {
		t.Fatalf("default budget = %d, want %d", got, defaultToolResultChars)
	}
}
