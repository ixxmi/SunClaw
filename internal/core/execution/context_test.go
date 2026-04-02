package execution

import (
	"context"
	"testing"

	"github.com/smallnest/goclaw/internal/core/bus"
)

func TestWithToolUseContext_PreservesLegacyKeysAndTypedAccessors(t *testing.T) {
	ctx := WithToolUseContext(context.Background(), ToolUseContext{
		SessionKey:       "agent:main:chat-1",
		AgentID:          "reviewer",
		BootstrapOwnerID: "vibecoding",
		WorkspaceRoot:    "/tmp/workspace",
		LoopIteration:    3,
		Channel:          "wework",
		AccountID:        "bot-1",
		ChatID:           "chat-1",
		SenderID:         "user-1",
		TenantID:         "tenant-a",
		ThreadID:         "thread-9",
	})

	if got := SessionKey(ctx); got != "agent:main:chat-1" {
		t.Fatalf("unexpected session key: %q", got)
	}
	if got := AgentID(ctx); got != "reviewer" {
		t.Fatalf("unexpected agent id: %q", got)
	}
	if got := BootstrapOwnerID(ctx); got != "vibecoding" {
		t.Fatalf("unexpected bootstrap owner id: %q", got)
	}
	if got := WorkspaceRoot(ctx); got != "/tmp/workspace" {
		t.Fatalf("unexpected workspace root: %q", got)
	}
	if got := LoopIteration(ctx); got != 3 {
		t.Fatalf("unexpected loop iteration: %d", got)
	}
	if got := Channel(ctx); got != "wework" {
		t.Fatalf("unexpected channel: %q", got)
	}
	if got := ChatID(ctx); got != "chat-1" {
		t.Fatalf("unexpected chat id: %q", got)
	}
	if raw, _ := ctx.Value("session_key").(string); raw != "agent:main:chat-1" {
		t.Fatalf("legacy session_key not mirrored: %q", raw)
	}
	if raw, _ := ctx.Value("workspace_root").(string); raw != "/tmp/workspace" {
		t.Fatalf("legacy workspace_root not mirrored: %q", raw)
	}
}

func TestWithInboundMessage_ExtractsRoutingMetadata(t *testing.T) {
	ctx := WithInboundMessage(context.Background(), &bus.InboundMessage{
		Channel:   "feishu",
		AccountID: "default",
		ChatID:    "chat-1",
		SenderID:  "sender-1",
		Metadata: map[string]interface{}{
			"tenant_id": "tenant-x",
			"thread_id": "thread-1",
			"chat_type": "group",
		},
	})

	if got := Channel(ctx); got != "feishu" {
		t.Fatalf("unexpected channel: %q", got)
	}
	if got := AccountID(ctx); got != "default" {
		t.Fatalf("unexpected account id: %q", got)
	}
	if got := ChatID(ctx); got != "chat-1" {
		t.Fatalf("unexpected chat id: %q", got)
	}
	if got := SenderID(ctx); got != "sender-1" {
		t.Fatalf("unexpected sender id: %q", got)
	}
	if got := TenantID(ctx); got != "tenant-x" {
		t.Fatalf("unexpected tenant id: %q", got)
	}
	if got := ThreadID(ctx); got != "thread-1" {
		t.Fatalf("unexpected thread id: %q", got)
	}
	if got := ChatType(ctx); got != "group" {
		t.Fatalf("unexpected chat type: %q", got)
	}
}
