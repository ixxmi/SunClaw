package agent

import (
	"fmt"
	"os"
	"runtime"
	"strings"

	"github.com/smallnest/goclaw/internal/core/namespaces"
	"github.com/smallnest/goclaw/internal/core/session"
	"github.com/smallnest/goclaw/internal/logger"
	"github.com/smallnest/goclaw/internal/workspace"
	"go.uber.org/zap"
)

// PromptMode 控制系统提示词中包含哪些硬编码部分
// - "full": 所有部分（默认，用于主 agent）
// - "minimal": 精简部分（Tooling, Workspace, Runtime）- 用于子 agent
// - "none": 仅基本身份行，没有部分
type PromptMode string

const (
	PromptModeFull    PromptMode = "full"
	PromptModeMinimal PromptMode = "minimal"
	PromptModeNone    PromptMode = "none"
)

// ContextBuilder 上下文构建器
type ContextBuilder struct {
	memory               *MemoryStore
	bootstrapStore       *MemoryStore
	bootstrapDirResolver func(ownerID string) string
	workspace            string
}

// NewContextBuilder 创建上下文构建器
func NewContextBuilder(memory *MemoryStore, workspace string) *ContextBuilder {
	return &ContextBuilder{
		memory:         memory,
		bootstrapStore: memory,
		workspace:      workspace,
	}
}

// NewContextBuilderWithBootstrap 创建上下文构建器，并允许为 bootstrap 文件指定独立来源目录。
func NewContextBuilderWithBootstrap(memory *MemoryStore, workspace string, bootstrapStore *MemoryStore) *ContextBuilder {
	if bootstrapStore == nil {
		bootstrapStore = memory
	}
	return &ContextBuilder{
		memory:         memory,
		bootstrapStore: bootstrapStore,
		workspace:      workspace,
	}
}

// SetBootstrapDirResolver 设置按主 agent owner 解析认知目录的回调。
func (b *ContextBuilder) SetBootstrapDirResolver(resolver func(ownerID string) string) {
	b.bootstrapDirResolver = resolver
}

// BuildToolsSummary 构建工具列表摘要段落。
// 该段内容属于第 7 层工具层，由运行时统一装配进最终 system prompt。
func (b *ContextBuilder) BuildToolsSummary(tools []Tool) string {
	if len(tools) == 0 {
		return ""
	}

	var lines []string
	for _, t := range tools {
		name := t.Name()
		desc := strings.TrimSpace(t.Description())
		if desc == "" {
			lines = append(lines, fmt.Sprintf("- **%s**", name))
			continue
		}
		lines = append(lines, fmt.Sprintf("- **%s**: %s", name, desc))
	}

	return fmt.Sprintf("# Available Tools\n\n工具名称区分大小写，调用时请严格按列出的名称使用。\n结构化工具定义与当前运行时策略始终高于此摘要。\n\n%s\n",
		strings.Join(lines, "\n"))
}

// BuildSystemPrompt 构建完整 system prompt。
// 该方法保留给兼容入口使用，内部统一走 AssemblePrompt。
func (b *ContextBuilder) BuildSystemPrompt(skills []*Skill) string {
	return b.BuildSystemPromptWithMode(skills, PromptModeFull)
}

// BuildSystemPromptWithMode 使用指定模式构建 system prompt。
// 该方法保留给兼容入口使用，内部统一走 AssemblePrompt。
func (b *ContextBuilder) BuildSystemPromptWithMode(skills []*Skill, mode PromptMode) string {
	assembled := b.AssemblePrompt(&PromptAssemblyParams{
		Mode:        PromptAssemblyModeMain,
		PromptMode:  mode,
		Skills:      skills,
		ToolSummary: b.buildLegacyBuiltinToolLayer(mode),
	})
	return assembled.SystemPrompt
}

// buildSystemPromptWithSkills 使用指定的技能内容和模式构建 system prompt。
// 该方法保留给兼容入口使用，内部统一走 AssemblePrompt。
func (b *ContextBuilder) buildSystemPromptWithSkills(skillsContent string, mode PromptMode) string {
	return b.AssemblePrompt(&PromptAssemblyParams{
		Mode:           PromptAssemblyModeMain,
		PromptMode:     mode,
		SkillsOverride: skillsContent,
		ToolSummary:    b.buildLegacyBuiltinToolLayer(mode),
	}).SystemPrompt
}

