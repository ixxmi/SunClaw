package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/smallnest/goclaw/internal/core/execution"
	"github.com/smallnest/goclaw/internal/core/plan"
)

type PlanUpdateTool struct {
	manager *plan.Manager
}

func NewPlanUpdateTool(manager *plan.Manager) *PlanUpdateTool {
	return &PlanUpdateTool{manager: manager}
}

func (t *PlanUpdateTool) Name() string {
	return "plan_update"
}

func (t *PlanUpdateTool) Description() string {
	return "Create or replace the active execution plan for the current session, including structured steps and the current step."
}

func (t *PlanUpdateTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"plan_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional existing plan ID to replace. Omit to create a new plan or replace the current active plan.",
			},
			"goal": map[string]interface{}{
				"type":        "string",
				"description": "Overall user goal that this plan is trying to complete.",
			},
			"current_step_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional step ID that should be treated as the current step.",
			},
			"steps": map[string]interface{}{
				"type":        "array",
				"description": "Ordered plan steps. Keep the list minimal and actionable.",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"id":             map[string]interface{}{"type": "string"},
						"title":          map[string]interface{}{"type": "string"},
						"kind":           map[string]interface{}{"type": "string"},
						"goal":           map[string]interface{}{"type": "string"},
						"agent_hint":     map[string]interface{}{"type": "string"},
						"strategy":       map[string]interface{}{"type": "string"},
						"relevant_files": map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						"constraints":    map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						"deliverables":   map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						"done_when":      map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
						"depends_on":     map[string]interface{}{"type": "array", "items": map[string]interface{}{"type": "string"}},
					},
					"required": []string{"title", "goal"},
				},
			},
		},
		"required": []string{"goal", "steps"},
	}
}

func (t *PlanUpdateTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.manager == nil {
		return "", fmt.Errorf("plan manager is unavailable")
	}

	goal, _ := params["goal"].(string)
	goal = strings.TrimSpace(goal)
	if goal == "" {
		return "", fmt.Errorf("goal is required")
	}

	rawSteps, ok := params["steps"].([]interface{})
	if !ok || len(rawSteps) == 0 {
		return "", fmt.Errorf("steps is required")
	}

	sessionKey := strings.TrimSpace(execution.SessionKey(ctx))
	if sessionKey == "" {
		sessionKey = "main"
	}
	agentID := strings.TrimSpace(execution.AgentID(ctx))
	if agentID == "" {
		agentID = "default"
	}

	planID, _ := params["plan_id"].(string)
	planID = strings.TrimSpace(planID)
	if planID == "" {
		if active, ok := t.manager.GetActiveBySession(sessionKey); ok && active != nil {
			planID = active.ID
		} else {
			planID = uuid.NewString()
		}
	}

	currentStepID, _ := params["current_step_id"].(string)
	steps := make([]plan.Step, 0, len(rawSteps))
	for i, raw := range rawSteps {
		stepMap, ok := raw.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("steps[%d] must be an object", i)
		}
		title, _ := stepMap["title"].(string)
		goal, _ := stepMap["goal"].(string)
		if strings.TrimSpace(title) == "" || strings.TrimSpace(goal) == "" {
			return "", fmt.Errorf("steps[%d] requires title and goal", i)
		}

		step := plan.Step{
			ID:            strings.TrimSpace(stringValue(stepMap["id"])),
			Title:         strings.TrimSpace(title),
			Kind:          plan.StepKind(strings.TrimSpace(stringValue(stepMap["kind"]))),
			Goal:          strings.TrimSpace(goal),
			AgentHint:     strings.TrimSpace(stringValue(stepMap["agent_hint"])),
			Strategy:      strings.TrimSpace(stringValue(stepMap["strategy"])),
			RelevantFiles: stringSliceValue(stepMap["relevant_files"]),
			Constraints:   stringSliceValue(stepMap["constraints"]),
			Deliverables:  stringSliceValue(stepMap["deliverables"]),
			DoneWhen:      stringSliceValue(stepMap["done_when"]),
			DependsOn:     stringSliceValue(stepMap["depends_on"]),
		}
		steps = append(steps, step)
	}

	record, err := t.manager.UpsertActive(&plan.Record{
		ID:            planID,
		SessionKey:    sessionKey,
		AgentID:       agentID,
		Goal:          goal,
		Status:        plan.StatusActive,
		Steps:         steps,
		CurrentStepID: strings.TrimSpace(currentStepID),
		LastDecision:  "plan_update",
	})
	if err != nil {
		return "", err
	}

	payload := map[string]interface{}{
		"status":          "saved",
		"plan_id":         record.ID,
		"goal":            record.Goal,
		"step_count":      len(record.Steps),
		"current_step_id": record.CurrentStepID,
	}
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

type PlanGetTool struct {
	manager *plan.Manager
}

func NewPlanGetTool(manager *plan.Manager) *PlanGetTool {
	return &PlanGetTool{manager: manager}
}

func (t *PlanGetTool) Name() string {
	return "plan_get"
}

func (t *PlanGetTool) Description() string {
	return "Get the current active execution plan for this session, or a specific plan by ID."
}

func (t *PlanGetTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"plan_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional explicit plan ID. Defaults to the current session's active plan.",
			},
		},
	}
}

func (t *PlanGetTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
	if t.manager == nil {
		return "", fmt.Errorf("plan manager is unavailable")
	}

	planID, _ := params["plan_id"].(string)
	planID = strings.TrimSpace(planID)

	var record *plan.Record
	var ok bool
	if planID != "" {
		record, ok = t.manager.Get(planID)
	} else {
		sessionKey := strings.TrimSpace(execution.SessionKey(ctx))
		if sessionKey == "" {
			sessionKey = "main"
		}
		record, ok = t.manager.GetActiveBySession(sessionKey)
	}
	if !ok || record == nil {
		return `{"status":"not_found"}`, nil
	}

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func stringValue(value interface{}) string {
	switch v := value.(type) {
	case string:
		return v
	default:
		return ""
	}
}

func stringSliceValue(value interface{}) []string {
	raw, ok := value.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		if str, ok := item.(string); ok && strings.TrimSpace(str) != "" {
			out = append(out, strings.TrimSpace(str))
		}
	}
	return out
}
