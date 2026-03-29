package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspacepkg "github.com/smallnest/goclaw/internal/workspace"
)

func readWorkspaceTemplate(t *testing.T, filename string) string {
	t.Helper()
	content, err := workspacepkg.ReadEmbeddedTemplate(filename)
	if err != nil {
		t.Fatalf("read template %s: %v", filename, err)
	}
	return content
}

func TestAssemblePrompt_CognitionTemplateIsTreatedAsInvalid_IncludesBootstrapWhenAllTemplate(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	ownerDir := t.TempDir()
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "owner1" {
			return ownerDir
		}
		return workspace
	})

	// Use template contents as cognition files (should be treated as invalid).
	templates := map[string]string{}
	for _, name := range []string{"SOUL.md", "IDENTITY.md", "AGENTS.md", "USER.md"} {
		content := readWorkspaceTemplate(t, name)
		templates[name] = strings.TrimSpace(content)
		if err := os.WriteFile(filepath.Join(ownerDir, name), []byte(templates[name]), 0644); err != nil {
			t.Fatalf("write owner %s: %v", name, err)
		}
	}

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:             PromptAssemblyModeMain,
		BootstrapOwnerID: "owner1",
		AgentCorePrompt:  "custom core",
	}).SystemPrompt

	// Template cognition should not be included.
	for name, content := range templates {
		if strings.Contains(got, content) {
			t.Fatalf("did not expect prompt to include template cognition %s content, got %q", name, got)
		}
	}

	// Since no effective cognition, should include bootstrap sections and content.
	for _, marker := range []string{"## Bootstrap Mode", "## Bootstrap Guide", "BOOTSTRAP.md - Hello, World"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected %q in prompt, got %q", marker, got)
		}
	}
	for _, marker := range []string{
		"overrides agent-core role wording for first-run onboarding, greetings, and identity questions",
		`Do not reply with generic introductions like "I am an AI assistant", "我是一个 AI 助手"`,
	} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected stronger bootstrap rule %q in prompt, got %q", marker, got)
		}
	}
}

func TestAssemblePrompt_PartialEffectiveCognitionStillIncludesBootstrapGuide(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	ownerDir := t.TempDir()
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "owner1" {
			return ownerDir
		}
		return workspace
	})

	// One effective cognition file should still be injected.
	if err := os.WriteFile(filepath.Join(ownerDir, "SOUL.md"), []byte("not template soul"), 0644); err != nil {
		t.Fatalf("write owner SOUL.md: %v", err)
	}
	// Others remain template, so bootstrap guidance is still required.
	for _, name := range []string{"IDENTITY.md", "AGENTS.md", "USER.md"} {
		content := readWorkspaceTemplate(t, name)
		if err := os.WriteFile(filepath.Join(ownerDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write owner %s: %v", name, err)
		}
	}

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:             PromptAssemblyModeMain,
		BootstrapOwnerID: "owner1",
		AgentCorePrompt:  "custom core",
	}).SystemPrompt

	// Effective cognition included.
	if !strings.Contains(got, "not template soul") {
		t.Fatalf("expected effective soul to be included, got %q", got)
	}
	for _, name := range []string{"IDENTITY.md", "AGENTS.md", "USER.md"} {
		if strings.Contains(got, strings.TrimSpace(readWorkspaceTemplate(t, name))) {
			t.Fatalf("did not expect template cognition %s to be injected, got %q", name, got)
		}
	}
	for _, marker := range []string{"## Bootstrap Mode", "## Bootstrap Guide", "BOOTSTRAP.md - Hello, World"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected %q in prompt when cognition is only partially initialized, got %q", marker, got)
		}
	}
}

func TestAssemblePrompt_BootstrapLayersAppearBeforeAgentCore(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

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
		AgentCorePrompt:  "custom core for vibecoding",
	}).SystemPrompt

	bootstrapIdx := strings.Index(got, "## Bootstrap Mode")
	guideIdx := strings.Index(got, "## Bootstrap Guide")
	coreIdx := strings.Index(got, "custom core for vibecoding")
	if bootstrapIdx == -1 || guideIdx == -1 || coreIdx == -1 {
		t.Fatalf("expected bootstrap mode, bootstrap guide, and custom core in prompt, got %q", got)
	}
	if !(bootstrapIdx < guideIdx && guideIdx < coreIdx) {
		t.Fatalf("expected bootstrap layers to appear before custom core, got %q", got)
	}
}

func TestAssemblePrompt_AllEffectiveCognitionSkipsBootstrap(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	ownerDir := t.TempDir()
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "owner1" {
			return ownerDir
		}
		return workspace
	})

	files := map[string]string{
		"SOUL.md":     "custom soul",
		"IDENTITY.md": "custom identity",
		"AGENTS.md":   "custom agents",
		"USER.md":     "custom user",
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(ownerDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write owner %s: %v", name, err)
		}
	}

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode:             PromptAssemblyModeMain,
		BootstrapOwnerID: "owner1",
		AgentCorePrompt:  "custom core",
	}).SystemPrompt

	for _, content := range files {
		if !strings.Contains(got, content) {
			t.Fatalf("expected effective cognition %q to be injected, got %q", content, got)
		}
	}
	for _, marker := range []string{"## Bootstrap Mode", "## Bootstrap Guide", "BOOTSTRAP.md - Hello, World"} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q once cognition is fully initialized, got %q", marker, got)
		}
	}
}