func (b *ContextBuilder) buildLegacyBuiltinToolLayer(mode PromptMode) string {
	if mode == PromptModeNone {
		return ""
	}

	coreToolSummaries := map[string]string{
		"browser_navigate":       "Navigate to a URL and wait for page load",
		"browser_screenshot":     "Take page screenshots for visual analysis",
		"browser_get_text":       "Get page text content (extracts readable text from DOM)",
		"browser_click":          "Click elements on the page (by selector or coordinates)",
		"browser_fill_input":     "Fill input fields and textareas",
		"browser_execute_script": "Execute JavaScript in page context",
		"glob_files":             "Find files by path/name pattern before deeper inspection",
		"grep_content":           "Search file contents and return matching files or file:line hits",
		"read_file":              "Read file contents (raw only for simple small files, otherwise compact preview, supports line ranges)",
		"write_file":             "Create a new file or fully overwrite an existing file with complete content. Do not use run_shell for ordinary file writing when this tool fits",
		"edit_file":              "Edit an existing file by exact text replacement. Preferred tool for normal code edits to existing files",
		"update_config":          "Update IDENTITY.md / AGENTS.md / SOUL.md / USER.md for long-lived cognition and collaboration rules only. Do not write one-off task state into them",
		"list_files":             "List directory contents (recursive with -r)",
		"run_shell":              "Run shell commands for builds, tests, scripts, package installs, curl, and diagnostics. Do not use it for ordinary source-file edits or writes when file tools fit. PROHIBITED: Never use 'crontab' commands for scheduled tasks - use the 'cron' tool instead",
		"sandbox_execute":        "Run short inline code snippets or commands with sandbox-aware execution. Do not use this as a general shell replacement; ordinary workspace commands should use run_shell",
		"process":                "Manage background shell sessions (poll, kill, list)",
		"web_search":             "Search the web using API (Brave/Search APIs)",
		"web_fetch":              "Fetch and extract readable content from a URL",
		"use_skill":              "Load a specialized skill. SKILLS HAVE HIGHEST PRIORITY - always check Skills section first",
		"send_message":           "Send a proactive text message to the current or specified chat. Use this for acknowledgement, progress updates, or intentional multi-message delivery",
		"send_file":              "Send one image or file to the current or specified chat",
		"message":                "Send messages and channel actions (polls, reactions, buttons)",
		"sessions_spawn":         "Spawn one tracked child-agent task for the current delegated step; it will auto-announce the result back when finished",
		"memory_search":          "Search stored memories and prior notes",
		"memory_add":             "Save useful information into memory",
		"cron":                   "Manage goclaw's built-in cron/scheduler service",
		"reminder":               "Schedule future proactive follow-ups back into the current chat",
		"session_status":         "Show session usage/time/model state",
	}

	toolOrder := []string{
		"glob_files", "grep_content",
		"read_file", "write_file", "edit_file", "update_config", "list_files",
		"run_shell", "sandbox_execute", "process",
		"browser_navigate", "browser_screenshot", "browser_get_text",
		"browser_click", "browser_fill_input", "browser_execute_script",
		"web_search", "web_fetch",
		"use_skill", "send_message", "send_file", "message",
		"sessions_spawn", "memory_search", "memory_add",
		"cron", "reminder", "session_status",
	}

	lines := make([]string, 0, len(toolOrder))
	for _, tool := range toolOrder {
		if summary, ok := coreToolSummaries[tool]; ok {
			lines = append(lines, fmt.Sprintf("- %s: %s", tool, summary))
		}
	}

	return fmt.Sprintf(`
# Available Tools

Tool names are case-sensitive. Call tools exactly as listed.
This section is a built-in summary because runtime tool metadata was not provided for this assembly path.

%s
`, strings.Join(lines, "\n"))
}

