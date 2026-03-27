# SunClaw 提示词分层装配与拼接逻辑设计

这份文档写给实现这套分层架构的开发者。

目标是把提示词装配逻辑从“分散在多处的字符串追加”重构为**单入口、可测试、带回退规则的层级装配器**。

本文描述的是**目标实现逻辑**，用于指导后续代码修改；它不是当前仓库所有代码路径的现状快照。

## 1. 设计目标

我们要解决 4 个问题：

1. 明确每一层的职责和优先级
2. 明确哪些层是固定层，哪些层是动态层
3. 明确主 Agent / 子 Agent 在什么情况下走什么回退逻辑
4. 保证工具权限由结构化代码控制，而不是仅靠文本提示

## 2. 逻辑层级与优先级

目标顺序如下：

1. 系统内置提示词（定义边界）
2. `SOUL.md`
3. `IDENTITY.md`
4. 主 Agent 自定义提示词
5. 子 Agent 描述
6. 技能
7. 工具
8. 上下文
9. 用户输入

实现上应理解为两类内容：

- `system prompt layers`
  由第 1 到第 8 层组成
- `conversation messages`
  历史消息、工具结果、当前用户输入

其中第 9 层“用户输入”不应拼进 system prompt 文本本身，而应以用户消息形式附在 system prompt 之后。

## 3. 系统内置提示词拆分

系统内置提示词必须拆成两个逻辑片段：

### 3.1 `builtin_boundary`

职责：

- 安全边界
- 输出协议
- 工具调用协议
- 永不变化的底层约束

特点：

- 永远存在
- 不受主 Agent 是否有自定义提示词影响

### 3.2 `builtin_generic_core`

职责：

- 系统通用认知
- 当主 Agent 没有专属自定义提示词时的通用工作方式

特点：

- 只有在主 Agent 的自定义提示词为空时，才作为第 4 层注入
- 如果主 Agent 已配置自定义提示词，则这一层完全不注入

也就是说：

- `builtin_boundary` 是固定边界层
- `builtin_generic_core` 是“主 Agent 自定义提示词槽位”的默认值

## 4. 输入源定义

推荐把装配输入统一收敛成一个结构体。

```go
type PromptAssemblyInput struct {
    Mode                PromptMode // main | subagent
    AgentID             string
    BootstrapOwnerID    string

    BuiltinBoundary     string
    BuiltinGenericCore  string

    Soul                string
    Identity            string
    AgentsFile          string
    UserFile            string

    AgentCustomPrompt   string

    SpawnableAgentCatalog string
    SubagentDescriptor    string
    CurrentTask           string

    SkillsSummary       string
    SelectedSkillBodies []string

    ToolSummary         string
    ToolDefinitions     []providers.ToolDefinition

    ContextBlocks       []string
    HistoryMessages     []providers.Message
    CurrentUserInput    string
}
```

这里要注意：

- `AGENTS.md` 是稳定协作知识
- `SubagentDescriptor` 是当前这次运行时生成的子 Agent 描述
- `ToolDefinitions` 是结构化工具定义，不属于纯文本层

## 5. 回退规则

### 5.1 主 Agent 自定义提示词回退

规则如下：

1. 如果 `AgentCustomPrompt` 非空：
   - 第 4 层直接使用 `AgentCustomPrompt`
   - 不注入 `BuiltinGenericCore`
2. 如果 `AgentCustomPrompt` 为空：
   - 第 4 层使用 `BuiltinGenericCore`

伪代码：

```go
func resolveAgentCore(in PromptAssemblyInput) string {
    if strings.TrimSpace(in.AgentCustomPrompt) != "" {
        return in.AgentCustomPrompt
    }
    return in.BuiltinGenericCore
}
```

### 5.2 子 Agent 层回退

规则如下：

1. 主 Agent 模式：
   - 第 5 层放动态生成的可派生 Agent 目录
   - 可选追加当前运行态的协作说明
2. 子 Agent 模式：
   - 第 5 层放目标子 Agent 描述和当前任务约束
3. 如果没有子 Agent 场景：
   - 第 5 层为空

### 5.3 技能层回退

规则如下：

1. 如果当前未选中技能：
   - 第 6 层注入技能摘要
