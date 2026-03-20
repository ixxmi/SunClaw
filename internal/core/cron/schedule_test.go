package cron

import (
	"testing"
	"time"
)

func TestParseCronExpressionNextDayWhenHourAlreadyPassed(t *testing.T) {
	from := time.Date(2026, 2, 28, 9, 52, 48, 0, time.UTC)
	next, err := parseCronExpression("0 8 * * *", from)
	if err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}

	expected := time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC)
	if !next.Equal(expected) {
		t.Fatalf("expected %s, got %s", expected.Format(time.RFC3339), next.Format(time.RFC3339))
	}
}

func TestCalculateNextRunEveryRequiresPositiveDuration(t *testing.T) {
	job := &Job{
		Schedule: Schedule{
			Type:          ScheduleTypeEvery,
			EveryDuration: 0,
		},
	}

	_, err := job.CalculateNextRun(time.Now())
	if err == nil {
		t.Fatalf("expected error for zero every duration")
	}
}

func TestCalculateNextRunCronRequiresExpression(t *testing.T) {
	job := &Job{
		Schedule: Schedule{
			Type:           ScheduleTypeCron,
			CronExpression: "",
		},
	}

	_, err := job.CalculateNextRun(time.Now())
	if err == nil {
		t.Fatalf("expected error for empty cron expression")
	}
}

func TestCalculateNextRunAtUsesScheduledTimeBeforeTrigger(t *testing.T) {
	from := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	at := from.Add(2 * time.Hour)
	job := &Job{
		Schedule: Schedule{
			Type: ScheduleTypeAt,
			At:   at,
		},
	}

	next, err := job.CalculateNextRun(from)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !next.Equal(at) {
		t.Fatalf("expected next run %s, got %s", at.Format(time.RFC3339), next.Format(time.RFC3339))
	}
}

func TestCalculateNextRunAtReturnsZeroAfterTriggerTime(t *testing.T) {
	at := time.Date(2026, 3, 13, 10, 0, 0, 0, time.UTC)
	job := &Job{
		Schedule: Schedule{
			Type: ScheduleTypeAt,
			At:   at,
		},
	}

	next, err := job.CalculateNextRun(at.Add(time.Minute))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !next.IsZero() {
		t.Fatalf("expected zero next run after trigger time, got %s", next.Format(time.RFC3339))
	}
}
