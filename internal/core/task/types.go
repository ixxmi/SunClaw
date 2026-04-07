package task

import "strings"

type Backend string

const (
	BackendSubagent Backend = "subagent"
)

type Status string

const (
	StatusAccepted    Status = "accepted"
	StatusRunning     Status = "running"
	StatusDone        Status = "completed"
	StatusFailed      Status = "failed"
	StatusTimedOut    Status = "timed_out"
	StatusCanceled    Status = "canceled"
	StatusInterrupted Status = "interrupted"
)

func (s Status) IsTerminal() bool {
	switch s {
	case StatusDone, StatusFailed, StatusTimedOut, StatusCanceled, StatusInterrupted:
		return true
	default:
		return false
	}
}

func OwnerSessionKey(record *Record) string {
	if record == nil {
		return ""
	}
	if trimmed := strings.TrimSpace(record.SessionKey); trimmed != "" {
		return trimmed
	}
	if record.Subagent != nil {
		return strings.TrimSpace(record.Subagent.RequesterSessionKey)
	}
	return ""
}

func BelongsToSession(record *Record, sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" || record == nil {
		return false
	}
	return OwnerSessionKey(record) == sessionKey
}

type DeliveryContext struct {
	Channel   string `json:"channel,omitempty"`
	AccountID string `json:"account_id,omitempty"`
	To        string `json:"to,omitempty"`
	ThreadID  string `json:"thread_id,omitempty"`
}

type Result struct {
	Output string `json:"output,omitempty"`
	Error  string `json:"error,omitempty"`
}

type SubagentPayload struct {
	RequesterSessionKey string           `json:"requester_session_key,omitempty"`
	RequesterDisplayKey string           `json:"requester_display_key,omitempty"`
	RequesterOrigin     *DeliveryContext `json:"requester_origin,omitempty"`
	RequesterAgentID    string           `json:"requester_agent_id,omitempty"`
	TargetAgentID       string           `json:"target_agent_id,omitempty"`
	BootstrapOwnerID    string           `json:"bootstrap_owner_id,omitempty"`
	ChildSessionKey     string           `json:"child_session_key,omitempty"`
	Task                string           `json:"task,omitempty"`
	Label               string           `json:"label,omitempty"`
	Cleanup             string           `json:"cleanup,omitempty"`
	TimeoutSeconds      int              `json:"timeout_seconds,omitempty"`
}

type Record struct {
	ID           string           `json:"id"`
	Backend      Backend          `json:"backend"`
	Type         string           `json:"type,omitempty"`
	Status       Status           `json:"status"`
	Summary      string           `json:"summary,omitempty"`
	SessionKey   string           `json:"session_key,omitempty"`
	AgentID      string           `json:"agent_id,omitempty"`
	PlanID       string           `json:"plan_id,omitempty"`
	StepID       string           `json:"step_id,omitempty"`
	ParentTaskID string           `json:"parent_task_id,omitempty"`
	ContinueOf   string           `json:"continue_of,omitempty"`
	CanContinue  bool             `json:"can_continue"`
	CreatedAt    int64            `json:"created_at"`
	StartedAt    *int64           `json:"started_at,omitempty"`
	EndedAt      *int64           `json:"ended_at,omitempty"`
	Result       *Result          `json:"result,omitempty"`
	Subagent     *SubagentPayload `json:"subagent,omitempty"`
}

func cloneRecord(record *Record) *Record {
	if record == nil {
		return nil
	}

	cloned := *record
	if record.StartedAt != nil {
		startedAt := *record.StartedAt
		cloned.StartedAt = &startedAt
	}
	if record.EndedAt != nil {
		endedAt := *record.EndedAt
		cloned.EndedAt = &endedAt
	}
	if record.Result != nil {
		result := *record.Result
		cloned.Result = &result
	}
	if record.Subagent != nil {
		subagent := *record.Subagent
		if record.Subagent.RequesterOrigin != nil {
			origin := *record.Subagent.RequesterOrigin
			subagent.RequesterOrigin = &origin
		}
		cloned.Subagent = &subagent
	}

	return &cloned
}
