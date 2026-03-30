package agent

import (
	"fmt"
	"strings"
	"time"

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
	isSubagent := assemblyMode == PromptAssemblyModeSubagent

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

	includeBootstrapGuide := !isSubagent && bundle.NeedsBootstrapGuide() && strings.TrimSpace(bundle.BootstrapGuide) != ""

	appendLayer("builtin_boundary", 10, "builtin_boundary", b.buildBuiltinBoundary(mode))

	switch assemblyMode {
	case PromptAssemblyModeSubagent:
		appendLayer("cognition", 20, "IDENTITY.md+SOUL.md+USER.md", b.buildCognitionLayer(bundle, false))
		appendLayer("agent_core", 50, resolveAgentCoreSource(params.AgentCorePrompt), b.resolveAgentCorePrompt(params.AgentCorePrompt, mode))
		appendLayer("subagent_descriptor", 60, "dynamic_subagent", strings.TrimSpace(params.SubagentDescriptor))
		appendLayer("subagent_spawnable_catalog", 65, "dynamic_catalog", strings.TrimSpace(params.SpawnableAgentCatalog))
	default:
		if includeBootstrapGuide {
			appendLayer("bootstrap_guide", 20, "BOOTSTRAP.md", wrapPromptFileLayer("## Bootstrap Guide", "BOOTSTRAP.md", bundle.BootstrapGuide))
		}
		appendLayer("cognition", 30, "IDENTITY.md+SOUL.md+AGENTS.md+dynamic_catalog+USER.md", b.buildCognitionLayer(bundle, true, strings.TrimSpace(params.SpawnableAgentCatalog)))
		appendLayer("agent_core", 55, resolveAgentCoreSource(params.AgentCorePrompt), b.resolveAgentCorePrompt(params.AgentCorePrompt, mode))
	}

	skillsLayer := ""
	// 子 agent 任何情况下都不拼接技能。
	// 主 agent 只有在 DisableSkillsPrompt 被显式设置为 true 时才跳过技能拼接；
	// nil 或 false 都保持默认行为，继续拼接技能上下文。
	if !isSubagent {
		skipSkills := params.DisableSkillsPrompt != nil && *params.DisableSkillsPrompt
		if !skipSkills {
			skillsLayer = strings.TrimSpace(params.SkillsOverride)
			if skillsLayer == "" {
				skillsLayer = b.buildSkillsContext(params.Skills, params.LoadedSkills, mode)
			}
		}
	}
	appendLayer("skills", 60, "runtime_skills", skillsLayer)
	toolSummary := strings.TrimSpace(params.ToolSummary)
	if toolSummary == "" {
		toolSummary = b.BuildToolsSummary(params.Tools)
	}
	appendLayer("tools", 70, "runtime_tools", toolSummary)
	appendLayer("context_summary", 85, "session_summary", b.buildContextSummary(params.SessionSummary))
	appendLayer("runtime_context", 90, "runtime_context", b.buildRuntimeContext(mode, params.WorkspaceRoot))

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

func (b *ContextBuilder) buildCognitionLayer(bundle bootstrapBundle, includeAgents bool, spawnableCatalog ...string) string {
	catalog := ""
	if len(spawnableCatalog) > 0 {
		catalog = strings.TrimSpace(spawnableCatalog[0])
	}
	parts := []string{
		buildTitledCognitionSection("Identity", strings.TrimSpace(bundle.Identity)),
		buildTitledCognitionSection("Soul", strings.TrimSpace(bundle.Soul)),
	}
	if includeAgents {
		collaborationParts := []string{strings.TrimSpace(bundle.Agents)}
		if catalog != "" {
			collaborationParts = append(collaborationParts, catalog)
		}
		parts = append(parts, buildTitledCognitionSection("Collaboration", joinNonEmpty(collaborationParts, "\n\n")))
	}
	parts = append(parts, buildTitledCognitionSection("User Context", strings.TrimSpace(bundle.User)))

	return joinNonEmpty(parts, "\n\n")
}

func buildTitledCognitionSection(title, content string) string {
	content = strings.TrimSpace(content)
	if title == "" || content == "" {
		return ""
	}
	return "# " + title + "\n\n" + shiftMarkdownHeadings(content, 1)
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

func resolveAgentCoreSource(agentCorePrompt string) string {
	if strings.TrimSpace(agentCorePrompt) != "" {
		return "agent_custom_prompt"
	}
	return "agent_core_empty"
}

func (b *ContextBuilder) resolveAgentCorePrompt(agentCorePrompt string, mode PromptMode) string {
	return strings.TrimSpace(agentCorePrompt)
}

func (b *ContextBuilder) buildBuiltinBoundary(mode PromptMode) string {
	if mode == PromptModeNone {
		return "You are a personal assistant running inside SunClaw."
	}

	return joinNonEmpty([]string{
		b.buildCommonBoundary(),
		b.buildSafety(),
	}, "\n\n---\n\n")
}

func (b *ContextBuilder) buildCommonBoundary() string {
	return `## Builtin Boundary

- This layer defines only common, non-overridable boundaries.
- Never hallucinate search results, fetched content, file contents, command output, or tool outcomes.
- Do not describe planned, partial, attempted, or inferred work as completed work.
- Respect approvals, sandbox limits, denylists, and tool-specific restrictions.
- You must prioritize safety, human oversight, and absolute accuracy over speed or completion.
- When a first-class tool exists for an action, you MUST use the tool instead of claiming results from memory.
- For agent-specific collaboration, ownership, orchestration, delegation, and execution strategy, ` + "`AGENTS.md`" + ` is the authoritative decision layer unless it conflicts with system safety or tool policy.
- If ` + "`AGENTS.md`" + ` defines workflow or decision rules, follow ` + "`AGENTS.md`" + ` rather than inventing a competing process in the builtin boundary layer.`
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

func (b *ContextBuilder) buildRuntimeContext(mode PromptMode, workspaceRoot string) string {
	if mode == PromptModeNone {
		return ""
	}

	parts := []string{
		fmt.Sprintf("## Runtime Context\n\n**Current Time**: %s", time.Now().Format("2006-01-02 15:04:05 MST")),
		b.buildWorkspace(workspaceRoot),
	}
	if mode != PromptModeMinimal {
		parts = append(parts, b.buildRuntime())
	}
	return joinNonEmpty(parts, "\n\n")
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
