package agent

import (
	"context"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/execution"
)

func withInboundToolContext(ctx context.Context, msg *bus.InboundMessage) context.Context {
	return execution.WithInboundMessage(ctx, msg)
}
