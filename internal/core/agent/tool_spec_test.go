package agent

import (
	"context"
	"testing"

	agenttools "github.com/smallnest/goclaw/internal/core/agent/tools"
	"github.com/smallnest/goclaw/internal/core/agent/tooltypes"
)

type legacySpeclessTool struct{}

func (legacySpeclessTool) Name() string               { return "legacy_tool" }
func (legacySpeclessTool) Description() string        { return "legacy tool" }
func (legacySpeclessTool) Label() string              { return "legacy_tool" }
func (legacySpeclessTool) Parameters() map[string]any { return map[string]any{} }
func (legacySpeclessTool) Execute(ctx context.Context, params map[string]any, onUpdate func(ToolResult)) (ToolResult, error) {
	return ToolResult{Content: []ContentBlock{TextContent{Text: "ok"}}}, nil
}

func TestResolveToolSpec_PreservesExplicitSpecFromAdaptedBaseTool(t *testing.T) {
	tool := agenttools.NewUseSkillTool()
	adapted := ToAgentTools([]agenttools.Tool{tool})
	if len(adapted) != 1 {
		t.Fatalf("expected 1 adapted tool, got %d", len(adapted))
	}

	spec := ResolveToolSpec(adapted[0])
	if spec.Name != "use_skill" {
		t.Fatalf("unexpected spec name: %q", spec.Name)
	}
	if spec.Concurrency != tooltypes.ConcurrencyConcurrent {
		t.Fatalf("unexpected concurrency: %q", spec.Concurrency)
	}
	if spec.Mutation != tooltypes.MutationRead {
		t.Fatalf("unexpected mutation: %q", spec.Mutation)
	}
	if spec.Risk != tooltypes.RiskLow {
		t.Fatalf("unexpected risk: %q", spec.Risk)
	}
}

func TestResolveToolSpec_ProvidesConservativeFallbackForLegacyTools(t *testing.T) {
	spec := ResolveToolSpec(legacySpeclessTool{})

	if spec.Name != "legacy_tool" {
		t.Fatalf("unexpected spec name: %q", spec.Name)
	}
	if spec.Concurrency != tooltypes.ConcurrencyExclusive {
		t.Fatalf("unexpected fallback concurrency: %q", spec.Concurrency)
	}
	if spec.Mutation != tooltypes.MutationSideEffect {
		t.Fatalf("unexpected fallback mutation: %q", spec.Mutation)
	}
	if spec.Risk != tooltypes.RiskMedium {
		t.Fatalf("unexpected fallback risk: %q", spec.Risk)
	}
}
