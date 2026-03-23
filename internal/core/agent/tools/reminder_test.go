package tools

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/smallnest/goclaw/internal/core/bus"
	"github.com/smallnest/goclaw/internal/core/cron"
)

func TestReminderToolSchedulesConversationAwareJob(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "reminder-tool-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	cfg := cron.DefaultCronConfig()
	cfg.StorePath = filepath.Join(tempDir, "jobs.json")

	service, err := cron.NewService(cfg, bus.NewMessageBus(8))
	if err != nil {
		t.Fatalf("new cron service: %v", err)
	}

	tool := NewReminderTool(service)
	ctx := context.Background()
	ctx = context.WithValue(ctx, "channel", "wework")
	ctx = context.WithValue(ctx, "account_id", "bot1")
	ctx = context.WithValue(ctx, "chat_id", "chat-1")
	ctx = context.WithValue(ctx, "sender_id", "user-1")
	ctx = context.WithValue(ctx, "chat_type", "group")
	ctx = context.WithValue(ctx, "thread_id", "thread-1")

	if _, err := tool.scheduleReminder(ctx, map[string]interface{}{
		"instruction":   "Please send today's weather now.",
		"delay_seconds": 5.0,
	}); err != nil {
		t.Fatalf("scheduleReminder error: %v", err)
	}

	jobs := service.ListJobs()
	if len(jobs) != 1 {
		t.Fatalf("expected 1 job, got %d", len(jobs))
	}

	job := jobs[0]
	if job.Conversation == nil {
		t.Fatalf("expected conversation context on job")
	}
	if job.Conversation.Channel != "wework" || job.Conversation.AccountID != "bot1" || job.Conversation.ChatID != "chat-1" {
		t.Fatalf("unexpected conversation context: %+v", job.Conversation)
	}
	if got, _ := job.Conversation.Metadata["chat_type"].(string); got != "group" {
		t.Fatalf("expected chat_type group, got %#v", job.Conversation.Metadata["chat_type"])
	}
	if got, _ := job.Conversation.Metadata["thread_id"].(string); got != "thread-1" {
		t.Fatalf("expected thread_id thread-1, got %#v", job.Conversation.Metadata["thread_id"])
	}
	if job.Schedule.Type != cron.ScheduleTypeAt {
		t.Fatalf("expected one-shot at schedule, got %s", job.Schedule.Type)
	}
	if !job.Schedule.At.After(time.Now()) {
		t.Fatalf("expected schedule time in the future, got %s", job.Schedule.At.Format(time.RFC3339))
	}
}
