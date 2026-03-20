package tools

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/goclaw/bus"
	"github.com/smallnest/goclaw/channels"
)

func TestMessageToolSendMessageUsesContextAndAccountID(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	tool := NewMessageTool(messageBus, "", nil, nil)

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

func TestMessageToolSendFileUsesBase64Image(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	tool := NewMessageTool(messageBus, "", nil, nil)

	ctx := context.Background()
	ctx = context.WithValue(ctx, "channel", "wework")
	ctx = context.WithValue(ctx, "account_id", "bot1")
	ctx = context.WithValue(ctx, "chat_id", "chat-1")

	raw := []byte("image-bytes")
	if _, err := tool.SendFile(ctx, map[string]interface{}{
		"media_type":  "image",
		"base64_data": base64.StdEncoding.EncodeToString(raw),
		"file_name":   "progress.png",
		"content":     "处理中",
	}); err != nil {
		t.Fatalf("SendFile error: %v", err)
	}

	select {
	case msg := <-sub.Channel:
		if msg == nil {
			t.Fatalf("expected outbound message")
		}
		if msg.Channel != "wework" || msg.AccountID != "bot1" || msg.ChatID != "chat-1" {
			t.Fatalf("unexpected outbound message routing: %#v", msg)
		}
		if msg.Content != "处理中" {
			t.Fatalf("unexpected outbound content: %q", msg.Content)
		}
		if len(msg.Media) != 1 {
			t.Fatalf("expected 1 media item, got %d", len(msg.Media))
		}
		media := msg.Media[0]
		if media.Type != channels.UnifiedMediaImage {
			t.Fatalf("unexpected media type: %q", media.Type)
		}
		if media.Name != "progress.png" {
			t.Fatalf("unexpected media name: %q", media.Name)
		}
		if media.Base64 != base64.StdEncoding.EncodeToString(raw) {
			t.Fatalf("unexpected media base64 payload")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting outbound message")
	}
}

func TestMessageToolSendFileUsesLocalPathAndHonorsAllowlist(t *testing.T) {
	messageBus := bus.NewMessageBus(16)
	sub := messageBus.SubscribeOutbound()
	defer sub.Unsubscribe()

	dir := t.TempDir()
	filePath := filepath.Join(dir, "report.pdf")
	if err := os.WriteFile(filePath, []byte("pdf-content"), 0644); err != nil {
		t.Fatalf("WriteFile error: %v", err)
	}

	tool := NewMessageTool(messageBus, dir, []string{dir}, nil)

	ctx := context.Background()
	ctx = context.WithValue(ctx, "channel", "wework")
	ctx = context.WithValue(ctx, "chat_id", "chat-1")

	if _, err := tool.SendFile(ctx, map[string]interface{}{
		"file_path": filePath,
	}); err != nil {
		t.Fatalf("SendFile error: %v", err)
	}

	select {
	case msg := <-sub.Channel:
		if msg == nil {
			t.Fatalf("expected outbound message")
		}
		if len(msg.Media) != 1 {
			t.Fatalf("expected 1 media item, got %d", len(msg.Media))
		}
		media := msg.Media[0]
		if media.Type != channels.UnifiedMediaFile {
			t.Fatalf("unexpected media type: %q", media.Type)
		}
		if media.Name != "report.pdf" {
			t.Fatalf("unexpected media name: %q", media.Name)
		}
		if media.Base64 == "" {
			t.Fatalf("expected base64 payload for local file")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting outbound message")
	}

	deniedTool := NewMessageTool(messageBus, dir, []string{filepath.Join(dir, "allowed")}, nil)
	if _, err := deniedTool.SendFile(ctx, map[string]interface{}{
		"file_path": filePath,
	}); err == nil {
		t.Fatalf("expected allowlist enforcement error")
	}
}
