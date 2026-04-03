package agent

import (
	"fmt"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/namespaces"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// PromptAssemblyMode 表示当前 system prompt 的装配模式。
type PromptAssemblyMode string

const (
	PromptAssemblyModeMain     PromptAssemblyMode = "main"
	PromptAssemblyModeSubagent PromptAssemblyMode = "subagent"
)

// PromptLayerSnapshot 用于调试和测试最终 prompt 的层级来源。
type PromptLayerSnapshot struct {
	Name     string
	Priority int
	Enabled  bool
	Source   string
	Content  string
}

// PromptAssemblyParams 描述一次运行时 system prompt 装配所需的输入。
type PromptAssemblyParams struct {
	Mode                  PromptAssemblyMode
	PromptMode            PromptMode
	AgentCorePrompt       string
	BootstrapOwnerID      string
	WorkspaceRoot         string
	SpawnableAgentCatalog string
	SubagentDescriptor    string
	Skills                []*Skill
	LoadedSkills          []string
	SkillsOverride        string
	DisableSkillsPrompt   *bool // 仅显式 true 时跳过技能拼接；nil/false 均拼接
	SessionSummary        string
	Tools                 []Tool
	ToolSummary           string
	SessionKey            string
}

// PromptAssemblyResult 是统一装配器的产物。
type PromptAssemblyResult struct {
	SystemPrompt string
	Layers       []PromptLayerSnapshot
}

type bootstrapBundle struct {
	Soul              string
	SoulEffective     bool
	Identity          string
	IdentityEffective bool
	Agents            string
	AgentsEffective   bool
	User              string
	UserEffective     bool
	BootstrapGuide    string
}

func (b bootstrapBundle) HasAnyEffectiveCognition() bool {
	return b.SoulEffective || b.IdentityEffective || b.AgentsEffective || b.UserEffective
}

func (b bootstrapBundle) HasAllCognitiveFiles() bool {
	return strings.TrimSpace(b.Identity) != "" &&
		strings.TrimSpace(b.User) != "" &&
		strings.TrimSpace(b.Agents) != "" &&
		strings.TrimSpace(b.Soul) != ""
}

func (b bootstrapBundle) PreferIdentityUserBeforeAgents() bool {
	return b.HasAllCognitiveFiles() && b.IdentityEffective && b.UserEffective
}

func (b bootstrapBundle) NeedsBootstrapGuide() bool {
	if strings.TrimSpace(b.Identity) == "" || !b.IdentityEffective {
		return true
	}
	if strings.TrimSpace(b.User) == "" || !b.UserEffective {
		return true
	}
	return false
}

// AssemblePrompt 按分层顺序和回退规则构建最终 system prompt。
func (b *ContextBuilder) AssemblePrompt(params *PromptAssemblyParams) *PromptAssemblyResult {
	if params == nil {
		params = &PromptAssemblyParams{}
	}

	mode := params.PromptMode
	if mode == "" {
		mode = PromptModeFull
	}

	assemblyMode := params.Mode
	if assemblyMode == "" {
		if strings.TrimSpace(params.SubagentDescriptor) != "" {
			assemblyMode = PromptAssemblyModeSubagent
		} else {
			assemblyMode = PromptAssemblyModeMain
		}
	}
	bundle := b.loadBootstrapBundleForOwner(params.BootstrapOwnerID, params.WorkspaceRoot)
	layers := make([]PromptLayerSnapshot, 0, 10)

	appendLayer := func(name string, priority int, source string, content string) {
		content = strings.TrimSpace(content)
		layers = append(layers, PromptLayerSnapshot{
			Name:     name,
			Priority: priority,
			Enabled:  content != "",
			Source:   source,
			Content:  content,
		})
	}

	switch assemblyMode {
	case PromptAssemblyModeSubagent:
		appendLayer("core_prompt", 10, resolveAgentCoreSource(params.AgentCorePrompt), b.buildSubagentCorePrompt(params.AgentCorePrompt, mode))
		appendLayer("subagent_descriptor", 20, "dynamic_subagent", strings.TrimSpace(params.SubagentDescriptor))
		appendLayer("subagent_context", 30, "subagent_context", b.buildSubagentContext(params.SessionSummary, params.WorkspaceRoot, params.SessionKey, mode))
	default:
		appendLayer("identity", 10, "IDENTITY.md", wrapPromptFileLayer("", "IDENTITY.md", strings.TrimSpace(bundle.Identity)))
		appendLayer("workspace", 20, "workspace", b.buildWorkspace(params.WorkspaceRoot))
		appendLayer("important_rules", 30, "AGENTS.md", wrapPromptFileLayer("", "AGENTS.md", strings.TrimSpace(bundle.Agents)))
		appendLayer("agent_prompt", 40, resolveAgentCoreSource(params.AgentCorePrompt), wrapPromptFileLayer("", "AGENT.md", b.resolveAgentCorePrompt(params.AgentCorePrompt, mode)))
		appendLayer("soul", 50, "SOUL.md", wrapPromptFileLayer("", "SOUL.md", strings.TrimSpace(bundle.Soul)))
		appendLayer("user_context", 60, "USER.md", wrapPromptFileLayer("", "USER.md", strings.TrimSpace(bundle.User)))

		skillsLayer := ""
		skipSkills := params.DisableSkillsPrompt != nil && *params.DisableSkillsPrompt
		if !skipSkills {
			skillsLayer = strings.TrimSpace(params.SkillsOverride)
			if skillsLayer == "" {
				skillsLayer = b.buildSkillsContext(params.Skills, params.LoadedSkills, mode)
			}
		}
		appendLayer("skills", 70, "runtime_skills", skillsLayer)

		toolSummary := strings.TrimSpace(params.ToolSummary)
		if toolSummary == "" {
			toolSummary = b.BuildToolsSummary(params.Tools)
		}
		appendLayer("tools", 80, "runtime_tools", toolSummary)
		appendLayer("context", 90, "runtime_context", b.buildMainContext(strings.TrimSpace(params.SpawnableAgentCatalog), params.SessionSummary, mode))
		appendLayer("user_info", 100, "session_identity", b.buildUserInfo(params.SessionKey))
	}

	result := &PromptAssemblyResult{
		SystemPrompt: renderPromptLayers(layers),
		Layers:       layers,
	}

	logMessage := "[Final assembled main-agent system prompt]"
	if assemblyMode == PromptAssemblyModeSubagent {
		logMessage = "[Final assembled subagent system prompt]"
	}

	logger.Info(logMessage,
		zap.String("assembly_mode", string(assemblyMode)),
		zap.String("prompt_mode", string(mode)),
		zap.String("bootstrap_owner_id", strings.TrimSpace(params.BootstrapOwnerID)),
		zap.Int("layer_count", len(layers)),
		zap.Strings("enabled_layers", enabledLayerNames(layers)),
		zap.Strings("layer_sources", enabledLayerSources(layers)),
		zap.Int("prompt_length", len(result.SystemPrompt)),
		zap.String("system_prompt", result.SystemPrompt))

	return result
}

func (b *ContextBuilder) buildSubagentCorePrompt(agentCorePrompt string, mode PromptMode) string {
	content := strings.TrimSpace(b.resolveAgentCorePrompt(agentCorePrompt, mode))
	if content == "" {
		return ""
	}
	return "# Core Prompt\n\n" + content
}

func (b *ContextBuilder) buildMainContext(spawnableAgentCatalog string, sessionSummary string, mode PromptMode) string {
	parts := []string{
		buildSpawnableAgentCatalogLayer(strings.TrimSpace(spawnableAgentCatalog)),
		b.buildContextSummary(sessionSummary),
		b.buildRuntimeSnapshot(mode),
	}
	return joinNonEmpty(parts, "\n\n---\n\n")
}

func (b *ContextBuilder) buildSubagentContext(
	sessionSummary string,
	workspaceRoot string,
	sessionKey string,
	mode PromptMode,
) string {
	parts := []string{
		b.buildContextSummary(sessionSummary),
		b.buildRuntimeContext(mode, workspaceRoot),
		b.buildUserInfo(sessionKey),
	}
	content := joinNonEmpty(parts, "\n\n---\n\n")
	if content == "" {
		return ""
	}
	return "# Subagent Runtime Context\n\n" + content
}

func (b *ContextBuilder) buildUserInfo(sessionKey string) string {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return ""
	}

	values := parseStructuredSessionKey(sessionKey)
	identity, _ := namespaces.FromSessionKey(sessionKey)

	lines := []string{}
	appendLine := func(label, value string) {
		value = strings.TrimSpace(value)
		if value == "" || value == "default" {
			return
		}
		lines = append(lines, fmt.Sprintf("- %s: %s", label, value))
	}

	appendLine("Tenant", identity.TenantID)
	appendLine("Channel", identity.Channel)
	appendLine("Account", identity.AccountID)
	appendLine("Sender", identity.SenderID)
	appendLine("Chat", values["chat"])
	appendLine("Thread", values["thread"])
	appendLine("Agent", values["agent"])
	appendLine("Subagent", values["subagent"])

	if len(lines) == 0 {
		lines = append(lines, fmt.Sprintf("- Session Key: %s", sessionKey))
	}

	return "# User Information\n\n" + strings.Join(lines, "\n")
}

