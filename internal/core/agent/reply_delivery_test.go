package agent

import (
	"context"
	"testing"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/channels"
	"github.com/smallnest/goclaw/internal/core/config"
)

type replyDeliveryTestChannel struct {
	name      string
	accountID string
	streamed  chan []*bus.StreamMessage
}

func (c *replyDeliveryTestChannel) Name() string { return c.name }

func (c *replyDeliveryTestChannel) AccountID() string { return c.accountID }

func (c *replyDeliveryTestChannel) Start(ctx context.Context) error { return nil }

func (c *replyDeliveryTestChannel) Stop() error { return nil }

func (c *replyDeliveryTestChannel) Send(msg *bus.OutboundMessage) error { return nil }

func (c *replyDeliveryTestChannel) SendStream(chatID string, stream <-chan *bus.StreamMessage) error {
	var messages []*bus.StreamMessage
	for msg := range stream {
		messages = append(messages, msg)
	}
	c.streamed <- messages
	return nil
}

func (c *replyDeliveryTestChannel) IsAllowed(senderID string) bool { return true }

func (c *replyDeliveryTestChannel) SupportsReplyStreamEdit() bool { return true }

func TestSplitReplyForDeliveryRespectsBoundaries(t *testing.T) {
	chunks := splitReplyForDelivery("第一句。第二句。第三句。", config.ReplyDeliveryConfig{
		MinChunkChars: 3,
		MaxChunkChars: 6,
		MaxPushCount:  5,
	})

	if len(chunks) != 3 {
		t.Fatalf("len(chunks) = %d, want 3 (%#v)", len(chunks), chunks)
	}
	if chunks[0] != "第一句。" || chunks[1] != "第二句。" || chunks[2] != "第三句。" {
		t.Fatalf("unexpected chunks: %#v", chunks)
	}
}

func TestSplitReplyForDeliveryDefaultDoesNotOverFragment(t *testing.T) {
	content := "收到，我已经帮你看过这块逻辑了。整体没有明显问题，主要是把边界处理和错误提示再顺一遍会更自然。现在先把结论发你，后面如果需要我再继续细化。"

	chunks := splitReplyForDelivery(content, config.ReplyDeliveryConfig{})

	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1 (%#v)", len(chunks), chunks)
	}
	if chunks[0] != content {
		t.Fatalf("unexpected chunk content: %#v", chunks)
	}
}

func TestSplitReplyForDeliveryConversationalTwoBeat(t *testing.T) {
	content := "哎，心情不好的时候真的很难受。发生什么事了？想说就说，我听着。"

	chunks := splitReplyForDelivery(content, config.ReplyDeliveryConfig{})

	if len(chunks) != 2 {
		t.Fatalf("len(chunks) = %d, want 2 (%#v)", len(chunks), chunks)
	}
	if chunks[0] != "哎，心情不好的时候真的很难受。" {
		t.Fatalf("unexpected first chunk: %q", chunks[0])
	}
	if chunks[1] != "发生什么事了？想说就说，我听着。" {
		t.Fatalf("unexpected second chunk: %q", chunks[1])
	}
}

func TestSplitReplyForDeliveryDoesNotSplitShortTechnicalReply(t *testing.T) {
	content := "可以。需要我现在改吗？"

	chunks := splitReplyForDelivery(content, config.ReplyDeliveryConfig{})

	if len(chunks) != 1 {
		t.Fatalf("len(chunks) = %d, want 1 (%#v)", len(chunks), chunks)
	}
	if chunks[0] != content {
		t.Fatalf("unexpected chunk content: %#v", chunks)
	}
}

func TestReplyDeliveryExecutorPublishesMultiPush(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	executor := newReplyDeliveryExecutor(&config.Config{
		ReplyDelivery: config.ReplyDeliveryConfig{
			Mode:          config.ReplyDeliveryModeMultiPush,
			MinChunkChars: 3,
			MaxChunkChars: 6,
			MinDelayMs:    1,
			MaxDelayMs:    1,
			MaxPushCount:  5,
		},
	}, messageBus, nil)
	executor.sleep = func(context.Context, time.Duration) error { return nil }

	if err := executor.Publish(context.Background(), &bus.OutboundMessage{
		Channel: "weixin",
		ChatID:  "chat-1",
		Content: "第一句。第二句。第三句。",
		ReplyTo: "msg-1",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var contents []string
	for len(contents) < 3 {
		select {
		case msg := <-sub.Channel:
			contents = append(contents, msg.Content)
			if len(contents) == 1 && msg.ReplyTo != "msg-1" {
				t.Fatalf("expected first chunk to preserve reply_to, got %q", msg.ReplyTo)
			}
			if len(contents) > 1 && msg.ReplyTo != "" {
				t.Fatalf("expected follow-up chunks to clear reply_to, got %q", msg.ReplyTo)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting outbound chunks: %#v", contents)
		}
	}

	if contents[0] != "第一句。" || contents[1] != "第二句。" || contents[2] != "第三句。" {
		t.Fatalf("unexpected outbound chunks: %#v", contents)
	}
}

func TestReplyDeliveryExecutorDispatchesStreamEdit(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	channelMgr := channels.NewManager(messageBus)
	testChannel := &replyDeliveryTestChannel{
		name:      "telegram",
		accountID: "default",
		streamed:  make(chan []*bus.StreamMessage, 1),
	}
	if err := channelMgr.Register(testChannel); err != nil {
		t.Fatalf("register channel: %v", err)
	}

	executor := newReplyDeliveryExecutor(&config.Config{
		ReplyDelivery: config.ReplyDeliveryConfig{
			Mode:          config.ReplyDeliveryModeStreamEdit,
			MinChunkChars: 3,
			MaxChunkChars: 6,
			MinDelayMs:    1,
			MaxDelayMs:    1,
			MaxPushCount:  5,
		},
	}, messageBus, channelMgr)
	executor.sleep = func(context.Context, time.Duration) error { return nil }

	if err := executor.Publish(context.Background(), &bus.OutboundMessage{
		Channel: "telegram",
		ChatID:  "chat-1",
		Content: "第一句。第二句。",
		ReplyTo: "42",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}

	select {
	case messages := <-testChannel.streamed:
		if len(messages) != 3 {
			t.Fatalf("len(messages) = %d, want 3", len(messages))
		}
		if messages[0].Content != "第一句。" || messages[1].Content != "第二句。" || !messages[2].IsComplete {
			t.Fatalf("unexpected streamed messages: %#v", messages)
		}
		if got, _ := messages[0].Metadata["reply_to"].(string); got != "42" {
			t.Fatalf("expected first stream message to carry reply_to metadata, got %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting streamed messages")
	}
}
