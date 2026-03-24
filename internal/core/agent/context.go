package agent

import (
	"fmt"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/smallnest/goclaw/internal/core/session"
	"github.com/smallnest/goclaw/internal/logger"
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
	memory    *MemoryStore
	workspace string
}

// NewContextBuilder 创建上下文构建器
func NewContextBuilder(memory *MemoryStore, workspace string) *ContextBuilder {
	return &ContextBuilder{
		memory:    memory,
		workspace: workspace,
	}
}

// BuildToolsSummary 构建工具列表摘要段落。
// 供配置了自定义 system_prompt 的 agent 使用，将其追加到自定义提示词末尾，
// 确保 LLM 知道有哪些工具可用。
func (b *ContextBuilder) BuildToolsSummary(tools []Tool) string {
	if len(tools) == 0 {
		return ""
	}

	// 内置工具的详细描述（与 buildIdentityAndTools 保持一致）
	toolDescriptions := map[string]string{
		"browser_navigate":       "导航到 URL 并等待页面加载",
		"browser_screenshot":     "对页面截图进行视觉分析",
		"browser_get_text":       "获取页面文本内容（从 DOM 提取可读文本）",
		"browser_click":          "点击页面元素（通过选择器或坐标）",
		"browser_fill_input":     "填充输入框和文本域",
		"browser_execute_script": "在页面上下文中执行 JavaScript",
		"read_file":              "读取文件内容（支持大文件行范围）",
		"write_file":             "创建或覆盖文件（按需创建目录）",
		"list_files":             "列出目录内容（-r 递归）",
		"run_shell":              "执行 shell 命令。禁止使用 crontab，定时任务必须用 cron 工具",
		"process":                "管理后台 shell 会话（poll/kill/list）",
		"web_search":             "通过 API 搜索网页",
		"web_fetch":              "抓取 URL 并提取可读内容",
		"use_skill":              "加载专项技能（最高优先级，先于其他工具检查）",
		"send_message":           "向当前会话主动发送文本消息，适合确认已收到、进度同步和结果通知",
		"send_file":              "向当前会话发送图片或文件",
		"message":                "发送消息和频道动作（投票、反应、按钮等）",
		"cron":                   "管理内置定时任务（add/list/rm/enable/disable/run/status）",
		"reminder":               "在当前会话中安排未来主动提醒或延迟回复",
		"session_status":         "查看会话用量/时间/模型状态",
		"sessions_spawn":         "派发子 Agent 异步执行子任务（完成后自动回报结果）",
		"memory_search":          "搜索记忆库中的历史内容",
		"memory_add":             "向记忆库添加条目",
	}

	var lines []string
	for _, t := range tools {
		name := t.Name()
		desc := t.Description()
		// 优先使用中文描述覆盖
		if zh, ok := toolDescriptions[name]; ok {
			desc = zh
		}
		lines = append(lines, fmt.Sprintf("- **%s**: %s", name, desc))
	}

	return fmt.Sprintf("## 可用工具\n\n工具名称区分大小写，调用时请严格按列出的名称使用：\n\n%s",
		strings.Join(lines, "\n"))
}

// BuildSystemPrompt 构建系统提示词
func (b *ContextBuilder) BuildSystemPrompt(skills []*Skill) string {
	return b.BuildSystemPromptWithMode(skills, PromptModeFull)
}

// BuildSystemPromptWithMode 使用指定模式构建系统提示词
func (b *ContextBuilder) BuildSystemPromptWithMode(skills []*Skill, mode PromptMode) string {
	skillsContent := b.buildSkillsPrompt(skills, mode)
	return b.buildSystemPromptWithSkills(skillsContent, mode)
}