func parseStructuredSessionKey(sessionKey string) map[string]string {
	parts := strings.Split(strings.TrimSpace(sessionKey), ":")
	values := make(map[string]string, len(parts)/2)
	for i := 0; i+1 < len(parts); i += 2 {
		key := strings.TrimSpace(parts[i])
		value := strings.TrimSpace(parts[i+1])
		switch key {
		case "tenant", "channel", "account", "sender", "chat", "thread", "agent", "subagent", "session":
			values[key] = value
		default:
			i = len(parts)
		}
	}
	return values
}

func (b *ContextBuilder) buildCognitionLayer(bundle bootstrapBundle, includeAgents bool) string {
	return joinNonEmpty(b.buildCognitionSections(bundle, includeAgents, 1), "\n\n")
}

func (b *ContextBuilder) buildSubagentCognitionSnapshot(bundle bootstrapBundle) string {
	sections := b.buildCognitionSections(bundle, true, 2)
	if len(sections) == 0 {
		return ""
	}
	return joinNonEmpty(append([]string{"# Cognition Snapshot"}, sections...), "\n\n")
}

func (b *ContextBuilder) buildCognitionSections(bundle bootstrapBundle, includeAgents bool, level int) []string {
	sections := []string{
		buildTitledCognitionSection("Identity", strings.TrimSpace(bundle.Identity), level),
	}
	if includeAgents {
		sections = append(sections, buildTitledRawCognitionSection("Collaboration Rules", strings.TrimSpace(bundle.Agents), level))
	}
	sections = append(sections,
		buildTitledCognitionSection("User Context", strings.TrimSpace(bundle.User), level),
		buildTitledCognitionSection("Personality", strings.TrimSpace(bundle.Soul), level),
	)
	out := make([]string, 0, len(sections))
	for _, section := range sections {
		if strings.TrimSpace(section) != "" {
			out = append(out, section)
		}
	}
	return out
}

