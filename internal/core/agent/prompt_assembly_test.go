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

func TestAssemblePrompt_DoesNotIncludeSilentRepliesOrHeartbeats(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode: PromptAssemblyModeMain,
	}).SystemPrompt

	for _, marker := range []string{"## Silent Replies", "## Heartbeats", "SILENT_REPLY", "HEARTBEAT_OK"} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q in prompt, got %q", marker, got)
		}
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

func TestAssemblePrompt_SubagentUsesDescriptorWithoutCatalogWhenNotProvided(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	if err := os.WriteFile(filepath.Join(workspace, "BOOTSTRAP.md"), []byte("# Bootstrap\n\nmain bootstrap guide"), 0644); err != nil {
		t.Fatalf("write BOOTSTRAP.md: %v", err)
	}

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:               PromptAssemblyModeSubagent,
		AgentCorePrompt:    "target agent core",
		BootstrapOwnerID:   "vibecoding",
		SubagentDescriptor: "# Subagent Context\n\nYou are a subagent for this step.",
		Skills: []*Skill{
			{
				Name:        "weather",
				Description: "Use for weather tasks.",
				Content:     "# Weather\n\nDetailed instructions.",
			},
		},
	}).SystemPrompt

	if !strings.Contains(got, "# Subagent Context") {
		t.Fatalf("expected subagent descriptor in prompt, got %q", got)
	}
	for _, marker := range []string{
		"## 可派生 Agent 目录",
		"## Skills (mandatory)",
		"## Selected Skills (active)",
		"## Bootstrap Mode",
		"## Bootstrap Guide",
		"main bootstrap guide",
	} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q in subagent prompt, got %q", marker, got)
		}
	}
}

func TestAssemblePrompt_SubagentIncludesSpawnableCatalogWhenProvided(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:                  PromptAssemblyModeSubagent,
		AgentCorePrompt:       "target agent core",
		BootstrapOwnerID:      "vibecoding",
		SpawnableAgentCatalog: "## 可派生 Agent 目录\n\n- **agent_id: \"coder\"** — Coder — 单步实现",
		SubagentDescriptor:    "# Subagent Context\n\nYou are a subagent for this step.",
	}).SystemPrompt

	if !strings.Contains(got, "# Subagent Context") {
		t.Fatalf("expected subagent descriptor in prompt, got %q", got)
	}
	if !strings.Contains(got, "## 可派生 Agent 目录") {
		t.Fatalf("expected subagent prompt to include spawnable catalog, got %q", got)
	}
}

func TestAssemblePrompt_SubagentSkipsLoadedSkillContent(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:               PromptAssemblyModeSubagent,
		AgentCorePrompt:    "target agent core",
		SubagentDescriptor: "# Subagent Context\n\nYou are a subagent for this step.",
		LoadedSkills:       []string{"weather"},
		Skills: []*Skill{
			{
				Name:        "weather",
				Description: "Use for weather tasks.",
				Content:     "# Weather\n\nDetailed instructions.",
			},
		},
	}).SystemPrompt

	for _, marker := range []string{"## Selected Skills (active)", "# Weather", "Detailed instructions."} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q in subagent prompt, got %q", marker, got)
		}
	}
}

// New tests for workspace isolation: remove root BOOTSTRAP fallback
func TestAssemblePrompt_Isolation_NoFallbackToRootBootstrapWhenOwnerEmpty(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	// root workspace has BOOTSTRAP.md
	if err := os.WriteFile(filepath.Join(workspace, "BOOTSTRAP.md"), []byte("root bootstrap content"), 0644); err != nil {
		t.Fatalf("write root BOOTSTRAP.md: %v", err)
	}

	ownerDir := t.TempDir()
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "owner1" {
			return ownerDir
		}
		return workspace
	})

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:             PromptAssemblyModeMain,
		BootstrapOwnerID: "owner1",
	}).SystemPrompt

	// Should not include root bootstrap content or bootstrap sections
	if strings.Contains(got, "root bootstrap content") {
		t.Fatalf("expected prompt NOT to include root bootstrap content, got %q", got)
	}
	for _, marker := range []string{"## Bootstrap Mode", "## Bootstrap Guide"} {
		if strings.Contains(got, marker) {
			t.Fatalf("expected no bootstrap sections when owner has no bootstrap and no cognition, got %q", got)
		}
	}
}