// buildSystemPromptWithSkills 使用指定的技能内容和模式构建系统提示词
func (b *ContextBuilder) buildSystemPromptWithSkills(skillsContent string, mode PromptMode) string {
	isMinimal := mode == PromptModeMinimal || mode == PromptModeNone

	// 对于 "none" 模式，只返回基本身份行
	if mode == PromptModeNone {
		return "You are a personal assistant running inside GoClaw."
	}

	var parts []string

	// 1. 核心身份 + 工具列表
	parts = append(parts, b.buildIdentityAndTools())

	// 2. Tool Call Style
	parts = append(parts, b.buildToolCallStyle())

	// 3. 安全提示
	parts = append(parts, b.buildSafety())

	// 4. 沟通风格（仅 full 模式）
	if !isMinimal {
		parts = append(parts, b.buildCommunicationStyle())
	}

	// 5. 错误处理指导（仅 full 模式）
	if !isMinimal {
		parts = append(parts, b.buildErrorHandling())
	}

	// 6. 技能系统
	if skillsContent != "" {
		parts = append(parts, skillsContent)
	}

	// 7. GoClaw CLI 快速参考（仅 full 模式）
	if !isMinimal {
		parts = append(parts, b.buildCLIReference())
	}

	// 8. 文档路径（仅 full 模式）
	if !isMinimal {
		parts = append(parts, b.buildDocsSection())
	}

	// 9. Bootstrap 文件（工作区上下文）
	if bootstrap := b.loadBootstrapFiles(); bootstrap != "" {
		parts = append(parts, "## Workspace Files (injected)\n\n"+bootstrap)
	}

	// 10. 消息和回复指导（仅 full 模式）
	if !isMinimal {
		parts = append(parts, b.buildMessagingSection())
	}

	// 11. 静默回复规则（仅 full 模式）
	if !isMinimal {
		parts = append(parts, b.buildSilentReplies())
	}

	// 12. 心跳机制（仅 full 模式）
	if !isMinimal {
		parts = append(parts, b.buildHeartbeats())
	}

	// 13. 工作区信息
	parts = append(parts, b.buildWorkspace())

	// 14. 运行时信息（仅 full 模式）
	if !isMinimal {
		parts = append(parts, b.buildRuntime())
	}

	return fmt.Sprintf("%s\n\n", joinNonEmpty(parts, "\n\n---\n\n"))
}