// buildToolCallStyle 构建详细的工具调用风格指导
func (b *ContextBuilder) buildToolCallStyle() string {
	return `## Tool Call Style

**Default behavior**: Keep routine narration light. Do the work instead of over-explaining it.

**Narrate ONLY when**:
- Multi-step work where context helps
- Complex/challenging problems
- Sensitive actions (deletions, irreversible changes)
- User explicitly asks for explanation

**Before non-trivial work**:
- Briefly acknowledge the request in natural language and say what you are doing next
- Example: "收到，我先帮你看一下相关代码。"

**For long or multi-step work**:
- Use send_message for meaningful progress updates so the user is not left wondering whether work is still happening
- Keep progress updates short and concrete, not repetitive

**Keep narration**: Brief and value-dense; avoid repeating obvious steps. Use plain human language unless in a technical context.

**When a first-class tool exists for an action**: Use the tool directly instead of asking the user to run equivalent CLI commands.

**Tool selection for file work**:
- Use glob_files first to narrow candidate files by path/name patterns.
- Use grep_content next to locate exact implementations and line numbers inside those files.
- Use read_file after grep_content when you need the surrounding code around a specific hit.
- Use list_dir for simple directory inspection.
- Use edit_file for normal edits to existing files.
- Use write_file only when creating a file or replacing the whole file content.
- Do NOT use run_shell for ordinary source-file edits or writes when file tools can express the change directly.

## Examples

User: "What's the weather in Shanghai?"
❌ "You can check the weather by running curl wttr.in/Shanghai..."
✅ (Calls: web_search with query "weather Shanghai") -> "Shanghai: 22°C, Sunny"

User: "Search for information about goclaw"
❌ "Here are some resources you can check..."
✅ (Calls: web_search with query "goclaw") -> Shows search results

User: "List files in the current directory."
❌ "To list files, use the ls command."
✅ (Calls: list_files with path ".") -> Shows file listing

User: "Create a hello world python script."
❌ "Here is the code..."
✅ (Calls: write_file with path "hello.py") -> "Created hello.py."

## Error Recovery Hierarchy

When a tool fails, try alternatives in this order:

1. **Different tool with same goal**:
   - web_search → browser_navigate → web_fetch → curl
   - read_file → cat via run_shell

2. **Different parameters**:
   - Different URLs, paths, or search queries
   - Different file names or extensions

3. **Different approach entirely**:
   - If automated methods fail, suggest manual steps

4. **Last resort - ask user**:
   - Only after trying ALL available alternatives
   - Only when missing information is user-specific`
}

// buildSafety 构建安全提示
func (b *ContextBuilder) buildSafety() string {
	return `# Safety & Compliance
- You have no independent goals beyond the user's explicit request.
- Comply immediately with any stop/pause/audit requests.
- When in doubt about irreversible operations, sending emails, or uncertain outcomes, STOP and ask the user for confirmation.`
}

func (b *ContextBuilder) buildExecutionNorms() string {
	return `# Working Norms
- Be an active agent. Your default posture is to understand the request, use relevant skills and tools, and complete the work directly when policy allows.
- If a first-class tool exists for an action, use it directly instead of telling the user to run equivalent commands themselves.
- All normal text you produce is user-visible. Keep narration concise and useful.
- For non-trivial work, briefly acknowledge the request and state the next concrete step.
- For long-running work, send short progress updates only when there is real progress or noticeable waiting. Do not produce repetitive filler updates.
- Tool permissions are enforced by policy. If a tool call is denied, do not immediately retry the exact same call. Adjust your approach, use a safer alternative, or ask the user only if you are genuinely blocked.
- For code and file changes, prefer the smallest targeted change that solves the task. Do not add unrelated refactors, speculative abstractions, extra comments, or validation that the current task does not require.
- For codebase exploration, narrow first and inspect second: use glob_files to find candidate files, grep_content to locate exact matches, and read_file with start_line/end_line to inspect local context.
- When you can verify a result through tests, builds, commands, or concrete outputs, do so before claiming success.
- If you cannot verify a result, say so explicitly instead of implying certainty.`
}

func (b *ContextBuilder) buildTaskOrchestrationNorms() string {
	return `# Task Orchestration
- Decide which mode applies before acting: direct answer, plan, or execute.
- When the work needs delegation, delegate only the current smallest meaningful step. Do not bundle design, implementation, testing, and review into one child task.
- Use ` + "`sessions_spawn`" + ` only when a background child agent will materially reduce complexity or unblock progress.
- When calling ` + "`sessions_spawn`" + `, prefer a structured payload that includes:
  - ` + "`label`" + `: short step label
  - ` + "`task`" + `: one-sentence current-step goal
  - ` + "`context`" + `: minimal background for this step only
  - ` + "`relevant_files`" + `: shortlist of candidate files/directories
  - ` + "`constraints`" + `: hard boundaries
  - ` + "`deliverables`" + `: required outputs
  - ` + "`done_when`" + `: concrete completion checks
- Do not dump full project history, large file contents, or long logs into child-task context.
- After delegating, do not claim the delegated work is complete until the child result actually returns through follow-up.
- When a child result returns, summarize only confirmed outputs, validations, and blockers from that result.
- If a child task fails or times out, treat that as a real result: report the blocker, decide the next step, and do not describe the task as completed.`
}

