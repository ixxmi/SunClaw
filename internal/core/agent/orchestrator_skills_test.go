package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/smallnest/goclaw/internal/core/providers"
	workspacepkg "github.com/smallnest/goclaw/internal/workspace"
)

type promptCaptureProvider struct {
	messages []providers.Message
}

func (p *promptCaptureProvider) Chat(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, options ...providers.ChatOption) (*providers.Response, error) {
	p.messages = append([]providers.Message(nil), messages...)
	return &providers.Response{
		Content:      "ok",
		FinishReason: "stop",
	}, nil
}

func (p *promptCaptureProvider) ChatWithTools(ctx context.Context, messages []providers.Message, tools []providers.ToolDefinition, options ...providers.ChatOption) (*providers.Response, error) {
	return p.Chat(ctx, messages, tools, options...)
}

func (p *promptCaptureProvider) Close() error { return nil }

func TestStreamAssistantResponse_AppendsSkillSummaryToCustomPrompt(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)
	provider := &promptCaptureProvider{}
	skills := []*Skill{
		{
			Name:        "weather",
			Description: "Use when the user asks about weather or forecast.",
			Content:     "# Weather\n\nDetailed instructions.",
		},
	}

	state := NewAgentState()
	state.SystemPrompt = "Custom orchestrator prompt"
	state.Messages = []AgentMessage{
		{
			Role:    RoleUser,
			Content: []ContentBlock{TextContent{Text: "上海明天天气怎么样"}},
		},
	}

	orchestrator := NewOrchestrator(&LoopConfig{
		Provider:       provider,
		ContextBuilder: builder,
		Skills:         skills,
	}, state)

	if _, err := orchestrator.streamAssistantResponse(context.Background(), state); err != nil {
		t.Fatalf("streamAssistantResponse error: %v", err)
	}
	if len(provider.messages) == 0 {
		t.Fatalf("expected provider to receive messages")
	}

	systemPrompt := provider.messages[0].Content
	checks := []string{
		"Custom orchestrator prompt",
		"# Skills (mandatory)",
		"<skill name=\"weather\">",
		"Use when the user asks about weather or forecast.",
	}
	for _, want := range checks {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
}

func TestStreamAssistantResponse_AppendsSelectedSkillContentToCustomPrompt(t *testing.T) {
	workspace := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspace), workspace)
	provider := &promptCaptureProvider{}
	skills := []*Skill{
		{
			Name:        "weather",
			Description: "Use when the user asks about weather or forecast.",
			Content:     "# Weather\n\nDetailed instructions.",
		},
	}

	state := NewAgentState()
	state.SystemPrompt = "Custom orchestrator prompt"
	state.LoadedSkills = []string{"weather"}
	state.Messages = []AgentMessage{
		{
			Role:    RoleUser,
			Content: []ContentBlock{TextContent{Text: "继续"}},
		},
	}

	orchestrator := NewOrchestrator(&LoopConfig{
		Provider:       provider,
		ContextBuilder: builder,
		Skills:         skills,
	}, state)

	if _, err := orchestrator.streamAssistantResponse(context.Background(), state); err != nil {
		t.Fatalf("streamAssistantResponse error: %v", err)
	}
	if len(provider.messages) == 0 {
		t.Fatalf("expected provider to receive messages")
	}

	systemPrompt := provider.messages[0].Content
	checks := []string{
		"Custom orchestrator prompt",
		"# Selected Skills (active)",
		"# Weather",
		"Detailed instructions.",
	}
	for _, want := range checks {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt missing %q", want)
		}
	}
	if strings.Contains(systemPrompt, "# Skills (mandatory)") {
		t.Fatalf("selected skill phase should not include summary section")
	}
}