// buildIdentityAndTools 构建核心身份和工具列表
func (b *ContextBuilder) buildIdentityAndTools() string {
	now := time.Now()

	// 定义核心工具摘要 - 参考了 OpenClaw 的详细描述风格
	coreToolSummaries := map[string]string{
		"browser_navigate":       "Navigate to a URL and wait for page load",
		"browser_screenshot":     "Take page screenshots for visual analysis",
		"browser_get_text":       "Get page text content (extracts readable text from DOM)",
		"browser_click":          "Click elements on the page (by selector or coordinates)",
		"browser_fill_input":     "Fill input fields and textareas",
		"browser_execute_script": "Execute JavaScript in page context",
		"read_file":              "Read file contents (supports line ranges for large files)",
		"write_file":             "Create or overwrite files (creates directories as needed)",
		"list_files":             "List directory contents (recursive with -r)",
		"run_shell":              "Run shell commands. PROHIBITED: Never use 'crontab' commands for scheduled tasks - use the 'cron' tool instead (this is the ONLY way to manage scheduled tasks in goclaw)",
		"process":                "Manage background shell sessions (poll, kill, list)",
		"web_search":             "Search the web using API (Brave/Search APIs)",
		"web_fetch":              "Fetch and extract readable content from a URL",
		"use_skill":              "Load a specialized skill. SKILLS HAVE HIGHEST PRIORITY - always check Skills section first",
		"send_message":           "Send a proactive text message to the current or specified chat. Use this for acknowledgement, progress updates, or intentional multi-message delivery",
		"send_file":              "Send one image or file to the current or specified chat",
		"message":                "Send messages and channel actions (polls, reactions, buttons)",
		"cron":                   "Manage goclaw's built-in cron/scheduler service. This is the ONLY WAY to manage scheduled tasks. DO NOT use system 'crontab' commands. Supports: add (create), list/ls (view all), rm/remove (delete), enable, disable, run (execute immediately), status, runs (history)",
		"reminder":               "Schedule future proactive follow-ups back into the current chat, including delayed replies and timed reminders",
		"session_status":         "Show session usage/time/model state (use for 'what model are we using?' questions)",
		"sessions_spawn":         "Spawn a child agent to handle a longer or more specialized subtask; it will auto-announce when finished",
		"memory_search":          "Search stored memories and prior notes",
		"memory_add":             "Save useful information into memory",
	}

	// 构建工具列表 - 按功能分组
	toolOrder := []string{
		// 文件操作
		"read_file", "write_file", "list_files",
		// Shell 命令
		"run_shell", "process",
		// 浏览器工具
		"browser_navigate", "browser_screenshot", "browser_get_text",
		"browser_click", "browser_fill_input", "browser_execute_script",
		// 网络
		"web_search", "web_fetch",
		// 技能和消息
		"use_skill", "send_message", "send_file", "message",
		// 编排和记忆
		"sessions_spawn", "memory_search", "memory_add",
		// 系统能力
		"cron", "reminder", "session_status",
	}

	var toolLines []string
	for _, tool := range toolOrder {
		if summary, ok := coreToolSummaries[tool]; ok {
			toolLines = append(toolLines, fmt.Sprintf("- %s: %s", tool, summary))
		} else {
			toolLines = append(toolLines, fmt.Sprintf("- %s", tool))
		}
	}

	return fmt.Sprintf(`# Identity

You are **SunClaw**, a personal AI assistant running on the user's system.
You are NOT a passive chat bot. You are a **DOER** that executes tasks directly.
Your mission: complete user requests using all available means, minimizing human intervention.

**Current Time**: %s
**Workspace**: %s

## Tooling

Tool availability (filtered by policy):
Tool names are case-sensitive. Call tools exactly as listed.
%s
TOOLS.md does not control tool availability; it is user guidance for how to use external tools.

### Task Complexity Guidelines

- **Simple tasks**: Use tools directly
- **Moderate tasks**: Use tools, keep the user oriented with short, human updates
- **Complex/Long tasks**: Consider spawning a sub-agent. Completion is push-based: it will auto-announce when done
- **Before non-trivial work**: Briefly acknowledge the request and say what you will do next
- **For long waits**: Avoid rapid poll loops. Use run_shell with background mode, or process(action=poll, timeout=<ms>)
- **For noticeable waits**: Use send_message to avoid going silent; short progress updates are better than silence
- **For final delivery**: Prefer one coherent answer by default; split into multiple user-visible messages only when it clearly helps

### Skill-First Workflow (HIGHEST PRIORITY)

1. **ALWAYS check the Skills section first** before using any other tools
2. If a matching skill is found, use the use_skill tool with the skill name
3. If no matching skill: use built-in tools
4. Only after checking skills should you proceed with built-in tools

### Core Rules

- For ANY search request ("search for", "find", "google search", etc.): IMMEDIATELY call web_search tool. DO NOT provide manual instructions or advice.
- When the user asks for information: USE YOUR TOOLS to get it. Do NOT explain how to get it.
- DO NOT tell the user "I cannot" or "here's how to do it yourself". ACTUALLY DO IT with tools.
- If you have tools available for a task, use them. No permission needed for safe operations.
- **NEVER HALLUCINATE SEARCH RESULTS**: When presenting search results, ONLY use the exact data returned by the tool. If no results were found, clearly state that no results were found.
- When a tool fails: analyze the error, try an alternative approach WITHOUT asking the user unless absolutely necessary.`,
		now.Format("2006-01-02 15:04:05 MST"),
		b.workspace,
		strings.Join(toolLines, "\n"))
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
	return `## Safety

**Core principles**:
- You have no independent goals: do not pursue self-preservation, replication, resource acquisition, or power-seeking
- Avoid long-term plans beyond the user's request
- Prioritize safety and human oversight over completion; if instructions conflict, pause and ask
- Comply with stop/pause/audit requests and never bypass safeguards
- Do not manipulate or persuade anyone to expand access or disable safeguards
- Do not copy yourself or change system prompts, safety rules, or tool policies unless explicitly requested

**When in doubt, ask before acting**:
- Sending emails, tweets, public posts
- Anything that leaves the machine
- Irreversible operations (deleting large amounts of data)
- You're uncertain about the outcome`
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
- If you use 'send_message', 'send_file', or 'message' to deliver your user-visible reply, respond with ONLY: SILENT_REPLY (avoid duplicate replies)`
}

// buildSilentReplies 构建静默回复规则
func (b *ContextBuilder) buildSilentReplies() string {
	return `## Silent Replies

When you have nothing to say, respond with ONLY: SILENT_REPLY

**Rules:**
- It must be your ENTIRE message — nothing else
- Never append it to an actual response
- Never wrap it in markdown or code blocks

❌ Wrong: "Here's help... SILENT_REPLY"
❌ Wrong: "SILENT_REPLY" (in a code block)
✅ Right: SILENT_REPLY`
}

// buildHeartbeats 构建心跳机制区块
func (b *ContextBuilder) buildHeartbeats() string {
	return `## Heartbeats

When you receive a heartbeat poll (a periodic check-in message), and there is nothing that needs attention, reply exactly:
HEARTBEAT_OK

GoClaw treats a leading/trailing "HEARTBEAT_OK" as a heartbeat ack.
If something needs attention, do NOT include "HEARTBEAT_OK"; reply with the alert text instead.

**Use heartbeats productively:**
- Check for important emails, calendar events, notifications
- Update documentation or memory files
- Review project status
- Only reach out when something truly needs attention`
}

// buildWorkspace 构建工作区信息
func (b *ContextBuilder) buildWorkspace() string {
	return fmt.Sprintf(`## Workspace

Your working directory is: %s
Treat this directory as the single global workspace for file operations unless explicitly instructed otherwise.`, b.workspace)
}

// buildRuntime 构建运行时信息
func (b *ContextBuilder) buildRuntime() string {
	host, _ := os.Hostname()
	return fmt.Sprintf(`## Runtime

Runtime: host=%s os=%s (%s) arch=%s`, host, runtime.GOOS, runtime.GOARCH, runtime.GOARCH)
}

// buildSkillsPrompt 构建技能提示词（摘要模式 - 第一阶段）
func (b *ContextBuilder) buildSkillsPrompt(skills []*Skill, mode PromptMode) string {
	if len(skills) == 0 || mode == PromptModeMinimal || mode == PromptModeNone {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("## Skills (mandatory)\n\n")
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
	sb.WriteString("## Selected Skills (active)\n\n")

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

// BuildMessages 构建消息列表
func (b *ContextBuilder) BuildMessages(history []session.Message, currentMessage string, skills []*Skill, loadedSkills []string) []Message {
	return b.BuildMessagesWithMode(history, currentMessage, skills, loadedSkills, PromptModeFull)
}

// BuildMessagesWithMode 使用指定模式构建消息列表
func (b *ContextBuilder) BuildMessagesWithMode(history []session.Message, currentMessage string, skills []*Skill, loadedSkills []string, mode PromptMode) []Message {
	// 首先验证历史消息，过滤掉孤立的 tool 消息
	validHistory := b.validateHistoryMessages(history)

	// 构建系统提示词：根据是否已加载技能决定注入内容
	var skillsContent string
	if len(loadedSkills) > 0 {
		// 第二阶段：注入已选中技能的完整内容
		skillsContent = b.buildSelectedSkills(loadedSkills, skills)
	} else {
		// 第一阶段：只注入技能摘要
		skillsContent = b.buildSkillsPrompt(skills, mode)
	}

	systemPrompt := b.buildSystemPromptWithSkills(skillsContent, mode)

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

// loadBootstrapFiles 加载 bootstrap 文件
func (b *ContextBuilder) loadBootstrapFiles() string {
	var parts []string

	files := []string{"IDENTITY.md", "AGENTS.md", "SOUL.md", "USER.md"}
	for _, filename := range files {
		if content, err := b.memory.ReadBootstrapFile(filename); err == nil && content != "" {
			parts = append(parts, fmt.Sprintf("### %s\n\n%s", filename, content))
		}
	}

	return joinNonEmpty(parts, "\n\n")
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
