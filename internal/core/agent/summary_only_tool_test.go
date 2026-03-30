package agent

import "context"

type summaryOnlyTool struct {
	name string
}

func (s *summaryOnlyTool) Name() string               { return s.name }
func (s *summaryOnlyTool) Description() string        { return "" }
func (s *summaryOnlyTool) Parameters() map[string]any { return map[string]any{} }
func (s *summaryOnlyTool) Label() string              { return s.name }
func (s *summaryOnlyTool) Execute(ctx context.Context, params map[string]any, onUpdate func(ToolResult)) (ToolResult, error) {
	return ToolResult{}, nil
}
