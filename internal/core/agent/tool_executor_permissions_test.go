package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/smallnest/goclaw/internal/core/config"
	"github.com/smallnest/goclaw/internal/core/permissions"
)

type shouldNotRunTool struct {
	name     string
	executed bool
}

func (s *shouldNotRunTool) Name() string               { return s.name }
func (s *shouldNotRunTool) Description() string        { return "should not run" }
func (s *shouldNotRunTool) Parameters() map[string]any { return map[string]any{} }
func (s *shouldNotRunTool) Label() string              { return s.name }
func (s *shouldNotRunTool) Execute(ctx context.Context, params map[string]any, onUpdate func(ToolResult)) (ToolResult, error) {
	s.executed = true
	return ToolResult{Content: []ContentBlock{TextContent{Text: "unexpected"}}}, nil
}

func TestExecuteToolCalls_DeniesShellCommandBeforeExecution(t *testing.T) {
	state := NewAgentState()
	tool := &shouldNotRunTool{name: "run_shell"}
	state.Tools = []Tool{tool}

	policy := permissions.CompilePolicy(&config.Config{
		Tools: config.ToolsConfig{
			Shell: config.ShellToolConfig{
				DeniedCmds: []string{"rm -rf"},
			},
		},
	})

	o := NewOrchestrator(&LoopConfig{
		PermissionPolicy: policy,
	}, state)

	results, steering := o.executeToolCalls(context.Background(), []ToolCallContent{
		{ID: "call-1", Name: "run_shell", Arguments: map[string]any{"command": "rm -rf /tmp/demo"}},
	}, state)

	if len(steering) != 0 {
		t.Fatalf("expected no steering messages, got %d", len(steering))
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if tool.executed {
		t.Fatalf("expected denied tool not to execute")
	}

	got := extractTextContent(results[0])
	if !strings.Contains(got, "Permission Denied") {
		t.Fatalf("expected permission denied message, got %q", got)
	}
}
