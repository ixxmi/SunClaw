package channels

import (
	"context"
	"testing"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
)

type outboundTestChannel struct {
	name      string
	accountID string
	sent      chan *bus.OutboundMessage
}

func (c *outboundTestChannel) Name() string { return c.name }

func (c *outboundTestChannel) AccountID() string { return c.accountID }

func (c *outboundTestChannel) Start(ctx context.Context) error { return nil }

func (c *outboundTestChannel) Stop() error { return nil }

func (c *outboundTestChannel) Send(msg *bus.OutboundMessage) error {
	c.sent <- msg
	return nil
}

func (c *outboundTestChannel) SendStream(chatID string, stream <-chan *bus.StreamMessage) error {
	return nil
}

func (c *outboundTestChannel) IsAllowed(senderID string) bool { return true }

func TestDispatchOutboundPrefersAccountScopedChannel(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	mgr := NewManager(messageBus)

	defaultChannel := &outboundTestChannel{
		name:      "wework",
		accountID: "default",
		sent:      make(chan *bus.OutboundMessage, 1),
	}
	accountChannel := &outboundTestChannel{
		name:      "wework",
		accountID: "bot1",
		sent:      make(chan *bus.OutboundMessage, 1),
	}

	if err := mgr.RegisterWithName(defaultChannel, "wework"); err != nil {
		t.Fatalf("register default channel: %v", err)
	}
	if err := mgr.RegisterWithName(accountChannel, "wework:bot1"); err != nil {
		t.Fatalf("register account channel: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.DispatchOutbound(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	if err := messageBus.PublishOutbound(context.Background(), &bus.OutboundMessage{
		Channel:   "wework",
		AccountID: "bot1",
		ChatID:    "chat-1",
		Content:   "hello",
		Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("publish outbound: %v", err)
	}

	select {
	case msg := <-accountChannel.sent:
		if msg == nil || msg.AccountID != "bot1" {
			t.Fatalf("unexpected account channel message: %#v", msg)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting account-scoped send")
	}

	select {
	case msg := <-defaultChannel.sent:
		t.Fatalf("expected default channel to remain idle, got %#v", msg)
	default:
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil && err != context.Canceled {
			t.Fatalf("dispatch outbound returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting dispatcher shutdown")
	}
}
