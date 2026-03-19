package agent

import (
	"context"
	"strings"

	"github.com/smallnest/goclaw/bus"
)

func withInboundToolContext(ctx context.Context, msg *bus.InboundMessage) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if msg == nil {
		return ctx
	}

	ctx = context.WithValue(ctx, "channel", msg.Channel)
	ctx = context.WithValue(ctx, "account_id", msg.AccountID)
	ctx = context.WithValue(ctx, "chat_id", msg.ChatID)
	ctx = context.WithValue(ctx, "sender_id", msg.SenderID)

	if msg.Metadata != nil {
		if chatType, ok := msg.Metadata["chat_type"].(string); ok && strings.TrimSpace(chatType) != "" {
			ctx = context.WithValue(ctx, "chat_type", chatType)
		}
		if threadID, ok := msg.Metadata["thread_id"].(string); ok && strings.TrimSpace(threadID) != "" {
			ctx = context.WithValue(ctx, "thread_id", threadID)
		}
	}

	return ctx
}