func TestStreamAssistantResponse_AppendsBootstrapToCustomPrompt(t *testing.T) {
	workspaceDir := t.TempDir()
	bootstrapDir := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "vibecoding" {
			return bootstrapDir
		}
		return workspaceDir
	})
	provider := &promptCaptureProvider{}

	agentsTemplate, err := workspacepkg.ReadEmbeddedTemplate("AGENTS.md")
	if err != nil {
		t.Fatalf("read AGENTS template: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bootstrapDir, "AGENTS.md"), []byte(agentsTemplate), 0644); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(bootstrapDir, "SOUL.md"), []byte("# Soul\n\nvibecoding bootstrap soul"), 0644); err != nil {
		t.Fatalf("write SOUL.md: %v", err)
	}

	state := NewAgentState()
	state.SystemPrompt = "Custom orchestrator prompt"
	state.BootstrapOwnerID = "vibecoding"
	state.Messages = []AgentMessage{
		{
			Role:    RoleUser,
			Content: []ContentBlock{TextContent{Text: "你好"}},
		},
	}

	orchestrator := NewOrchestrator(&LoopConfig{
		Provider:       provider,
		ContextBuilder: builder,
	}, state)

	if _, err := orchestrator.streamAssistantResponse(context.Background(), state); err != nil {
		t.Fatalf("streamAssistantResponse error: %v", err)
	}
	if len(provider.messages) == 0 {
		t.Fatalf("expected provider to receive messages")
	}

	systemPrompt := provider.messages[0].Content
	if !strings.Contains(systemPrompt, "Custom orchestrator prompt") {
		t.Fatalf("expected custom orchestrator prompt in system prompt, got %q", systemPrompt)
	}
	for _, marker := range []string{
		"### AGENTS.md",
		"### AGENT.md",
		"### SOUL.md",
		"vibecoding bootstrap soul",
		strings.TrimSpace(agentsTemplate),
	} {
		if !strings.Contains(systemPrompt, marker) {
			t.Fatalf("expected %q in system prompt, got %q", marker, systemPrompt)
		}
	}
	for _, marker := range []string{"### BOOTSTRAP.md", "# Personality", "# Collaboration Rules"} {
		if strings.Contains(systemPrompt, marker) {
			t.Fatalf("did not expect legacy cognition/bootstrap marker %q, got %q", marker, systemPrompt)
		}
	}
}

