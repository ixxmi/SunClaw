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

	if msg.Metadata != nil {
		if threadID, ok := msg.Metadata["thread_id"].(string); ok && strings.TrimSpace(threadID) != "" {
			ctx = context.WithValue(ctx, "thread_id", threadID)
		}
	}

	return ctx
}
