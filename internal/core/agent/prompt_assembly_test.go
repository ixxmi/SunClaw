package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type summaryOnlyTool struct {
	name string
}

func (t *summaryOnlyTool) Name() string { return t.name }

func (t *summaryOnlyTool) Description() string { return "summary only tool" }

func (t *summaryOnlyTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *summaryOnlyTool) Label() string { return t.name }

func (t *summaryOnlyTool) Execute(ctx context.Context, params map[string]any, onUpdate func(ToolResult)) (ToolResult, error) {
	return ToolResult{}, nil
}

func TestAssemblePrompt_FallsBackToBuiltinGenericCoreWhenAgentCoreEmpty(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode: PromptAssemblyModeMain,
	}).SystemPrompt

	if !strings.Contains(got, "## Builtin Boundary") {
		t.Fatalf("expected builtin boundary in prompt, got %q", got)
	}
	if !strings.Contains(got, "## Builtin Generic Core") {
		t.Fatalf("expected builtin generic core fallback in prompt, got %q", got)
	}
}

func TestAssemblePrompt_UsesCustomAgentCoreWithoutBuiltinGenericFallback(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:            PromptAssemblyModeMain,
		AgentCorePrompt: "custom agent core",
	}).SystemPrompt

	if !strings.Contains(got, "custom agent core") {
		t.Fatalf("expected custom agent core in prompt, got %q", got)
	}
	if strings.Contains(got, "## Builtin Generic Core") {
		t.Fatalf("did not expect builtin generic core fallback when custom agent core exists: %q", got)
	}
}

func TestAssemblePrompt_OrdersSoulIdentityCoreCollaborationAndContext(t *testing.T) {
	workspace := t.TempDir()
	ownerDir := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "vibecoding" {
			return ownerDir
		}
		return workspace
	})

	files := map[string]string{
		"SOUL.md":     "owner soul",
		"IDENTITY.md": "owner identity",
		"AGENTS.md":   "owner agents",
		"USER.md":     "owner user",
	}
	for filename, content := range files {
		if err := os.WriteFile(filepath.Join(ownerDir, filename), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", filename, err)
		}
	}

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:                  PromptAssemblyModeMain,
		BootstrapOwnerID:      "vibecoding",
		AgentCorePrompt:       "custom agent core",
		SpawnableAgentCatalog: "## 可派生 Agent 目录\n\n- agent_id: coder",
		Tools:                 []Tool{&summaryOnlyTool{name: "read_file"}},
	}).SystemPrompt

	sections := []string{
		"## Builtin Boundary",
		"## Soul",
		"## Identity",
		"custom agent core",
		"## Agent Collaboration",
		"## 可派生 Agent 目录",
		"## 可用工具",
		"## User Context",
	}
	lastIdx := -1
	for _, marker := range sections {
		idx := strings.Index(got, marker)
		if idx == -1 {
			t.Fatalf("expected marker %q in prompt, got %q", marker, got)
		}
		if idx <= lastIdx {
			t.Fatalf("expected marker %q after previous section in prompt %q", marker, got)
		}
		lastIdx = idx
	}
}

func TestAssemblePrompt_SubagentUsesDescriptorInsteadOfSpawnableCatalog(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:                  PromptAssemblyModeSubagent,
		AgentCorePrompt:       "target agent core",
		SpawnableAgentCatalog: "## 可派生 Agent 目录\n\n- should not appear",
		SubagentDescriptor:    "# Subagent Context\n\nYou are a subagent for this step.",
	}).SystemPrompt

	if !strings.Contains(got, "# Subagent Context") {
		t.Fatalf("expected subagent descriptor in prompt, got %q", got)
	}
	if strings.Contains(got, "## 可派生 Agent 目录") {
		t.Fatalf("did not expect spawnable catalog in subagent prompt, got %q", got)
	}
}