func TestStreamAssistantResponse_AppendsBootstrapAfterTemplateAgents(t *testing.T) {
	workspaceDir := t.TempDir()
	ownerBootstrapDir := filepath.Join(t.TempDir(), "agents", "vibecoding", "bootstrap")
	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "vibecoding" {
			return ownerBootstrapDir
		}
		return workspaceDir
	})
	provider := &promptCaptureProvider{}

	for _, name := range []string{"AGENTS.md", "IDENTITY.md", "SOUL.md", "USER.md"} {
		content, err := workspacepkg.ReadEmbeddedTemplate(name)
		if err != nil {
			t.Fatalf("read %s template: %v", name, err)
		}
		if err := os.MkdirAll(ownerBootstrapDir, 0755); err != nil {
			t.Fatalf("mkdir ownerBootstrapDir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(ownerBootstrapDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	state := NewAgentState()
	state.SystemPrompt = "你是 SunClaw 的 vibecoding 主编排 Agent。"
	state.BootstrapOwnerID = "vibecoding"
	state.Messages = []AgentMessage{
		{
			Role:    RoleUser,
			Content: []ContentBlock{TextContent{Text: "你是谁"}},
		},
	}

	orchestrator := NewOrchestrator(&LoopConfig{
		Provider:       provider,
		ContextBuilder: builder,
	}, state)

	if _, err := orchestrator.streamAssistantResponse(context.Background(), state); err != nil {
		t.Fatalf("streamAssistantResponse error: %v", err)
	}
	if len(provider.messages) == 0 {
		t.Fatalf("expected provider to receive messages")
	}

	systemPrompt := provider.messages[0].Content
	checks := []string{
		"### IDENTITY.md",
		"### AGENTS.md",
		"### AGENT.md",
		"### SOUL.md",
		"### USER.md",
	}
	for _, want := range checks {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("system prompt missing %q, got %q", want, systemPrompt)
		}
	}
	for _, marker := range []string{"### BOOTSTRAP.md", "BOOTSTRAP.md - Hello, World", "## Bootstrap Mode"} {
		if strings.Contains(systemPrompt, marker) {
			t.Fatalf("did not expect bootstrap marker %q, got %q", marker, systemPrompt)
		}
	}

	identityFileIdx := strings.Index(systemPrompt, "### IDENTITY.md")
	workspaceIdx := strings.Index(systemPrompt, "## Workspace")
	agentsFileIdx := strings.Index(systemPrompt, "### AGENTS.md")
	coreIdx := strings.Index(systemPrompt, "你是 SunClaw 的 vibecoding 主编排 Agent。")
	soulFileIdx := strings.Index(systemPrompt, "### SOUL.md")
	userFileIdx := strings.Index(systemPrompt, "### USER.md")
	if !(identityFileIdx < workspaceIdx &&
		workspaceIdx < agentsFileIdx &&
		agentsFileIdx < coreIdx &&
		coreIdx < soulFileIdx &&
		soulFileIdx < userFileIdx) {
		t.Fatalf("expected IDENTITY.md -> Workspace -> AGENTS.md -> core -> SOUL.md -> USER.md order, got %q", systemPrompt)
	}
}

func TestStreamAssistantResponse_SubagentSkipsBootstrapGuideAndSkills(t *testing.T) {
	workspaceDir := t.TempDir()
	ownerBootstrapDir := filepath.Join(t.TempDir(), "agents", "vibecoding", "bootstrap")
	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "vibecoding" {
			return ownerBootstrapDir
		}
		return workspaceDir
	})
	provider := &promptCaptureProvider{}
	skills := []*Skill{
		{
			Name:        "weather",
			Description: "Use when the user asks about weather or forecast.",
			Content:     "# Weather\n\nDetailed instructions.",
		},
	}

	state := NewAgentState()
	state.SystemPrompt = "Subagent target prompt"
	state.IsSubagent = true
	state.BootstrapOwnerID = "vibecoding"
	state.SubagentDescriptor = "# Subagent Context\n\nFocus on the delegated task."
	state.Messages = []AgentMessage{
		{
			Role:    RoleUser,
			Content: []ContentBlock{TextContent{Text: "继续"}},
		},
	}

	orchestrator := NewOrchestrator(&LoopConfig{
		Provider:       provider,
		ContextBuilder: builder,
		Skills:         skills,
	}, state)

	if _, err := orchestrator.streamAssistantResponse(context.Background(), state); err != nil {
		t.Fatalf("streamAssistantResponse error: %v", err)
	}
	if len(provider.messages) == 0 {
		t.Fatalf("expected provider to receive messages")
	}

	systemPrompt := provider.messages[0].Content
	if !strings.Contains(systemPrompt, "# Subagent Context") {
		t.Fatalf("expected subagent descriptor in system prompt, got %q", systemPrompt)
	}
	for _, marker := range []string{
		"# Skills (mandatory)",
		"# Selected Skills (active)",
		"## Bootstrap Mode",
		"BOOTSTRAP.md - Hello, World",
		"### IDENTITY.md",
		"### SOUL.md",
		"### USER.md",
	} {
		if strings.Contains(systemPrompt, marker) {
			t.Fatalf("did not expect %q in subagent system prompt, got %q", marker, systemPrompt)
		}
	}
	if !strings.Contains(systemPrompt, "# Subagent Runtime Context") {
		t.Fatalf("expected subagent runtime context in system prompt, got %q", systemPrompt)
	}
}

