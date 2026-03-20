package agent

import "testing"

func TestFindLatestReplyableAssistantMessage_PicksLatestAssistantBeforeTrailingNonAssistant(t *testing.T) {
	msg := findLatestReplyableAssistantMessage([]AgentMessage{
		{Role: RoleUser, Content: []ContentBlock{TextContent{Text: "question"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "final answer"}}},
		{Role: RoleUser, Content: []ContentBlock{TextContent{Text: "[SYSTEM] follow-up"}}},
	})
	if msg == nil {
		t.Fatalf("expected replyable assistant message")
	}
	if got := extractTextContent(*msg); got != "final answer" {
		t.Fatalf("unexpected reply text: %q", got)
	}
}

func TestFindLatestReplyableAssistantMessage_SkipsEmptyAssistantAndUsesPreviousReply(t *testing.T) {
	msg := findLatestReplyableAssistantMessage([]AgentMessage{
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "use this"}}},
		{Role: RoleAssistant, Content: []ContentBlock{ToolCallContent{ID: "call-1", Name: "tool"}}},
		{Role: RoleToolResult, Content: []ContentBlock{TextContent{Text: "done"}}},
	})
	if msg == nil {
		t.Fatalf("expected previous replyable assistant message")
	}
	if got := extractTextContent(*msg); got != "use this" {
		t.Fatalf("unexpected reply text: %q", got)
	}
}

func TestFindLatestReplyableAssistantMessage_DropsSilentReply(t *testing.T) {
	msg := findLatestReplyableAssistantMessage([]AgentMessage{
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "visible"}}},
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "SILENT_REPLY"}}},
	})
	if msg != nil {
		t.Fatalf("expected nil for silent reply, got %q", extractTextContent(*msg))
	}
}

func TestFindLatestReplyableAssistantMessage_DropsHeartbeatAck(t *testing.T) {
	msg := findLatestReplyableAssistantMessage([]AgentMessage{
		{Role: RoleAssistant, Content: []ContentBlock{TextContent{Text: "HEARTBEAT_OK"}}},
	})
	if msg != nil {
		t.Fatalf("expected nil for heartbeat ack, got %q", extractTextContent(*msg))
	}
}
