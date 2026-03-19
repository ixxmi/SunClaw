package tools

import (
	"context"
	"testing"
	"time"

	"github.com/smallnest/goclaw/bus"
)

func TestMessageToolSendMessageUsesContextAndAccountID(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	tool := NewMessageTool(messageBus)

	ctx := context.Background()
	ctx = context.WithValue(ctx, "channel", "wework")
	ctx = context.WithValue(ctx, "account_id", "bot1")
	ctx = context.WithValue(ctx, "chat_id", "chat-1")

	if _, err := tool.SendMessage(ctx, map[string]interface{}{
		"content": "hello",
	}); err != nil {
		t.Fatalf("SendMessage error: %v", err)
	}

	select {
	case msg := <-sub.Channel:
		if msg == nil {
			t.Fatalf("expected outbound message")
		}
		if msg.Channel != "wework" || msg.AccountID != "bot1" || msg.ChatID != "chat-1" {
			t.Fatalf("unexpected outbound message: %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting outbound message")
	}
}