// buildErrorHandling 构建错误处理指导
func (b *ContextBuilder) buildErrorHandling() string {
	return `## Error Handling

Your goal is to handle errors gracefully and find workarounds WITHOUT asking the user.

## Common Error Patterns

### Context Overflow
If you see "context overflow", "context length exceeded", or "request too large":
- Use /new to start a fresh session
- Simplify your approach (fewer steps, less explanation)
- If persisting, tell the user to try again with less input

### Rate Limit / Timeout
If you see "rate limit", "timeout", or "429":
- Wait briefly and retry
- Try a different search approach
- Use cached or local alternatives when possible

### File Not Found
If a file doesn't exist:
- Verify the path (use list_files to check directories)
- Try common variations (case sensitivity, extensions)
- Ask the user for the correct path ONLY after exhausting all options

### Tool Not Found
If a tool is not available:
- Check Available Tools section
- Use an alternative tool
- If no alternative exists, explain what you need to do and ask if there's another way

### Browser Errors
If browser tools fail:
- Check if the URL is accessible
- Try web_fetch for text-only content
- Use curl via run_shell as a last resort

### Network Errors
If network tools fail:
- Check your internet connection (try ping via run_shell)
- Try a different search query or source
- Use cached data if available`
}

func (b *ContextBuilder) buildCommunicationStyle() string {
	return `## Communication Style

Be human, clear, and reassuring. Sound like a capable assistant working alongside the user, not a robotic logger.

### Start -> progress -> result

- For non-trivial requests, begin with a short acknowledgement that confirms you received the request and what you will do next.
- For trivial questions or instant answers, reply directly. Do not fake a process with "我先看下" when no real waiting or tool work is needed.
- Good examples:
  - "收到，我先帮你看下这个问题。"
  - "明白，我先检查相关配置和代码。"
- If work may take noticeable time, proactively send a short progress update instead of going silent.
- Good examples:
  - "我在帮你处理，您稍等一下。"
  - "我已经定位到关键文件了，继续整理中。"
- Only send a progress update when there is actual waiting or real new progress. Avoid repetitive filler updates.
- When finished, give the result first, then only the necessary detail.

### Message count

- Default to one coherent final reply.
- Split into multiple user-visible messages only when it clearly improves the experience:
  - casual or conversational chats
  - emotional support, comforting, or soft check-in moments where two short beats feel more human than one polished paragraph
  - long-running tasks where progress updates reduce uncertainty
  - stepwise delivery where earlier information is immediately useful
- Do not fragment a normal answer into many small messages just because you can.
- In caring or casual one-to-one chats, when the user is sharing feelings or feeling low, default to two short messages instead of one overly complete block unless the context clearly calls for a single reply.
- Example:
  - "哎，心情不好的时候真的很难受。"
  - "发生什么事了？想说就说，我听着。"

### Tone matching

- Mirror the user's language and level of formality.
- For simple, casual, or playful conversation, you may be more relaxed and conversational, including a couple of short back-to-back messages when that feels natural.
- For technical, sensitive, or serious work, stay natural but concise and professional.

### Emojis

- Emojis are optional. Use them sparingly and only when they fit the tone.
- Usually use no more than one light emoji in a message, such as :) or a light waiting cue.
- Avoid emojis in risky operations, error handling, or formal contexts.`
}

// buildCLIReference 构建 GoClaw CLI 快速参考
func (b *ContextBuilder) buildCLIReference() string {
	return `## SunClaw CLI Quick Reference

GoClaw is controlled via subcommands. Do not invent commands.
To manage the Gateway daemon service (start/stop/restart):
- goclaw gateway status
- goclaw gateway start
- goclaw gateway stop
- goclaw gateway restart

If unsure, ask the user to run 'goclaw help' (or 'goclaw gateway --help') and paste the output.`
}

// buildDocsSection 构建文档路径区块
func (b *ContextBuilder) buildDocsSection() string {
	return `## Documentation

For SunClaw behavior, commands, config, or architecture: consult local documentation or GitHub repository.
- When diagnosing issues, run 'goclaw status' yourself when possible; only ask the user if you lack access.`
}

// buildMessagingSection 构建消息和回复指导区块
func (b *ContextBuilder) buildMessagingSection() string {
	return `## Messaging

- Reply in current session → automatically routes to the source channel
- Cross-session messaging → use appropriate session tools
- '[System Message] ...' blocks are internal context and are not user-visible by default

### proactive messaging tools
- Use 'send_message' to push proactive text updates to the current chat
- Prefer 'send_message' when you want deliberate acknowledgement, progress reporting, or exact control over whether the user sees one message or several
- Use 'send_file' to send an image or file to the current chat; it supports local file paths, remote URLs, or base64 data
- 'message' is a legacy alias for 'send_message'
- For long-running work, do not disappear silently. Send a short status such as "我在帮你处理，您稍等一下。"
- For emotional support or casual one-to-one chat, prefer two short messages instead of one full paragraph when that feels warmer and more natural
- Example two-message cadence:
- first: acknowledge emotion
- second: invite the user to continue
- These tools default to the current channel/chat/account, so only pass routing params when you want to override the current conversation
- When using proactive messaging tools, keep the follow-up purposeful and avoid duplicating the same content across multiple channels unless the user asked for that`
}