func TestAssemblePrompt_Isolation_OwnerBootstrapPreferredOverRoot(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	// root workspace has BOOTSTRAP.md
	if err := os.WriteFile(filepath.Join(workspace, "BOOTSTRAP.md"), []byte("root bootstrap content"), 0644); err != nil {
		t.Fatalf("write root BOOTSTRAP.md: %v", err)
	}

	// owner workspace has its own BOOTSTRAP.md
	ownerDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(ownerDir, "BOOTSTRAP.md"), []byte("owner bootstrap content"), 0644); err != nil {
		t.Fatalf("write owner BOOTSTRAP.md: %v", err)
	}
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "owner1" {
			return ownerDir
		}
		return workspace
	})

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:             PromptAssemblyModeMain,
		BootstrapOwnerID: "owner1",
	}).SystemPrompt

	// Should include owner's bootstrap, not root's
	if !strings.Contains(got, "owner bootstrap content") {
		t.Fatalf("expected prompt to include owner bootstrap content, got %q", got)
	}
	if strings.Contains(got, "root bootstrap content") {
		t.Fatalf("did not expect prompt to include root bootstrap content when owner has BOOTSTRAP, got %q", got)
	}
	for _, marker := range []string{"## Bootstrap Mode", "## Bootstrap Guide"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected bootstrap sections when owner has bootstrap, missing %q in %q", marker, got)
		}
	}
}

// New tests for cognitive isolation: SOUL/IDENTITY/AGENTS/USER should never fall back to root when owner workspace is set
func TestAssemblePrompt_Isolation_NoFallbackToRootCognitionWhenOwnerEmpty(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	// root has cognitive files
	rootFiles := map[string]string{
		"SOUL.md":     "root soul content",
		"IDENTITY.md": "root identity content",
		"AGENTS.md":   "root agents content",
		"USER.md":     "root user content",
	}
	for name, content := range rootFiles {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0644); err != nil {
			t.Fatalf("write root %s: %v", name, err)
		}
	}

	ownerDir := t.TempDir()
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "owner1" {
			return ownerDir
		}
		return workspace
	})

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:             PromptAssemblyModeMain,
		BootstrapOwnerID: "owner1",
		AgentCorePrompt:  "custom core",
	}).SystemPrompt

	// None of the root cognitive contents should appear
	for _, notWant := range rootFiles {
		if strings.Contains(got, notWant) {
			t.Fatalf("expected prompt NOT to include root cognition content %q", notWant)
		}
	}
}

func TestAssemblePrompt_Isolation_OwnerCognitionPreferredOverRoot(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	// root has cognitive files
	rootFiles := map[string]string{
		"SOUL.md":     "root soul content",
		"IDENTITY.md": "root identity content",
		"AGENTS.md":   "root agents content",
		"USER.md":     "root user content",
	}
	for name, content := range rootFiles {
		if err := os.WriteFile(filepath.Join(workspace, name), []byte(content), 0644); err != nil {
			t.Fatalf("write root %s: %v", name, err)
		}
	}

	// owner has its own cognitive files
	ownerDir := t.TempDir()
	ownerFiles := map[string]string{
		"SOUL.md":     "owner soul content",
		"IDENTITY.md": "owner identity content",
		"AGENTS.md":   "owner agents content",
		"USER.md":     "owner user content",
	}
	for name, content := range ownerFiles {
		if err := os.WriteFile(filepath.Join(ownerDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write owner %s: %v", name, err)
		}
	}

	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "owner1" {
			return ownerDir
		}
		return workspace
	})

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:             PromptAssemblyModeMain,
		BootstrapOwnerID: "owner1",
	}).SystemPrompt

	// Owner contents should appear; root contents should not
	for _, want := range ownerFiles {
		if !strings.Contains(got, want) {
			t.Fatalf("expected prompt to include owner cognition content %q, got %q", want, got)
		}
	}
	for _, notWant := range rootFiles {
		if strings.Contains(got, notWant) {
			t.Fatalf("did not expect root cognition content %q in prompt when owner exists", notWant)
		}
	}
}
