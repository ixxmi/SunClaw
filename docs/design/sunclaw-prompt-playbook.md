# SunClaw Prompt Playbook

## 目标

这份文档定义 SunClaw 后续新增或改写提示词时的默认写法，避免再次出现下面这些问题：

- 公共边界写进每个 agent，导致重复和漂移
- 主编排 prompt 和子 agent prompt 职责混杂
- `sessions_spawn` 派发载荷没有统一结构
- 子 agent 返回内容过大，挤爆主 agent 上下文

结论先说：

- 公共安全、执行、编排规则放在运行时内建层
- 自定义 `system_prompt` 只写角色、业务边界、风格差异
- 子 agent 不复用主 prompt 原文，而是使用“主 agent core + subagent descriptor + runtime context”
- 派发上下文一律写成结构化 Markdown

---

## 主编排 Prompt 写法

主编排 agent 的自定义 prompt 只负责三件事：

1. 定义角色身份
2. 定义当前业务边界
3. 定义该 agent 是否应该亲自执行还是更偏向编排

不要在自定义 prompt 里重复这些内容：

- 安全边界
- 工具通用规则
- 代码编辑通用规则
- `sessions_spawn` 的公共调用规则
- 子任务回传后的收口规则

推荐模板：

```md
# Identity

你是 SunClaw 的主编排 Agent，负责理解用户目标、拆解当前步骤、选择合适子 Agent，并基于真实结果推进下一步。

# Collaboration Rules

- 你优先负责计划、派发、校验、收束。
- 能直接回答的问题直接回答。
- 需要执行时，优先判断是否值得派发。
- 只推进当前最小一步，不把多阶段工作一次性打包。

# User Context

- 当前服务于 SunClaw 项目。
- 优先沿用仓库现有模式，不做无关重构。

# Personality

- 冷静
- 简洁
- 对不确定性诚实
```

---

## 子 Agent Prompt 写法

子 agent 不应该继承整份主编排 prompt，只应该拿到：

1. 目标 agent 的 core prompt
2. 当前委派步骤的 `SubagentDescriptor`
3. 精简的 runtime context

子 agent 默认不再拼接：

- `IDENTITY.md`
- `SOUL.md`
- `USER.md`

原因：

- 这些内容更适合主编排 agent 保持长期人格与用户上下文
- 子 agent 的职责是完成当前步骤，不需要再次吸收整套人格/用户信息
- 可以降低上下文占用，减少注意力分散

子 agent descriptor 推荐结构：

```md
# Subagent Context

你是当前被派发来处理单一步骤的子 Agent。

## 当前职责
- 当前委派步骤：<一句话步骤目标>
- 只完成当前步骤，或完成其中最小可交付闭环。

## 执行规则
1. 只读取最小必要上下文。
2. 优先产出真实执行结果。
3. 不把未完成工作描述为完成。

## 输出格式
- `状态`
- `结果`
- `关键产出`
- `验证`
- `风险与下一步`

## 上下文控制
- 不返回大段文件全文
- 不返回超长日志
- 优先返回摘要、关键片段、文件路径、命令名、验证结论
```

---

## `sessions_spawn` 载荷写法

后续所有派发都应该尽量使用同一套字段，避免 prompt 漂移。

推荐字段：

```json
{
  "label": "implement-api",
  "task": "实现用户资料更新接口的当前步骤",
  "context": "只处理 profile update 路径，不扩到鉴权和回归测试。",
  "relevant_files": [
    "internal/api/user.go",
    "internal/service/profile.go"
  ],
  "constraints": [
    "保持现有返回结构兼容",
    "不要修改无关模块"
  ],
  "deliverables": [
    "代码改动",
    "涉及文件列表",
    "局部验证结果"
  ],
  "done_when": [
    "接口逻辑完成",
    "返回结构未破坏兼容性",
    "结果按结构化格式回传"
  ]
}
```

字段原则：

- `task` 只写当前一步
- `context` 只写这一步所需背景
- `relevant_files` 保持短列表
- `constraints` 只写硬边界
- `deliverables` 写主 agent 真正需要汇总的东西
- `done_when` 写可判断的完成条件

不要这样写：

- 把整个需求文档直接塞进 `context`
- 把十几个目录全塞进 `relevant_files`
- 在 `task` 里混入“设计 + 实现 + 测试 + review”

---

## 层级标题约定

为了让模型注意力更稳定，标题层级统一如下：

- 运行时公共层：`#`
- 主认知包装层：`#`
- 子 agent runtime context：`#`
- 被包裹认知文件中的原始标题：只在主 agent 路径中按需下沉

推荐标题命名：

- `# Builtin Boundary`
- `# Safety & Compliance`
- `# Working Norms`
- `# Task Orchestration`
- `# Identity`
- `# Collaboration Rules`
- `# User Context`
- `# Personality`
- `# Subagent Runtime Context`

不要使用的问题标题：

- `# Assigned Task`
  - 容易和当前用户输入、子任务描述混淆
- `# Soul`
  - 对模型来说语义过虚，不如 `Personality` 稳定
- `# Instructions`
  - 信息过宽，不利于注意力聚焦

---

## 推荐拼接顺序

主编排 agent：

1. `Builtin Boundary`
2. `Safety & Compliance`
3. `Working Norms`
4. `Task Orchestration`
5. `Bootstrap Guide`（如果需要）
6. `Agent Core Prompt`
7. `Identity`
8. `Collaboration Rules`
9. `User Context`
10. `Personality`
11. `Skills`
12. `available_agents`
13. `available_tools`
14. `Context Summary`
15. `Runtime Context`

子 agent：

1. `Builtin Boundary`
2. `Safety & Compliance`
3. `Working Norms`
4. `Task Orchestration`
5. `Agent Core Prompt`
6. `Subagent Context`
7. `Subagent Runtime Context`
8. `available_tools`
9. `Context Summary`
10. `Runtime Context`

---

## 这样设计的优势

- 公共规则只维护一处，减少提示词漂移
- 主编排和子 agent 的注意力焦点分离更清楚
- `sessions_spawn` 的派发质量更稳定
- 子 agent 回传内容默认更短，降低上下文爆炸风险
- 后续接 ACP / shell / cron 时，可以复用同一套 task-oriented 提示词思路

## 当前限制

- 这套约定会让提示词更强调“当前一步”，对一次性大包任务更保守
- 如果某些 agent 天生就是执行型而非编排型，需要在其自定义 core prompt 里明确放宽
- 仅靠提示词不能替代权限和任务状态机，仍然要依赖运行时约束
