package plan

import "strings"

type Status string

const (
	StatusDraft     Status = "draft"
	StatusActive    Status = "active"
	StatusCompleted Status = "completed"
	StatusBlocked   Status = "blocked"
	StatusCanceled  Status = "canceled"
)

type StepStatus string

const (
	StepPending StepStatus = "pending"
	StepReady   StepStatus = "ready"
	StepRunning StepStatus = "running"
	StepDone    StepStatus = "completed"
	StepBlocked StepStatus = "blocked"
	StepFailed  StepStatus = "failed"
	StepSkipped StepStatus = "skipped"
)

func (s StepStatus) IsTerminal() bool {
	switch s {
	case StepDone, StepBlocked, StepFailed, StepSkipped:
		return true
	default:
		return false
	}
}

type StepKind string

const (
	StepKindTask           StepKind = "task"
	StepKindResearch       StepKind = "research"
	StepKindSynthesis      StepKind = "synthesis"
	StepKindDesign         StepKind = "design"
	StepKindImplementation StepKind = "implementation"
	StepKindVerification   StepKind = "verification"
	StepKindReview         StepKind = "review"
	StepKindSummary        StepKind = "summary"
)

type Step struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Kind          StepKind   `json:"kind"`
	Goal          string     `json:"goal"`
	AgentHint     string     `json:"agent_hint,omitempty"`
	Strategy      string     `json:"strategy,omitempty"`
	RelevantFiles []string   `json:"relevant_files,omitempty"`
	Constraints   []string   `json:"constraints,omitempty"`
	Deliverables  []string   `json:"deliverables,omitempty"`
	DoneWhen      []string   `json:"done_when,omitempty"`
	DependsOn     []string   `json:"depends_on,omitempty"`
	Status        StepStatus `json:"status"`
	TaskID        string     `json:"task_id,omitempty"`
	Notes         string     `json:"notes,omitempty"`
}

type Record struct {
	ID            string `json:"id"`
	SessionKey    string `json:"session_key"`
	AgentID       string `json:"agent_id"`
	Goal          string `json:"goal"`
	Status        Status `json:"status"`
	Steps         []Step `json:"steps"`
	CurrentStepID string `json:"current_step_id,omitempty"`
	LastDecision  string `json:"last_decision,omitempty"`
	CreatedAt     int64  `json:"created_at"`
	UpdatedAt     int64  `json:"updated_at"`
}

func cloneRecord(record *Record) *Record {
	if record == nil {
		return nil
	}

	cloned := *record
	if len(record.Steps) > 0 {
		cloned.Steps = make([]Step, len(record.Steps))
		for i, step := range record.Steps {
			cloned.Steps[i] = cloneStep(step)
		}
	}

	return &cloned
}

func cloneStep(step Step) Step {
	cloned := step
	cloned.RelevantFiles = append([]string(nil), step.RelevantFiles...)
	cloned.Constraints = append([]string(nil), step.Constraints...)
	cloned.Deliverables = append([]string(nil), step.Deliverables...)
	cloned.DoneWhen = append([]string(nil), step.DoneWhen...)
	cloned.DependsOn = append([]string(nil), step.DependsOn...)
	cloned.ID = strings.TrimSpace(cloned.ID)
	cloned.Title = strings.TrimSpace(cloned.Title)
	cloned.Goal = strings.TrimSpace(cloned.Goal)
	cloned.AgentHint = strings.TrimSpace(cloned.AgentHint)
	cloned.Strategy = strings.TrimSpace(cloned.Strategy)
	cloned.TaskID = strings.TrimSpace(cloned.TaskID)
	cloned.Notes = strings.TrimSpace(cloned.Notes)
	return cloned
}
