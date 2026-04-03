package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func boolPtr(v bool) *bool {
	return &v
}

func TestAssemblePrompt_MainUsesRequestedLayerOrder(t *testing.T) {
	workspaceDir := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)

	ownerDir := t.TempDir()
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "owner1" {
			return ownerDir
		}
		return workspaceDir
	})

	files := map[string]string{
		"IDENTITY.md": "# Identity\n\ncustom identity",
		"AGENTS.md":   "# Rules\n\ncustom rules",
		"SOUL.md":     "# Soul\n\ncustom soul",
		"USER.md":     "# User\n\ncustom user",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(ownerDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:                  PromptAssemblyModeMain,
		PromptMode:            PromptModeFull,
		BootstrapOwnerID:      "owner1",
		AgentCorePrompt:       "custom core",
		SpawnableAgentCatalog: "## Available Agents\n\n- Coder (`coder`): 单步实现\n",
		SessionSummary:        "Earlier work summary",
		SessionKey:            "tenant:demo:channel:telegram:account:bot:sender:user-1:chat:chat-1",
		Tools:                 []Tool{&summaryOnlyTool{name: "read_file"}},
		Skills: []*Skill{{
			Name:        "weather",
			Description: "Use when the user asks about weather or forecast.",
			Content:     "# Weather\n\nDetailed instructions.",
		}},
	}).SystemPrompt

	for _, marker := range []string{
		"### IDENTITY.md",
		"custom identity",
		"## Workspace",
		"### AGENTS.md",
		"custom rules",
		"### AGENT.md",
		"custom core",
		"### SOUL.md",
		"custom soul",
		"### USER.md",
		"custom user",
		"# Skills (mandatory)",
		"# Available Tools",
		"# Available Agents",
		"# Context Summary",
		"# Runtime Context",
		"# User Information",
		"- Channel: telegram",
		"- Sender: user-1",
	} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected %q in prompt, got %q", marker, got)
		}
	}

	if strings.Contains(got, "Builtin Boundary") || strings.Contains(got, "BOOTSTRAP.md") {
		t.Fatalf("did not expect legacy builtin/bootstrap sections, got %q", got)
	}

	identityIdx := strings.Index(got, "### IDENTITY.md")
	workspaceIdx := strings.Index(got, "## Workspace")
	rulesIdx := strings.Index(got, "### AGENTS.md")
	agentIdx := strings.Index(got, "### AGENT.md")
	soulIdx := strings.Index(got, "### SOUL.md")
	userIdx := strings.Index(got, "### USER.md")
	skillsIdx := strings.Index(got, "# Skills (mandatory)")
	toolsIdx := strings.Index(got, "# Available Tools")
	agentsIdx := strings.Index(got, "# Available Agents")
	contextIdx := strings.Index(got, "# Context Summary")
	userInfoIdx := strings.Index(got, "# User Information")

	if !(identityIdx < workspaceIdx &&
		workspaceIdx < rulesIdx &&
		rulesIdx < agentIdx &&
		agentIdx < soulIdx &&
		soulIdx < userIdx &&
		userIdx < skillsIdx &&
		skillsIdx < toolsIdx &&
		toolsIdx < agentsIdx &&
		agentsIdx < contextIdx &&
		contextIdx < userInfoIdx) {
		t.Fatalf("unexpected main prompt order: %q", got)
	}
}

func TestAssemblePrompt_MainDoesNotFallbackToBuiltinBoundary(t *testing.T) {
	workspaceDir := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:       PromptAssemblyModeMain,
		PromptMode: PromptModeFull,
	}).SystemPrompt

	for _, marker := range []string{
		"Builtin Boundary",
		"Safety & Compliance",
		"Working Norms",
		"Task Orchestration",
		"## Builtin Generic Core",
	} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect legacy builtin marker %q in prompt, got %q", marker, got)
		}
	}
}

func TestAssemblePrompt_SubagentUsesCoreDescriptorAndRuntimeContext(t *testing.T) {
	workspaceDir := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:               PromptAssemblyModeSubagent,
		PromptMode:         PromptModeMinimal,
		BootstrapOwnerID:   "owner1",
		AgentCorePrompt:    "subagent core",
		SubagentDescriptor: "# Subagent Context\n\nFocus on the delegated step.",
		SessionSummary:     "Current delegated step summary",
		SessionKey:         "tenant:demo:channel:telegram:account:bot:sender:user-1:chat:chat-1:agent:coder:subagent:run-1",
		Skills: []*Skill{{
			Name:        "weather",
			Description: "Use when the user asks about weather or forecast.",
			Content:     "# Weather\n\nDetailed instructions.",
		}},
		Tools: []Tool{&summaryOnlyTool{name: "read_file"}},
	}).SystemPrompt

	for _, marker := range []string{
		"# Core Prompt",
		"subagent core",
		"# Subagent Context",
		"# Subagent Runtime Context",
		"# Context Summary",
		"# Runtime Context",
		"# User Information",
		"- Agent: coder",
		"- Subagent: run-1",
	} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected %q in subagent prompt, got %q", marker, got)
		}
	}

	for _, marker := range []string{
		"### IDENTITY.md",
		"### SOUL.md",
		"### USER.md",
		"# Available Tools",
		"# Available Agents",
		"# Skills (mandatory)",
		"# Selected Skills (active)",
		"Builtin Boundary",
	} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q in subagent prompt, got %q", marker, got)
		}
	}

	coreIdx := strings.Index(got, "# Core Prompt")
	descriptorIdx := strings.Index(got, "# Subagent Context")
	contextIdx := strings.Index(got, "# Subagent Runtime Context")
	if !(coreIdx < descriptorIdx && descriptorIdx < contextIdx) {
		t.Fatalf("unexpected subagent prompt order: %q", got)
	}
}

func TestAssemblePrompt_SkillsAreLoadedByDefaultWhenFlagIsNilOrFalse(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)
	skills := []*Skill{
		{
			Name:        "weather",
			Description: "Use when the user asks about weather or forecast.",
			Content:     "# Weather\n\nDetailed instructions.",
		},
	}

	for _, tc := range []struct {
		name                string
		disableSkillsPrompt *bool
	}{
		{name: "nil uses default loading", disableSkillsPrompt: nil},
		{name: "false still loads skills", disableSkillsPrompt: boolPtr(false)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := builder.AssemblePrompt(&PromptAssemblyParams{
				Mode:                PromptAssemblyModeMain,
				AgentCorePrompt:     "custom core",
				Skills:              skills,
				DisableSkillsPrompt: tc.disableSkillsPrompt,
			}).SystemPrompt

			for _, marker := range []string{"# Skills (mandatory)", "<skill name=\"weather\">"} {
				if !strings.Contains(got, marker) {
					t.Fatalf("expected %q in prompt, got %q", marker, got)
				}
			}
		})
	}
}

func TestAssemblePrompt_SkillsAreSkippedOnlyWhenFlagIsExplicitlyTrue(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)
	skills := []*Skill{
		{
			Name:        "weather",
			Description: "Use when the user asks about weather or forecast.",
			Content:     "# Weather\n\nDetailed instructions.",
		},
	}

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:                PromptAssemblyModeMain,
		AgentCorePrompt:     "custom core",
		Skills:              skills,
		DisableSkillsPrompt: boolPtr(true),
	}).SystemPrompt

	for _, marker := range []string{"# Skills (mandatory)", "<skill name=\"weather\">", "# Selected Skills (active)"} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q in prompt when skills are explicitly skipped, got %q", marker, got)
		}
	}
}
