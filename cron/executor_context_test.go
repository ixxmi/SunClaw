package cron

import (
	"context"
	"testing"
	"time"

	"github.com/smallnest/goclaw/bus"
)

func TestExecuteAgentTurnPublishesBackToConversationContext(t *testing.T) {
	messageBus := bus.NewMessageBus(4)
	executor := NewJobExecutor(messageBus, nil, 0)

	job := &Job{
		ID:   "job-ctx1",
		Name: "ctx reminder",
		Payload: Payload{
			Type:    PayloadTypeAgentTurn,
			Message: "Please send the weather now.",
		},
		Conversation: &ConversationContext{
			Channel:   "wework",
			AccountID: "bot1",
			ChatID:    "chat-1",
			SenderID:  "user-1",
			Metadata: map[string]interface{}{
				"chat_type": "group",
				"thread_id": "thread-1",
			},
		},
	}

	if err := executor.Execute(context.Background(), job); err != nil {
		t.Fatalf("Execute error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	msg, err := messageBus.ConsumeInbound(ctx)
	if err != nil {
		t.Fatalf("ConsumeInbound error: %v", err)
	}

	if msg.Channel != "wework" || msg.AccountID != "bot1" || msg.ChatID != "chat-1" {
		t.Fatalf("unexpected conversation routing: %#v", msg)
	}
	if msg.SenderID != "user-1" {
		t.Fatalf("expected sender_id user-1, got %q", msg.SenderID)
	}
	if got, _ := msg.Metadata["chat_type"].(string); got != "group" {
		t.Fatalf("expected chat_type group, got %#v", msg.Metadata["chat_type"])
	}
	if got, _ := msg.Metadata["thread_id"].(string); got != "thread-1" {
		t.Fatalf("expected thread_id thread-1, got %#v", msg.Metadata["thread_id"])
	}
	if scheduled, _ := msg.Metadata["scheduled"].(bool); !scheduled {
		t.Fatalf("expected scheduled metadata flag")
	}
}
