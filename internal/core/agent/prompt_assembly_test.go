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
		expected := strings.TrimSpace(content)
		if name != "AGENTS.md" {
			expected = shiftMarkdownHeadings(content, 1)
		}
		if !strings.Contains(got, expected) {
			t.Fatalf("expected prompt to include template cognition %s content, got %q", name, got)
		}
	}

	for _, marker := range []string{"# Identity", "# Collaboration Rules", "# User Context", "# Personality", "### BOOTSTRAP.md", "custom core"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected %q in prompt, got %q", marker, got)
		}
	}
	if strings.Contains(got, "## Bootstrap Mode") {
		t.Fatalf("did not expect legacy bootstrap notice, got %q", got)
	}

	bootstrapIdx := strings.Index(got, "### BOOTSTRAP.md")
	coreIdx := strings.Index(got, "custom core")
	identityIdx := strings.Index(got, "# Identity")
	agentsIdx := strings.Index(got, "# Collaboration Rules")
	userIdx := strings.Index(got, "# User Context")
	personalityIdx := strings.Index(got, "# Personality")
	if !(bootstrapIdx < coreIdx && coreIdx < identityIdx && identityIdx < agentsIdx && agentsIdx < userIdx && userIdx < personalityIdx) {
		t.Fatalf("expected BOOTSTRAP -> core -> IDENTITY -> AGENTS -> USER -> PERSONALITY order, got %q", got)
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

	coreIdx := strings.Index(got, "custom core")
	identityIdx := strings.Index(got, "custom identity")
	agentsIdx := strings.Index(got, "custom agents")
	userIdx := strings.Index(got, "custom user")
	personalityIdx := strings.Index(got, "custom soul")
	if !(coreIdx < identityIdx && identityIdx < agentsIdx && agentsIdx < userIdx && userIdx < personalityIdx) {
		t.Fatalf("expected core -> IDENTITY -> AGENTS -> USER -> PERSONALITY order once all four files exist, got %q", got)
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

	for _, marker := range []string{"custom identity", "custom user", "# Collaboration Rules", "# Personality"} {
		if !strings.Contains(got, marker) {
			t.Fatalf("expected %q to be injected, got %q", marker, got)
		}
	}
	if !strings.Contains(got, strings.TrimSpace(readWorkspaceTemplate(t, "AGENTS.md"))) {
		t.Fatalf("expected raw AGENTS.md content to be injected, got %q", got)
	}
	for _, marker := range []string{"### BOOTSTRAP.md", "BOOTSTRAP.md - Hello, World"} {
		if strings.Contains(got, marker) {
			t.Fatalf("did not expect %q when IDENTITY.md and USER.md are customized, got %q", marker, got)
		}
	}

	coreIdx := strings.Index(got, "custom core")
	identityIdx := strings.Index(got, "custom identity")
	agentsIdx := strings.Index(got, "# Collaboration Rules")
	userIdx := strings.Index(got, "custom user")
	personalityIdx := strings.Index(got, "# Personality")
	if !(coreIdx < identityIdx && identityIdx < agentsIdx && agentsIdx < userIdx && userIdx < personalityIdx) {
		t.Fatalf("expected core -> IDENTITY -> AGENTS -> USER -> PERSONALITY order when all four files exist, got %q", got)
	}
}

func TestAssemblePrompt_MainPlacesAvailableAgentsBeforeTools(t *testing.T) {
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
		SpawnableAgentCatalog: "## Available Agents\n\nReference only. Consult this directory only when selecting the next child agent for the current step.\nPrefer `agent_name`; add `agent_id` only when disambiguation is needed.\n\n- Coder (`coder`): 单步实现\n",
		Tools:                 []Tool{&summaryOnlyTool{name: "read_file"}},
	}).SystemPrompt

	coreIdx := strings.Index(got, "custom core")
	identityIdx := strings.Index(got, "custom identity")
	agentsFileIdx := strings.Index(got, "custom agents")
	userIdx := strings.Index(got, "custom user")
	personalityIdx := strings.Index(got, "custom soul")
	agentsCatalogIdx := strings.Index(got, "<available_agents>")
	toolsIdx := strings.Index(got, "<available_tools>")
	runtimeIdx := strings.Index(got, "## Runtime Context")

	if !(coreIdx < identityIdx && identityIdx < agentsFileIdx && agentsFileIdx < userIdx && userIdx < personalityIdx && personalityIdx < agentsCatalogIdx && agentsCatalogIdx < toolsIdx && toolsIdx < runtimeIdx) {
		t.Fatalf("expected core -> IDENTITY -> AGENTS -> USER -> PERSONALITY -> available_agents -> available_tools -> Runtime order, got %q", got)
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