func buildTitledCognitionSection(title, content string, level int) string {
	content = strings.TrimSpace(content)
	if title == "" || content == "" {
		return ""
	}
	if level < 1 {
		level = 1
	}
	return strings.Repeat("#", level) + " " + title + "\n\n" + shiftMarkdownHeadings(content, level)
}

func buildTitledRawCognitionSection(title, content string, level int) string {
	content = strings.TrimSpace(content)
	if title == "" || content == "" {
		return ""
	}
	if level < 1 {
		level = 1
	}
	return strings.Repeat("#", level) + " " + title + "\n\n" + content
}

func shiftMarkdownHeadings(content string, delta int) string {
	if delta == 0 || strings.TrimSpace(content) == "" {
		return content
	}

	lines := strings.Split(content, "\n")
	inFence := false
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			continue
		}
		if inFence {
			continue
		}

		leadingSpaces := len(line) - len(strings.TrimLeft(line, " \t"))
		body := line[leadingSpaces:]
		if !strings.HasPrefix(body, "#") {
			continue
		}

		hashes := 0
		for hashes < len(body) && body[hashes] == '#' {
			hashes++
		}
		if hashes == 0 || hashes >= len(body) || body[hashes] != ' ' {
			continue
		}

		newHashes := hashes + delta
		if newHashes < 1 {
			newHashes = 1
		}
		if newHashes > 6 {
			newHashes = 6
		}
		lines[i] = line[:leadingSpaces] + strings.Repeat("#", newHashes) + body[hashes:]
	}

	return strings.Join(lines, "\n")
}