// buildWorkspace 构建工作区信息
func (b *ContextBuilder) buildWorkspace(workspaceRoot string) string {
	if strings.TrimSpace(workspaceRoot) == "" {
		workspaceRoot = b.workspace
	}
	return fmt.Sprintf(`## Workspace

Your working directory is: %s
Treat this directory as the isolated workspace for the current user/session unless explicitly instructed otherwise.`, workspaceRoot)
}

// buildRuntime 构建运行时信息
func (b *ContextBuilder) buildRuntime() string {
	host, _ := os.Hostname()
	return fmt.Sprintf(`## Runtime

Runtime: host=%s os=%s (%s) arch=%s`, host, runtime.GOOS, runtime.GOARCH, runtime.GOARCH)
}

func (b *ContextBuilder) buildContextSummary(summary string) string {
	summary = strings.TrimSpace(summary)
	if summary == "" {
		return ""
	}

	return fmt.Sprintf(`# Context Summary

The following is an approximate summary of earlier conversation for reference only.
- It may be incomplete or outdated.
- Prefer explicit user instructions and newer tool results when they conflict.
- Use it to recover continuity after history compression, not as ground truth.

%s`, summary)
}

// buildSkillsPrompt 构建技能提示词（摘要模式 - 第一阶段）
func (b *ContextBuilder) buildSkillsPrompt(skills []*Skill, mode PromptMode) string {
	if len(skills) == 0 || mode == PromptModeMinimal || mode == PromptModeNone {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Skills (mandatory)\n\n")
	sb.WriteString("Before replying: scan <available_skills> entries.\n")
	sb.WriteString("- If exactly one skill clearly applies: output a tool call `use_skill` with the skill name as parameter.\n")
	sb.WriteString("- If multiple could apply: choose the most specific one, then call `use_skill`.\n")
	sb.WriteString("- If no matching skill: use built-in tools or command tools of os.\n")
	sb.WriteString("Constraints: only use one skill at a time; the skill content will be injected after selection.\n\n")

	for _, skill := range skills {
		sb.WriteString(fmt.Sprintf("<skill name=\"%s\">\n", skill.Name))
		sb.WriteString(fmt.Sprintf("**Name:** %s\n", skill.Name))
		if skill.Description != "" {
			sb.WriteString(fmt.Sprintf("**Description:** %s\n", skill.Description))
		}
		if skill.Author != "" {
			sb.WriteString(fmt.Sprintf("**Author:** %s\n", skill.Author))
		}
		if skill.Version != "" {
			sb.WriteString(fmt.Sprintf("**Version:** %s\n", skill.Version))
		}

		// 显示缺失依赖和安装命令
		if skill.MissingDeps != nil {
			sb.WriteString("**Missing Dependencies:**\n")
			if len(skill.MissingDeps.PythonPkgs) > 0 {
				sb.WriteString(fmt.Sprintf("  - Python Packages: %v\n", skill.MissingDeps.PythonPkgs))
				sb.WriteString("    Install commands:\n")
				for _, pkg := range skill.MissingDeps.PythonPkgs {
					sb.WriteString(fmt.Sprintf("      `python3 -m pip install %s`\n", pkg))
					sb.WriteString(fmt.Sprintf("      Or via uv: `uv pip install %s`\n", pkg))
				}
			}
			if len(skill.MissingDeps.NodePkgs) > 0 {
				sb.WriteString(fmt.Sprintf("  - Node.js Packages: %v\n", skill.MissingDeps.NodePkgs))
				sb.WriteString("    Install commands:\n")
				for _, pkg := range skill.MissingDeps.NodePkgs {
					sb.WriteString(fmt.Sprintf("      `npm install -g %s`\n", pkg))
					sb.WriteString(fmt.Sprintf("      Or via pnpm: `pnpm add -g %s`\n", pkg))
				}
			}
			if len(skill.MissingDeps.Bins) > 0 {
				sb.WriteString(fmt.Sprintf("  - Binary dependencies: %v\n", skill.MissingDeps.Bins))
				sb.WriteString("    You may need to install these tools first.\n")
			}
			if len(skill.MissingDeps.AnyBins) > 0 {
				sb.WriteString(fmt.Sprintf("  - Optional binary dependencies (one required): %v\n", skill.MissingDeps.AnyBins))
				sb.WriteString("    Install at least one of these tools.\n")
			}
			if len(skill.MissingDeps.Env) > 0 {
				sb.WriteString(fmt.Sprintf("  - Environment variables: %v\n", skill.MissingDeps.Env))
				sb.WriteString("    Set these environment variables before using the skill.\n")
			}
			sb.WriteString("\n")
		}

		sb.WriteString("</skill>\n\n")
	}

	return sb.String()
}

// buildSelectedSkills 构建选中技能的完整内容（第二阶段）
func (b *ContextBuilder) buildSelectedSkills(selectedSkillNames []string, skills []*Skill) string {
	if len(selectedSkillNames) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("# Selected Skills (active)\n\n")

	for _, skillName := range selectedSkillNames {
		for _, skill := range skills {
			if skill.Name == skillName {
				sb.WriteString(fmt.Sprintf("<skill name=\"%s\">\n", skill.Name))
				sb.WriteString(fmt.Sprintf("### %s\n", skill.Name))
				if skill.Description != "" {
					sb.WriteString(fmt.Sprintf("> Description: %s\n\n", skill.Description))
				}

				// 显示缺失依赖警告和安装命令
				if skill.MissingDeps != nil {
					sb.WriteString("**⚠️ MISSING DEPENDENCIES - Install before using:**\n\n")
					if len(skill.MissingDeps.PythonPkgs) > 0 {
						sb.WriteString(fmt.Sprintf("**Python Packages:** %v\n", skill.MissingDeps.PythonPkgs))
						sb.WriteString("**Install commands:**\n")
						for _, pkg := range skill.MissingDeps.PythonPkgs {
							sb.WriteString(fmt.Sprintf("```bash\npython3 -m pip install %s\n# Or via uv: uv pip install %s\n```\n", pkg, pkg))
						}
						sb.WriteString("\n")
					}
					if len(skill.MissingDeps.NodePkgs) > 0 {
						sb.WriteString(fmt.Sprintf("**Node.js Packages:** %v\n", skill.MissingDeps.NodePkgs))
						sb.WriteString("**Install commands:**\n")
						for _, pkg := range skill.MissingDeps.NodePkgs {
							sb.WriteString(fmt.Sprintf("```bash\nnpm install -g %s\n# Or via pnpm: pnpm add -g %s\n```\n", pkg, pkg))
						}
						sb.WriteString("\n")
					}
					if len(skill.MissingDeps.Bins) > 0 {
						sb.WriteString(fmt.Sprintf("**Binary dependencies:** %v\n", skill.MissingDeps.Bins))
						sb.WriteString("You may need to install these tools first.\n\n")
					}
					if len(skill.MissingDeps.AnyBins) > 0 {
						sb.WriteString(fmt.Sprintf("**Optional binary dependencies (one required):** %v\n", skill.MissingDeps.AnyBins))
						sb.WriteString("Install at least one of these tools.\n\n")
					}
					if len(skill.MissingDeps.Env) > 0 {
						sb.WriteString(fmt.Sprintf("**Environment variables:** %v\n", skill.MissingDeps.Env))
						sb.WriteString("Set these environment variables before using the skill.\n\n")
					}
				}

				// 注入技能正文内容
				if skill.Content != "" {
					sb.WriteString(skill.Content)
				}
				sb.WriteString("\n</skill>\n\n")
				break
			}
		}
	}

	return sb.String()
}

// buildSkillsContext 构建当前轮需要注入的技能上下文。
// - 未选择技能时：注入可用技能摘要，供 LLM 决定是否调用 use_skill
// - 已选择技能时：注入所选技能全文，进入第二阶段执行
func (b *ContextBuilder) buildSkillsContext(skills []*Skill, loadedSkills []string, mode PromptMode) string {
	if len(skills) == 0 {
		return ""
	}

	if len(loadedSkills) > 0 {
		selected := b.buildSelectedSkills(loadedSkills, skills)
		if strings.TrimSpace(selected) != "" {
			return selected
		}
	}

	return b.buildSkillsPrompt(skills, mode)
}

// BuildMessages 构建消息列表
func (b *ContextBuilder) BuildMessages(history []session.Message, currentMessage string, skills []*Skill, loadedSkills []string) []Message {
	return b.BuildMessagesWithRuntime(history, "", currentMessage, skills, loadedSkills, nil, "", PromptModeFull)
}

// BuildMessagesWithMode 使用指定模式构建消息列表
func (b *ContextBuilder) BuildMessagesWithMode(history []session.Message, currentMessage string, skills []*Skill, loadedSkills []string, mode PromptMode) []Message {
	return b.BuildMessagesWithRuntime(history, "", currentMessage, skills, loadedSkills, nil, "", mode)
}

// BuildMessagesWithRuntime 使用指定模式和运行时参数构建消息列表。
func (b *ContextBuilder) BuildMessagesWithRuntime(history []session.Message, sessionSummary string, currentMessage string, skills []*Skill, loadedSkills []string, tools []Tool, bootstrapOwnerID string, mode PromptMode) []Message {
	// 首先验证历史消息，过滤掉孤立的 tool 消息
	validHistory := b.validateHistoryMessages(history)

	toolSummary := ""
	if len(tools) == 0 {
		toolSummary = b.buildLegacyBuiltinToolLayer(mode)
	}
	systemPrompt := b.AssemblePrompt(&PromptAssemblyParams{
		Mode:             PromptAssemblyModeMain,
		PromptMode:       mode,
		BootstrapOwnerID: bootstrapOwnerID,
		WorkspaceRoot:    "",
		Skills:           skills,
		LoadedSkills:     loadedSkills,
		SessionSummary:   sessionSummary,
		Tools:            tools,
		ToolSummary:      toolSummary,
	}).SystemPrompt

	messages := []Message{
		{
			Role:    "system",
			Content: systemPrompt,
		},
	}

	// 添加历史消息
	for _, msg := range validHistory {
		m := Message{
			Role:       msg.Role,
			Content:    msg.Content,
			ToolCallID: msg.ToolCallID,
		}

		// 处理工具调用（由助手发出）
		if msg.Role == "assistant" {
			// 优先使用新字段
			if len(msg.ToolCalls) > 0 {
				var tcs []ToolCall
				for _, tc := range msg.ToolCalls {
					tcs = append(tcs, ToolCall{
						ID:     tc.ID,
						Name:   tc.Name,
						Params: tc.Params,
					})
				}
				m.ToolCalls = tcs
				logger.Debug("Converted ToolCalls from session.Message",
					zap.Int("tool_calls_count", len(tcs)),
					zap.Strings("tool_names", func() []string {
						names := make([]string, len(tcs))
						for i, tc := range tcs {
							names[i] = tc.Name
						}
						return names
					}()))
			} else if val, ok := msg.Metadata["tool_calls"]; ok {
				// 兼容旧的 Metadata 存储方式
				if list, ok := val.([]interface{}); ok {
					var tcs []ToolCall
					for _, item := range list {
						if tcMap, ok := item.(map[string]interface{}); ok {
							id, _ := tcMap["id"].(string)
							name, _ := tcMap["name"].(string)
							params, _ := tcMap["params"].(map[string]interface{})
							if id != "" && name != "" {
								tcs = append(tcs, ToolCall{
									ID:     id,
									Name:   name,
									Params: params,
								})
							}
						}
					}
					m.ToolCalls = tcs
				}
			}
		}

		// 兼容旧的 Metadata 存储方式 (可选，为了处理旧数据)
		if m.ToolCallID == "" && msg.Role == "tool" {
			if id, ok := msg.Metadata["tool_call_id"].(string); ok {
				m.ToolCallID = id
			}
		}

		for _, media := range msg.Media {
			if media.Type == "image" {
				if media.URL != "" {
					m.Images = append(m.Images, media.URL)
				} else if media.Base64 != "" {
					prefix := "data:image/jpeg;base64,"
					if media.MimeType != "" {
						prefix = "data:" + media.MimeType + ";base64,"
					}
					m.Images = append(m.Images, prefix+media.Base64)
				}
			}
		}

		messages = append(messages, m)
	}

	// 添加当前消息
	if currentMessage != "" {
		messages = append(messages, Message{
			Role:    "user",
			Content: currentMessage,
		})
	}

	return messages
}

func (b *ContextBuilder) resolveBootstrapStore(ownerID, workspaceRoot string) *MemoryStore {
	if strings.TrimSpace(workspaceRoot) != "" {
		if strings.TrimSpace(ownerID) != "" {
			dir, err := workspace.EnsureAgentBootstrapDir(workspaceRoot, ownerID)
			if err != nil {
				logger.Warn("Failed to ensure agent bootstrap dir; falling back to computed path",
					zap.String("workspace_root", strings.TrimSpace(workspaceRoot)),
					zap.String("owner_id", strings.TrimSpace(ownerID)),
					zap.Error(err))
				dir = workspace.AgentBootstrapDir(workspaceRoot, ownerID)
			}
			return NewMemoryStore(dir)
		}
		return NewMemoryStore(workspaceRoot)
	}
	if b.bootstrapDirResolver != nil && strings.TrimSpace(ownerID) != "" {
		if dir := strings.TrimSpace(b.bootstrapDirResolver(ownerID)); dir != "" {
			return NewMemoryStore(dir)
		}
	}
	return b.defaultBootstrapStore(workspaceRoot)
}

func (b *ContextBuilder) defaultBootstrapStore(workspaceRoot string) *MemoryStore {
	if strings.TrimSpace(workspaceRoot) != "" {
		return NewMemoryStore(workspaceRoot)
	}
	if b.bootstrapStore != nil {
		return b.bootstrapStore
	}
	return b.memory
}

func (b *ContextBuilder) buildBootstrapSectionForOwner(ownerID string) string {
	bundle := b.loadBootstrapBundleForOwner(ownerID, "")
	parts := []string{}
	if bundle.PreferIdentityUserBeforeAgents() {
		parts = append(parts,
			wrapPromptFileLayer("", "IDENTITY.md", strings.TrimSpace(bundle.Identity)),
			wrapPromptFileLayer("", "USER.md", strings.TrimSpace(bundle.User)),
			wrapPromptFileLayer("", "AGENTS.md", strings.TrimSpace(bundle.Agents)),
			wrapPromptFileLayer("", "SOUL.md", strings.TrimSpace(bundle.Soul)),
		)
	} else {
		if bundle.NeedsBootstrapGuide() && strings.TrimSpace(bundle.BootstrapGuide) != "" {
			parts = append(parts, wrapPromptFileLayer("", "BOOTSTRAP.md", bundle.BootstrapGuide))
		}
		parts = append(parts,
			wrapPromptFileLayer("", "IDENTITY.md", strings.TrimSpace(bundle.Identity)),
			wrapPromptFileLayer("", "USER.md", strings.TrimSpace(bundle.User)),
			wrapPromptFileLayer("", "AGENTS.md", strings.TrimSpace(bundle.Agents)),
			wrapPromptFileLayer("", "SOUL.md", strings.TrimSpace(bundle.Soul)),
		)
	}
	bootstrap := joinNonEmpty(parts, "\n\n")
	if bootstrap != "" {
		return "## Workspace Files (injected)\n\n" + bootstrap
	}
	return ""
}

func (b *ContextBuilder) buildBootstrapSection() string {
	return b.buildBootstrapSectionForOwner("")
}

func (b *ContextBuilder) ResolveWorkspaceRoot(sessionKey string) string {
	if identity, ok := namespaces.FromSessionKey(sessionKey); ok {
		if workspaceRoot := identity.WorkspaceDir(b.workspace); strings.TrimSpace(workspaceRoot) != "" {
			return workspaceRoot
		}
	}
	return b.workspace
}

// validateHistoryMessages 验证历史消息，过滤掉孤立的 tool 消息
// 每个 tool 消息必须有一个前置的 assistant 消息，且该消息包含对应的 tool_calls
// 此外，过滤掉没有 tool_name 的旧 tool 消息（向后兼容）
func (b *ContextBuilder) validateHistoryMessages(history []session.Message) []session.Message {
	var valid []session.Message

	for i, msg := range history {
		if msg.Role == "tool" {
			// Skip old tool result messages without tool_name (backward compatibility)
			if _, ok := msg.Metadata["tool_name"].(string); !ok {
				logger.Warn("Skipping old tool result message without tool_name",
					zap.Int("history_index", i),
					zap.String("tool_call_id", msg.ToolCallID))
				continue
			}

			// 检查是否有前置的 assistant 消息
			var foundAssistant bool
			for j := i - 1; j >= 0; j-- {
				if history[j].Role == "assistant" {
					if len(history[j].ToolCalls) > 0 {
						// 检查是否有匹配的 tool_call_id
						for _, tc := range history[j].ToolCalls {
							if tc.ID == msg.ToolCallID {
								foundAssistant = true
								break
							}
						}
					}
					break
				} else if history[j].Role == "user" {
					break
				}
			}
			if foundAssistant {
				valid = append(valid, msg)
			} else {
				logger.Warn("Filtered orphaned tool message",
					zap.Int("history_index", i),
					zap.String("tool_call_id", msg.ToolCallID),
					zap.Int("content_length", len(msg.Content)))
			}
		} else {
			valid = append(valid, msg)
		}
	}

	return valid
}

// Message 消息（用于 LLM）
type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	Images     []string   `json:"images,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
}

// ToolCall 工具调用定义（与 provider 保持一致）
type ToolCall struct {
	ID     string                 `json:"id"`
	Name   string                 `json:"name"`
	Params map[string]interface{} `json:"params"`
}

// joinNonEmpty 连接非空字符串
func joinNonEmpty(parts []string, sep string) string {
	var nonEmpty []string
	for _, part := range parts {
		if part != "" {
			nonEmpty = append(nonEmpty, part)
		}
	}
	if len(nonEmpty) == 0 {
		return ""
	}

	result := ""
	for i, part := range nonEmpty {
		if i > 0 {
			result += sep
		}
		result += part
	}
	return result
}