2. 如果当前已选中技能：
   - 第 6 层注入选中技能正文
   - 不再保留所有技能摘要

### 5.4 上下文层回退

上下文层允许为空。

如果 `USER.md`、环境信息、历史摘要都为空：

- 第 8 层直接省略
- 不能因此回退去覆盖前面的认知层

## 6. `SOUL.md` / `IDENTITY.md` / `AGENTS.md` / `USER.md` 的归位

推荐归位如下：

- `SOUL.md`：第 2 层
- `IDENTITY.md`：第 3 层
- `AGENTS.md`：第 5 层的稳定协作部分
- `USER.md`：第 8 层

这里要刻意避免当前很多项目常见的混用方式：

- 不要把 `USER.md` 提前到人格层
- 不要把 `AGENTS.md` 混成 identity
- 不要把 `SOUL.md` 和 `IDENTITY.md` 合并成一个大文件后失去职责边界

## 7. 主 Agent 与子 Agent 的装配差异

### 7.1 主 Agent 装配

目标顺序：

```text
builtin_boundary
-> SOUL.md
-> IDENTITY.md
-> agent_custom_prompt or builtin_generic_core
-> AGENTS.md + spawnable_agent_catalog
-> skills
-> tool_summary
-> USER.md + runtime_context
-> history_messages
-> current_user_input
```

说明：

- 主 Agent 需要看到可派生 Agent 目录
- 主 Agent 需要看到长期协作知识
- 主 Agent 的第 4 层必须走“自定义 prompt 优先，否则通用认知回退”

### 7.2 子 Agent 装配

目标顺序：

```text
builtin_boundary
-> target SOUL.md
-> target IDENTITY.md
-> target agent custom prompt or builtin_generic_core
-> subagent_descriptor + current_task
-> selected_skills_or_summary
-> filtered_tool_summary
-> minimal context
-> task as user message
```

关键点：

- 子 Agent 不应默认继承主 Agent 的长期身份文本
- 子 Agent 要使用目标 Agent 自己的 `SOUL.md` 和 `IDENTITY.md`
- 子 Agent 可以继承主 Agent 的任务上下文，但不应继承主 Agent 的长期角色认知

## 8. 工具层的处理原则

工具层必须分成两部分：

1. 文本工具摘要
2. 结构化工具定义

### 8.1 文本工具摘要

作用：

- 帮助模型理解工具用途
- 让模型知道有哪些能力、优先级和调用限制

### 8.2 结构化工具定义

作用：

- 真正决定当前运行可调用什么
- 作为 provider 的 `tools` 参数传入

结论：

- 文本提示只负责“理解”
- 结构化定义才负责“权限”

因此：

- 不要把工具白名单仅写在提示词里
- 也不要只传结构化工具而完全没有工具文本说明

## 9. 上下文层的处理原则

第 8 层的推荐内容：

- `USER.md`
- 工作区路径和环境信息
- 当前会话必要历史摘要
- 历史工具结果摘要
- 当前分支、当前任务状态、待完成项

不建议直接把所有原始历史消息都塞进文本上下文层。

推荐做法：

- system prompt 层只放“摘要化上下文”
- 原始历史消息继续按 conversation messages 传给模型

## 10. 单入口装配流程

推荐提供一个单入口：

```go
func AssemblePrompt(in PromptAssemblyInput) (*PromptAssemblyResult, error)
```

返回值建议如下：

```go
type PromptAssemblyResult struct {
    SystemPrompt    string
    ToolDefinitions []providers.ToolDefinition
    Messages        []providers.Message
    Layers          []PromptLayerSnapshot
}
```

其中 `Layers` 用于调试和测试：

```go
type PromptLayerSnapshot struct {
    Name      string
    Priority  int
    Enabled   bool
    Source    string
    Content   string
}
```

这样做的价值是：

- 可以单测每一层是否启用
- 可以排查最终 prompt 为什么长成这样
- 可以快速定位是谁覆盖了谁

## 11. 推荐拼接算法

推荐不要在各处零散追加，而是在一个地方做统一拼接。

伪代码如下：