func TestStreamAssistantResponse_SubagentDoesNotIncludeIdentitySoulUserEvenWhenAvailable(t *testing.T) {
	workspaceDir := t.TempDir()
	ownerBootstrapDir := filepath.Join(t.TempDir(), "agents", "vibecoding", "bootstrap")
	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)
	builder.SetBootstrapDirResolver(func(ownerID string) string {
		if ownerID == "vibecoding" {
			return ownerBootstrapDir
		}
		return workspaceDir
	})
	provider := &promptCaptureProvider{}

	files := map[string]string{
		"IDENTITY.md": "# Identity\n\nsubagent identity",
		"AGENTS.md":   "# AGENTS\n\nsubagent collaboration rules",
		"USER.md":     "# User\n\nuser preference",
		"SOUL.md":     "# Soul\n\nsubagent personality",
	}
	for name, content := range files {
		if err := os.MkdirAll(ownerBootstrapDir, 0755); err != nil {
			t.Fatalf("mkdir ownerBootstrapDir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(ownerBootstrapDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	state := NewAgentState()
	state.SystemPrompt = "Subagent target prompt"
	state.IsSubagent = true
	state.BootstrapOwnerID = "vibecoding"
	state.SubagentDescriptor = "# Subagent Context\n\nFocus on the delegated task."
	state.Messages = []AgentMessage{
		{
			Role:    RoleUser,
			Content: []ContentBlock{TextContent{Text: "继续"}},
		},
	}

	orchestrator := NewOrchestrator(&LoopConfig{
		Provider:       provider,
		ContextBuilder: builder,
	}, state)

	if _, err := orchestrator.streamAssistantResponse(context.Background(), state); err != nil {
		t.Fatalf("streamAssistantResponse error: %v", err)
	}
	if len(provider.messages) == 0 {
		t.Fatalf("expected provider to receive messages")
	}

	systemPrompt := provider.messages[0].Content
	for _, marker := range []string{
		"# Subagent Runtime Context",
		"# Runtime Context",
	} {
		if !strings.Contains(systemPrompt, marker) {
			t.Fatalf("expected %q in subagent runtime context, got %q", marker, systemPrompt)
		}
	}
	for _, marker := range []string{
		"# Cognition Snapshot",
		"# AGENTS",
		"subagent collaboration rules",
		"### IDENTITY.md",
		"subagent identity",
		"### USER.md",
		"user preference",
		"### SOUL.md",
		"subagent personality",
	} {
		if strings.Contains(systemPrompt, marker) {
			t.Fatalf("did not expect legacy subagent cognition marker %q, got %q", marker, systemPrompt)
		}
	}
}

func TestStreamAssistantResponse_SubagentDoesNotIncludeSpawnableCatalog(t *testing.T) {
	workspaceDir := t.TempDir()
	builder := NewContextBuilder(NewMemoryStore(workspaceDir), workspaceDir)
	provider := &promptCaptureProvider{}

	state := NewAgentState()
	state.SystemPrompt = "Subagent target prompt"
	state.IsSubagent = true
	state.BootstrapOwnerID = "vibecoding"
	state.SubagentDescriptor = "# Subagent Context\n\nFocus on the delegated task."
	state.SpawnableAgentCatalog = "## Available Agents\n\nReference only. Consult this directory only when selecting the next child agent for the current step.\nPrefer `agent_name`; add `agent_id` only when disambiguation is needed.\n\n- Coder (`coder`): 单步实现\n"
	state.Messages = []AgentMessage{
		{
			Role:    RoleUser,
			Content: []ContentBlock{TextContent{Text: "继续"}},
		},
	}

	orchestrator := NewOrchestrator(&LoopConfig{
		Provider:       provider,
		ContextBuilder: builder,
	}, state)

	if _, err := orchestrator.streamAssistantResponse(context.Background(), state); err != nil {
		t.Fatalf("streamAssistantResponse error: %v", err)
	}
	if len(provider.messages) == 0 {
		t.Fatalf("expected provider to receive messages")
	}

	systemPrompt := provider.messages[0].Content
	if strings.Contains(systemPrompt, "<available_agents>") {
		t.Fatalf("did not expect subagent system prompt to include spawnable catalog, got %q", systemPrompt)
	}
}