func enabledLayerNames(layers []PromptLayerSnapshot) []string {
	names := make([]string, 0, len(layers))
	for _, layer := range layers {
		if !layer.Enabled {
			continue
		}
		names = append(names, layer.Name)
	}
	return names
}

func enabledLayerSources(layers []PromptLayerSnapshot) []string {
	sources := make([]string, 0, len(layers))
	for _, layer := range layers {
		if !layer.Enabled {
			continue
		}
		sources = append(sources, fmt.Sprintf("%s=%s", layer.Name, layer.Source))
	}
	return sources
}

func renderPromptLayers(layers []PromptLayerSnapshot) string {
	parts := make([]string, 0, len(layers))
	for _, layer := range layers {
		if !layer.Enabled {
			continue
		}
		parts = append(parts, layer.Content)
	}
	return joinNonEmpty(parts, "\n\n---\n\n")
}

func buildSpawnableAgentCatalogLayer(content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if strings.Contains(content, "<available_agents>") {
		return content
	}
	content = strings.Replace(content, "## Available Agents", "# Available Agents", 1)
	return fmt.Sprintf("\n%s\n", content)
}

func resolveAgentCoreSource(agentCorePrompt string) string {
	if strings.TrimSpace(agentCorePrompt) != "" {
		return "agent_custom_prompt"
	}
	return "agent_core_empty"
}

func (b *ContextBuilder) resolveAgentCorePrompt(agentCorePrompt string, mode PromptMode) string {
	content := strings.TrimSpace(agentCorePrompt)
	if content == "" {
		return ""
	}
	if strings.HasPrefix(content, "#") || strings.HasPrefix(content, "<") {
		return content
	}
	return buildTitledRawCognitionSection("Agent Core Prompt", content, 1)
}

func (b *ContextBuilder) buildBuiltinBoundary(mode PromptMode) string {
	if mode == PromptModeNone {
		return "You are a personal assistant running inside SunClaw."
	}

	return joinNonEmpty([]string{
		b.buildCommonBoundary(),
		b.buildSafety(),
		b.buildExecutionNorms(),
		b.buildTaskOrchestrationNorms(),
	}, "\n\n---\n\n")
}

func (b *ContextBuilder) buildCommonBoundary() string {
	return `# Builtin Boundary
- Never hallucinate search results, fetched content, file contents, command output, or tool outcomes.
- Do not describe planned, partial, attempted, or inferred work as completed work.
- Respect approvals, sandbox limits, denylists, and tool-specific restrictions.
- You must prioritize safety, human oversight, and absolute accuracy over speed or completion.`
}

func (b *ContextBuilder) buildBuiltinGenericCore(mode PromptMode) string {
	if mode == PromptModeNone {
		return ""
	}

	isMinimal := mode == PromptModeMinimal
	parts := []string{
		b.buildBuiltinGenericIdentity(),
		b.buildToolCallStyle(),
	}
	if !isMinimal {
		parts = append(parts,
			b.buildCommunicationStyle(),
			b.buildErrorHandling(),
			b.buildCLIReference(),
			b.buildDocsSection(),
			b.buildMessagingSection(),
		)
	}
	return joinNonEmpty(parts, "\n\n---\n\n")
}

func (b *ContextBuilder) buildBuiltinGenericIdentity() string {
	return `## Builtin Generic Core

You are **SunClaw**, a personal AI assistant running on the user's system.
You are not a passive chat bot. Your default posture is to understand the request, use available skills and tools, and complete the work directly when policy allows.

### Task Complexity Guidelines

- Simple tasks: execute directly with the relevant tools
- Moderate tasks: execute while keeping the user oriented with short progress updates
- Complex or long tasks: decompose carefully and consider using a sub-agent when it reduces complexity
- Before non-trivial work: briefly acknowledge the request and state the next concrete step`
}