```go
func AssemblePrompt(in PromptAssemblyInput) (*PromptAssemblyResult, error) {
    layers := []PromptLayerSnapshot{}

    appendLayer := func(name string, priority int, source string, content string) {
        content = strings.TrimSpace(content)
        if content == "" {
            layers = append(layers, PromptLayerSnapshot{
                Name: name, Priority: priority, Enabled: false, Source: source,
            })
            return
        }
        layers = append(layers, PromptLayerSnapshot{
            Name: name, Priority: priority, Enabled: true, Source: source, Content: content,
        })
    }

    appendLayer("builtin_boundary", 10, "system", in.BuiltinBoundary)
    appendLayer("soul", 20, "SOUL.md", in.Soul)
    appendLayer("identity", 30, "IDENTITY.md", in.Identity)
    appendLayer("agent_core", 40, "agent_custom_or_builtin_generic", resolveAgentCore(in))

    if in.Mode == PromptModeMain {
        appendLayer("agent_collaboration", 50, "AGENTS.md", in.AgentsFile)
        appendLayer("spawnable_agent_catalog", 55, "dynamic_catalog", in.SpawnableAgentCatalog)
    } else {
        appendLayer("subagent_descriptor", 50, "dynamic_subagent", in.SubagentDescriptor)
        appendLayer("current_task", 55, "runtime_task", in.CurrentTask)
    }

    appendLayer("skills", 60, "skills_runtime", resolveSkills(in))
    appendLayer("tools", 70, "runtime_tools", in.ToolSummary)
    appendLayer("context_user", 80, "USER.md", in.UserFile)
    appendLayer("context_runtime", 85, "runtime_context", strings.Join(in.ContextBlocks, "\n\n"))

    systemPrompt := renderLayers(layers)

    messages := []providers.Message{
        {Role: "system", Content: systemPrompt},
    }
    messages = append(messages, in.HistoryMessages...)
    if strings.TrimSpace(in.CurrentUserInput) != "" {
        messages = append(messages, providers.Message{
            Role: "user",
            Content: in.CurrentUserInput,
        })
    }

    return &PromptAssemblyResult{
        SystemPrompt: systemPrompt,
        ToolDefinitions: in.ToolDefinitions,
        Messages: messages,
        Layers: layers,
    }, nil
}
```

## 12. 渲染规则

建议统一使用一个渲染函数，不要在多个地方手工拼换行。

规则建议：

1. 每层单独渲染标题
2. 层与层之间用统一分隔符连接
3. 空层不渲染正文
4. 系统 prompt 中不拼入当前用户输入

示例：

```md
## Builtin Boundary
...

---

## Soul
...

---

## Identity
...
```

## 13. 测试建议

这套逻辑必须配单测，至少覆盖下面场景：

1. 主 Agent 有自定义提示词
   - 断言使用 `AgentCustomPrompt`
   - 断言不使用 `BuiltinGenericCore`
2. 主 Agent 无自定义提示词
   - 断言第 4 层回退到 `BuiltinGenericCore`
3. 子 Agent 模式
   - 断言使用目标 Agent 的 `SOUL.md`、`IDENTITY.md`
   - 断言注入 `SubagentDescriptor`
4. 技能两阶段
   - 未选择技能时注入摘要
   - 选择技能后注入正文
5. 工具层
   - 断言文本摘要和结构化工具定义都存在
6. 空层处理
   - 断言空层不会污染最终 prompt

## 14. 推荐代码改造方向

如果在当前仓库落地，建议按这个方向拆分：

- `internal/core/agent/prompt_layers.go`
  定义层、输入、输出和渲染器
- `internal/core/agent/prompt_sources.go`
  负责读取 `SOUL.md`、`IDENTITY.md`、`AGENTS.md`、`USER.md`
- `internal/core/agent/prompt_assembler.go`
  实现统一装配入口
- `internal/core/agent/prompt_assembler_test.go`
  覆盖优先级和回退规则

现有的 `context.go`、`manager.go`、`orchestrator.go` 可以逐步收敛到这个装配器，而不是继续在多处追加字符串。

## 15. 一句话原则

**把“提示词内容”改造成“有优先级的层”，把“层的回退”改造成显式规则，把“最终 prompt”改造成单入口产物。**
