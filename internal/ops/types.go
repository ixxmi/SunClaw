package ops

import "time"

// Action represents operation type.
type Action string

const (
	ActionUnknown  Action = "unknown"
	ActionHelp     Action = "help"
	ActionStatus   Action = "status"
	ActionRestart  Action = "restart"
	ActionUpgrade  Action = "upgrade"
	ActionRollback Action = "rollback"
)

// Request is the structured input of an ops command.
type Request struct {
	Command    string
	Action     Action
	RequestID  string
	Confirm    bool
	Role       string
	Branch     string
	RollbackTo string
}

// Response is the structured output for an ops command.
type Response struct {
	Status  string
	Action  Action
	Message string
	Steps   []StepResult

	RequestID string
	Version   string
	Health    *HealthStatus
}

// StepResult captures one execution step.
type StepResult struct {
	Name       string
	Success    bool
	Duration   time.Duration
	Output     string
	ErrMessage string
}

// HealthStatus is unified health probe result.
type HealthStatus struct {
	OK      bool
	Detail  string
	Checked time.Time
}