func (b *ContextBuilder) buildBootstrapModeNotice() string {
	return `## Bootstrap Mode

One or more required agent-specific cognitive files are still missing, empty, or left as template content (` + "`IDENTITY.md`" + `, ` + "`USER.md`" + `).
` + "`SOUL.md`" + ` and ` + "`AGENTS.md`" + ` may still use template content without blocking bootstrap completion.

Treat ` + "`BOOTSTRAP.md`" + ` below as the temporary guide for establishing identity and cognition.
Any fixed identity or role wording elsewhere in this system prompt is only temporary operational behavior, not the final self-identity.

Bootstrap behavior rules:
- During bootstrap, ` + "`BOOTSTRAP.md`" + ` overrides agent-core role wording for first-run onboarding, greetings, and identity questions.
- If the user says "你好", "hi", "hello", asks "你是谁", "你叫什么", or asks what you are, do not give a generic assistant introduction.
- Do not reply with generic introductions like "I am an AI assistant", "我是一个 AI 助手", "I am SunClaw", or any other fixed self-description that is not already written in ` + "`IDENTITY.md`" + `.
- Your first job is to say that you have not been initialized yet, then guide the user to define your identity, role, vibe, and the user profile.

If the user asks who you are, do not answer with a fixed identity unless that identity has already been explicitly written into ` + "`IDENTITY.md`" + `.`
}

func wrapPromptFileLayer(title, filename, content string) string {
	content = strings.TrimSpace(content)
	if content == "" {
		return ""
	}
	if strings.TrimSpace(title) == "" {
		return fmt.Sprintf("### %s\n\n%s", filename, content)
	}
	return fmt.Sprintf("%s\n\n### %s\n\n%s", title, filename, content)
}

func (b *ContextBuilder) buildRuntimeSnapshot(mode PromptMode) string {
	if mode == PromptModeNone {
		return ""
	}

	parts := []string{
		fmt.Sprintf("# Runtime Context\n\n**Current Time**: %s", time.Now().Format("2006-01-02 15:04:05 MST")),
	}
	if mode != PromptModeMinimal {
		parts = append(parts, b.buildRuntime())
	}
	return joinNonEmpty(parts, "\n\n")
}

func (b *ContextBuilder) buildRuntimeContext(mode PromptMode, workspaceRoot string) string {
	if mode == PromptModeNone {
		return ""
	}

	parts := []string{
		b.buildWorkspace(workspaceRoot),
		b.buildRuntimeSnapshot(mode),
	}
	return joinNonEmpty(parts, "\n\n---\n\n")
}

func (b *ContextBuilder) loadBootstrapBundleForOwner(ownerID, workspaceRoot string) bootstrapBundle {
	store := b.resolveBootstrapStore(ownerID, workspaceRoot)
	templates := b.resolveBootstrapTemplates()
	bundle := bootstrapBundle{}

	readFromStore := func(target *string, filename string, s *MemoryStore) {
		if s == nil {
			return
		}
		if content, err := s.ReadBootstrapFile(filename); err == nil {
			*target = strings.TrimSpace(content)
		}
	}

	readFromStore(&bundle.Soul, "SOUL.md", store)
	bundle.SoulEffective = isEffectiveCognition(bundle.Soul, templates.Soul)
	readFromStore(&bundle.Identity, "IDENTITY.md", store)
	bundle.IdentityEffective = isEffectiveCognition(bundle.Identity, templates.Identity)
	readFromStore(&bundle.Agents, "AGENTS.md", store)
	bundle.AgentsEffective = isEffectiveCognition(bundle.Agents, templates.Agents)
	readFromStore(&bundle.User, "USER.md", store)
	bundle.UserEffective = isEffectiveCognition(bundle.User, templates.User)

	if bundle.NeedsBootstrapGuide() {
		readFromStore(&bundle.BootstrapGuide, "BOOTSTRAP.md", store)
		if strings.TrimSpace(bundle.BootstrapGuide) == "" {
			bundle.BootstrapGuide = templates.BootstrapGuide
		}
	}

	// Isolation rule: do not fall back to root workspace BOOTSTRAP.md when within an owner workspace.
	// The previous behavior read BOOTSTRAP.md from root when owner has no cognitive files and no bootstrap guide.
	// This fallback has been removed to enforce workspace isolation.

	return bundle
}

func isEffectiveCognition(content, template string) bool {
	content = strings.TrimSpace(content)
	if content == "" {
		return false
	}
	template = strings.TrimSpace(template)
	if template == "" {
		return true
	}
	return content != template
}
