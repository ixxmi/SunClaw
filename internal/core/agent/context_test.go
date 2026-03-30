package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspacepkg "github.com/smallnest/goclaw/internal/workspace"
)

func TestBuildSystemPromptKeepsBoundaryWithoutBuiltinGenericCoreFallback(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	prompt := builder.BuildSystemPrompt(nil)

	if !strings.Contains(prompt, "## Builtin Boundary") {
		t.Fatalf("expected builtin boundary in prompt, got %q", prompt)
	}
	for _, marker := range []string{"## Builtin Generic Core", "## Communication Style", "## Error Handling"} {
		if strings.Contains(prompt, marker) {
			t.Fatalf("did not expect builtin generic core marker %q in prompt, got %q", marker, prompt)
		}
	}
}

func TestBuildSystemPromptIncludesCommonBoundaryAndAgentsAuthority(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	prompt := builder.BuildSystemPrompt(nil)

	checks := []string{
		"This layer defines only common, non-overridable boundaries.",
		"When a first-class tool exists for an action, you MUST use the tool instead of claiming results from memory.",
		"Do not describe planned, partial, attempted, or inferred work as completed work.",
		"`AGENTS.md` is the authoritative decision layer unless it conflicts with system safety or tool policy.",
		"follow `AGENTS.md` rather than inventing a competing process in the builtin boundary layer.",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing common boundary rule %q", want)
		}
	}
}

func TestBuildSystemPromptListsProgressMessagingTools(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	prompt := builder.BuildSystemPrompt(nil)

	checks := []string{
		"- send_message:",
		"- send_file:",
		"- sessions_spawn:",
		"- memory_search:",
		"- memory_add:",
		"- sandbox_execute:",
	}

	for _, want := range checks {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing tool summary %q", want)
		}
	}
}

func TestBuildSkillsContextUsesSummaryBeforeSelection(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)
	skills := []*Skill{
		{
			Name:        "weather",
			Description: "Use when the user asks about weather or forecast.",
			Content:     "# Weather\n\nDetailed instructions.",
		},
	}

	got := builder.buildSkillsContext(skills, nil, PromptModeFull)

	checks := []string{
		"## Skills (mandatory)",
		"<skill name=\"weather\">",
		"Use when the user asks about weather or forecast.",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("skills context missing %q", want)
		}
	}
	if strings.Contains(got, "## Selected Skills (active)") {
		t.Fatalf("summary phase should not include selected skill section")
	}
}

func TestBuildSkillsContextUsesSelectedSkillContentAfterSelection(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)
	skills := []*Skill{
		{
			Name:        "weather",
			Description: "Use when the user asks about weather or forecast.",
			Content:     "# Weather\n\nDetailed instructions.",
		},
	}

	got := builder.buildSkillsContext(skills, []string{"weather"}, PromptModeFull)

	checks := []string{
		"## Selected Skills (active)",
		"<skill name=\"weather\">",
		"# Weather",
		"Detailed instructions.",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Fatalf("selected skills context missing %q", want)
		}
	}
	if strings.Contains(got, "## Skills (mandatory)") {
		t.Fatalf("selected phase should not include summary section")
	}
}

func TestLoadBootstrapFilesUsesDedicatedBootstrapStore(t *testing.T) {
	workspaceDir := t.TempDir()
	bootstrapDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(bootstrapDir, "IDENTITY.md"), []byte("# Identity\n\nbootstrap identity"), 0644); err != nil {
		t.Fatalf("write identity: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bootstrapDir, "USER.md"), []byte("# User\n\nbootstrap user"), 0644); err != nil {
		t.Fatalf("write user: %v", err)
	}

	builder := NewContextBuilderWithBootstrap(
		NewMemoryStore(workspaceDir),
		workspaceDir,
		NewMemoryStore(bootstrapDir),
	)

	got := builder.buildBootstrapSection()
	if !strings.Contains(got, "bootstrap identity") {
		t.Fatalf("expected bootstrap identity in section, got %q", got)
	}
	if !strings.Contains(got, "bootstrap user") {
		t.Fatalf("expected bootstrap user in section, got %q", got)
	}
}

func TestBuildBootstrapSectionUsesOwnerResolver(t *testing.T) {
	workspaceDir := t.TempDir()
	ownerDir := t.TempDir()

	if err := os.WriteFile(filepath.Join(workspaceDir, "SOUL.md"), []byte("root soul"), 0644); err != nil {
		t.Fatalf("write root SOUL.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(ownerDir, "SOUL.md"), []byte("owner soul"), 0644); err != nil {
		t.Fatalf("write owner SOUL.md: %v", err)
	}

	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "vibecoding" {
			return ownerDir
		}
		return workspaceDir
	})

	got := builder.buildBootstrapSectionForOwner("vibecoding")
	if !strings.Contains(got, "owner soul") {
		t.Fatalf("expected owner bootstrap content, got %q", got)
	}
	if strings.Contains(got, "root soul") {
		t.Fatalf("expected owner bootstrap to override root content, got %q", got)
	}
}

func TestBuildBootstrapSectionFallsBackToBootstrapGuideWhenNoCognitiveFiles(t *testing.T) {
	workspaceDir := t.TempDir()
	ownerDir := filepath.Join(t.TempDir(), "agents", "new-agent", "bootstrap")

	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "new-agent" {
			return ownerDir
		}
		return workspaceDir
	})

	got := builder.buildBootstrapSectionForOwner("new-agent")
	if !strings.Contains(got, "BOOTSTRAP.md") {
		t.Fatalf("expected BOOTSTRAP.md section, got %q", got)
	}
	bootstrapTemplate, err := workspacepkg.ReadEmbeddedTemplate("BOOTSTRAP.md")
	if err != nil {
		t.Fatalf("read BOOTSTRAP template: %v", err)
	}
	if !strings.Contains(got, strings.TrimSpace(bootstrapTemplate)) {
		t.Fatalf("expected bootstrap guide content, got %q", got)
	}
}

func TestBuildMessagesWithRuntimeUsesRuntimeToolSummary(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	msgs := builder.BuildMessagesWithRuntime(nil, "", "hello", nil, nil, []Tool{
		&summaryOnlyTool{name: "read_file"},
	}, "", PromptModeFull)

	if len(msgs) == 0 {
		t.Fatalf("expected messages to be built")
	}
	systemPrompt := msgs[0].Content
	if !strings.Contains(systemPrompt, "<available_tools>") {
		t.Fatalf("expected runtime tool layer in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "**read_file**") {
		t.Fatalf("expected runtime tool summary to include read_file, got %q", systemPrompt)
	}
	if strings.Contains(systemPrompt, "Tool availability (legacy summary)") {
		t.Fatalf("did not expect legacy tool summary when runtime tools are provided, got %q", systemPrompt)
	}
}

func TestBuildMessagesWithRuntimeIncludesSessionSummary(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	msgs := builder.BuildMessagesWithRuntime(nil, "Earlier work summary", "hello", nil, nil, nil, "", PromptModeFull)
	if len(msgs) == 0 {
		t.Fatalf("expected messages to be built")
	}

	systemPrompt := msgs[0].Content
	if !strings.Contains(systemPrompt, "## Context Summary") {
		t.Fatalf("expected context summary layer in system prompt, got %q", systemPrompt)
	}
	if !strings.Contains(systemPrompt, "Earlier work summary") {
		t.Fatalf("expected session summary content in system prompt, got %q", systemPrompt)
	}
}
