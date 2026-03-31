package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	workspacepkg "github.com/smallnest/goclaw/internal/workspace"
)

func boolPtr(v bool) *bool {
	return &v
}

func readWorkspaceTemplate(t *testing.T, filename string) string {
	t.Helper()
	content, err := workspacepkg.ReadEmbeddedTemplate(filename)
	if err != nil {
		t.Fatalf("read template %s: %v", filename, err)
	}
	return content
}

func TestAssemblePrompt_TemplateCognitionIsInjectedAndBootstrapFollowsAgents(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	ownerDir := t.TempDir()
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "owner1" {
			return ownerDir
		}
		return workspace
	})

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

	for name, content := range templates {
		expected := shiftMarkdownHeadings(content, 1)
		if !strings.Contains(got, expected) {
			t.Fatalf("expected prompt to include normalized template cognition %s content, got %q", name, got)
		}
	}

	for _, marker := range []string{"# Identity", "# Soul", "# Collaboration", "# User Context", "### BOOTSTRAP.md", "custom core"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected %q in prompt, got %q", marker, got)
		}
	}
	if strings.Contains(got, "## Bootstrap Mode") {
		t.Fatalf("did not expect legacy bootstrap notice, got %q", got)
	}

	bootstrapIdx := strings.Index(got, "### BOOTSTRAP.md")
	identityIdx := strings.Index(got, "# Identity")
	soulIdx := strings.Index(got, "# Soul")
	agentsIdx := strings.Index(got, "# Collaboration")
	userIdx := strings.Index(got, "# User Context")
	coreIdx := strings.Index(got, "custom core")
	if !(bootstrapIdx < identityIdx && identityIdx < soulIdx && soulIdx < agentsIdx && agentsIdx < userIdx && userIdx < coreIdx) {
		t.Fatalf("expected BOOTSTRAP -> IDENTITY -> SOUL -> AGENTS -> USER -> core order, got %q", got)
	}
}

func TestAssemblePrompt_CustomizedCognitionSkipsBootstrap(t *testing.T) {
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
	for _, marker := range []string{"### BOOTSTRAP.md", "## Bootstrap Mode", "BOOTSTRAP.md - Hello, World"} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q once cognition is customized, got %q", marker, got)
		}
	}

	identityIdx := strings.Index(got, "custom identity")
	soulIdx := strings.Index(got, "custom soul")
	agentsIdx := strings.Index(got, "custom agents")
	userIdx := strings.Index(got, "custom user")
	coreIdx := strings.Index(got, "custom core")
	if !(identityIdx < soulIdx && soulIdx < agentsIdx && agentsIdx < userIdx && userIdx < coreIdx) {
		t.Fatalf("expected IDENTITY -> SOUL -> AGENTS -> USER -> core order once all four files exist, got %q", got)
	}
}

func TestAssemblePrompt_EmptyAgentCoreDoesNotFallbackToBuiltinGenericCore(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)

	got := builder.AssemblePrompt(&PromptAssemblyParams{
		Mode: PromptAssemblyModeMain,
	}).SystemPrompt

	for _, marker := range []string{"## Builtin Generic Core", "## Communication Style", "## Error Handling"} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect builtin generic core marker %q in prompt, got %q", marker, got)
		}
	}
	if !strings.Contains(got, "Builtin Boundary") {
		t.Fatalf("expected builtin boundary to remain, got %q", got)
	}
}

func TestAssemblePrompt_DoesNotAppendBootstrapWhenOnlyAgentsOrSoulRemainTemplate(t *testing.T) {
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
		"SOUL.md":     strings.TrimSpace(readWorkspaceTemplate(t, "SOUL.md")),
		"IDENTITY.md": "custom identity",
		"AGENTS.md":   strings.TrimSpace(readWorkspaceTemplate(t, "AGENTS.md")),
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

	for _, marker := range []string{"custom identity", "custom user", "# Collaboration", "# Soul"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected %q to be injected, got %q", marker, got)
		}
	}
	for _, marker := range []string{"### BOOTSTRAP.md", "BOOTSTRAP.md - Hello, World"} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q when IDENTITY.md and USER.md are customized, got %q", marker, got)
		}
	}

	identityIdx := strings.Index(got, "custom identity")
	soulIdx := strings.Index(got, "# Soul")
	agentsIdx := strings.Index(got, "# Collaboration")
	userIdx := strings.Index(got, "custom user")
	coreIdx := strings.Index(got, "custom core")
	if !(identityIdx < soulIdx && soulIdx < agentsIdx && agentsIdx < userIdx && userIdx < coreIdx) {
		t.Fatalf("expected IDENTITY -> SOUL -> AGENTS -> USER -> core order when all four files exist, got %q", got)
	}
}

func TestAssemblePrompt_MainOrdersCapabilitiesBeforeAgentsFile(t *testing.T) {
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
		Mode:                  PromptAssemblyModeMain,
		BootstrapOwnerID:      "owner1",
		AgentCorePrompt:       "custom core",
		SpawnableAgentCatalog: "<available_agents>\n- agent_name: \"Coder\" | agent_id: \"coder\" — 单步实现\n</available_agents>",
		Tools:                 []Tool{&summaryOnlyTool{name: "read_file"}},
	}).SystemPrompt

	identityIdx := strings.Index(got, "custom identity")
	soulIdx := strings.Index(got, "custom soul")
	agentsFileIdx := strings.Index(got, "custom agents")
	userIdx := strings.Index(got, "custom user")
	coreIdx := strings.Index(got, "custom core")
	agentsCatalogIdx := strings.Index(got, "<available_agents>")
	toolsIdx := strings.Index(got, "<available_tools>")
	runtimeIdx := strings.Index(got, "## Runtime Context")

	if !(identityIdx < soulIdx && soulIdx < agentsFileIdx && agentsFileIdx < agentsCatalogIdx && agentsCatalogIdx < userIdx && userIdx < coreIdx && coreIdx < toolsIdx && toolsIdx < runtimeIdx) {
		t.Fatalf("expected IDENTITY -> SOUL -> AGENTS -> available_agents -> USER -> core -> available_tools -> Runtime order, got %q", got)
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

			for _, marker := range []string{"## Skills (mandatory)", "<skill name=\"weather\">"} {
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

	for _, marker := range []string{"## Skills (mandatory)", "<skill name=\"weather\">", "## Selected Skills (active)"} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q in prompt when skills are explicitly skipped, got %q", marker, got)
		}
	}
}
