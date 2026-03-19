package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/smallnest/goclaw/cron"
)

func buildConversationContextFromToolContext(ctx context.Context) *cron.ConversationContext {
	channel := contextString(ctx, "channel")
	chatID := contextString(ctx, "chat_id")
	if channel == "" || chatID == "" {
		return nil
	}

	metadata := make(map[string]interface{}, 2)
	if chatType := contextString(ctx, "chat_type"); chatType != "" {
		metadata["chat_type"] = chatType
	}
	if threadID := contextString(ctx, "thread_id"); threadID != "" {
		metadata["thread_id"] = threadID
	}
	if len(metadata) == 0 {
		metadata = nil
	}

	return &cron.ConversationContext{
		Channel:   channel,
		AccountID: contextString(ctx, "account_id"),
		ChatID:    chatID,
		SenderID:  contextString(ctx, "sender_id"),
		Metadata:  metadata,
	}
}

// ReminderTool schedules a future agent turn back into the current chat context.
type ReminderTool struct {
	service *cron.Service
}

func NewReminderTool(service *cron.Service) *ReminderTool {
	return &ReminderTool{service: service}
}

func (t *ReminderTool) scheduleReminder(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.service == nil {
		return "", fmt.Errorf("reminder service is not available")
	}

	instruction, _ := params["instruction"].(string)
	instruction = strings.TrimSpace(instruction)
	if instruction == "" {
		return "", fmt.Errorf("instruction parameter is required")
	}

	conversation := buildConversationContextFromToolContext(ctx)
	if conversation == nil {
		return "", fmt.Errorf("reminder requires an active chat session context")
	}

	schedule, summary, err := parseReminderSchedule(params, time.Now())
	if err != nil {
		return "", err
	}

	name, _ := params["name"].(string)
	name = strings.TrimSpace(name)
	if name == "" {
		name = buildReminderName(instruction)
	}

	job := &cron.Job{
		Name:          name,
		Schedule:      schedule,
		SessionTarget: cron.SessionTargetMain,
		WakeMode:      cron.WakeModeNow,
		Payload: cron.Payload{
			Type:    cron.PayloadTypeAgentTurn,
			Message: instruction,
		},
		Conversation: conversation,
		State: cron.JobState{
			Enabled: true,
		},
	}

	if err := t.service.AddJob(job); err != nil {
		return "", fmt.Errorf("failed to schedule reminder: %w", err)
	}

	return fmt.Sprintf("Reminder scheduled: %s (job_id=%s)", summary, job.ID), nil
}

func buildReminderName(instruction string) string {
	name := strings.TrimSpace(instruction)
	if name == "" {
		return "scheduled reminder"
	}
	runes := []rune(name)
	if len(runes) > 40 {
		name = string(runes[:40]) + "..."
	}
	return "reminder: " + name
}

func parseReminderSchedule(params map[string]interface{}, now time.Time) (cron.Schedule, string, error) {
	var schedule cron.Schedule
	count := 0
	var summary string

	if atRaw, ok := params["at"].(string); ok && strings.TrimSpace(atRaw) != "" {
		count++
		at, err := time.Parse(time.RFC3339, strings.TrimSpace(atRaw))
		if err != nil {
			return schedule, "", fmt.Errorf("invalid at time: must be RFC3339 (%w)", err)
		}
		if !at.After(now) {
			return schedule, "", fmt.Errorf("at time must be in the future")
		}
		schedule.Type = cron.ScheduleTypeAt
		schedule.At = at
		summary = at.Format(time.RFC3339)
	}

	if delay, ok, err := parseDelaySeconds(params["delay_seconds"]); err != nil {
		return schedule, "", err
	} else if ok {
		count++
		schedule.Type = cron.ScheduleTypeAt
		schedule.At = now.Add(delay)
		summary = fmt.Sprintf("in %s", delay.Round(time.Second))
	}

	if everyRaw, ok := params["every"].(string); ok && strings.TrimSpace(everyRaw) != "" {
		count++
		every, err := cron.ParseHumanDuration(strings.TrimSpace(everyRaw))
		if err != nil {
			return schedule, "", fmt.Errorf("invalid every duration: %w", err)
		}
		schedule.Type = cron.ScheduleTypeEvery
		schedule.EveryDuration = every
		summary = fmt.Sprintf("every %s", every)
	}

	if cronExpr, ok := params["cron"].(string); ok && strings.TrimSpace(cronExpr) != "" {
		count++
		schedule.Type = cron.ScheduleTypeCron
		schedule.CronExpression = strings.TrimSpace(cronExpr)
		summary = fmt.Sprintf("cron %s", schedule.CronExpression)
	}

	if count == 0 {
		return schedule, "", fmt.Errorf("one of at, delay_seconds, every, or cron is required")
	}
	if count > 1 {
		return schedule, "", fmt.Errorf("only one of at, delay_seconds, every, or cron may be specified")
	}

	return schedule, summary, nil
}

func parseDelaySeconds(value interface{}) (time.Duration, bool, error) {
	switch v := value.(type) {
	case nil:
		return 0, false, nil
	case int:
		if v <= 0 {
			return 0, false, fmt.Errorf("delay_seconds must be greater than 0")
		}
		return time.Duration(v) * time.Second, true, nil
	case int64:
		if v <= 0 {
			return 0, false, fmt.Errorf("delay_seconds must be greater than 0")
		}
		return time.Duration(v) * time.Second, true, nil
	case float64:
		if v <= 0 {
			return 0, false, fmt.Errorf("delay_seconds must be greater than 0")
		}
		return time.Duration(v * float64(time.Second)), true, nil
	default:
		return 0, false, fmt.Errorf("delay_seconds must be a number")
	}
}

func (t *ReminderTool) GetTools() []Tool {
	return []Tool{
		NewBaseTool(
			"reminder",
			"Schedule a future proactive follow-up in the current chat. Use this for delayed replies and future reminders. The instruction is what the assistant should do at trigger time.",
			map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"instruction": map[string]interface{}{
						"type":        "string",
						"description": "What the assistant should do when the reminder fires. Example: 'Please send today's weather to the user now.'",
					},
					"name": map[string]interface{}{
						"type":        "string",
						"description": "Optional human-readable reminder name.",
					},
					"at": map[string]interface{}{
						"type":        "string",
						"description": "One-shot trigger time in RFC3339 format, for example 2026-03-14T07:00:00+08:00.",
					},
					"delay_seconds": map[string]interface{}{
						"type":        "number",
						"description": "One-shot delay in seconds from now, for example 5.",
					},
					"every": map[string]interface{}{
						"type":        "string",
						"description": "Repeat interval like 30s, 5m, 2h, 1d.",
					},
					"cron": map[string]interface{}{
						"type":        "string",
						"description": "Recurring cron expression in standard 5-field or 6-field format.",
					},
				},
				"required": []string{"instruction"},
			},
			t.scheduleReminder,
		),
	}
}
