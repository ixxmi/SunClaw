# SunClaw 多 Agent 编排实施蓝图

## 文档目标

这份文档承接前一份分析文档：

- [sunclaw-multi-agent-plan-dispatch-optimization.md](/Users/xuechenxi/Documents/company/code/SunClaw/docs/design/sunclaw-multi-agent-plan-dispatch-optimization.md)

上一份文档讲的是：

- Claude Code 的多 Agent 协作方法是什么
- SunClaw 当前差距在哪里
- 为什么要引入 `Plan -> Step -> Task`

这份文档只回答一个问题：

**如果现在就开始改 SunClaw，具体应该怎么拆模块、怎么定义数据结构、怎么接到现有代码里，才能保证“能写计划，也能按计划派发任务”。**

---

## 1. 实施目标

改造完成后，SunClaw 应该具备以下能力：

1. 主编排 Agent 能把任务写成正式计划
2. 每个计划步骤都能绑定到一个真实任务
3. 任务可以是 `subagent / acp / shell / cron`
4. 主编排可以继续已有任务，而不是只能新开
5. 子任务完成后，结果以统一结构回流
6. 主编排只基于真实结果推进下一步
7. 同时具备最小的可观测性与可控制性
   - `list`
   - `get`
   - `stop`
   - `continue`

---

## 2. 当前代码基线

当前已经有的基础：

- 主编排循环
  - [orchestrator.go](/Users/xuechenxi/Documents/company/code/SunClaw/internal/core/agent/orchestrator.go)
- Agent 管理器
  - [manager.go](/Users/xuechenxi/Documents/company/code/SunClaw/internal/core/agent/manager.go)
- 子 Agent 派发
  - [subagent_spawn_tool.go](/Users/xuechenxi/Documents/company/code/SunClaw/internal/core/agent/tools/subagent_spawn_tool.go)
- 子 Agent 回流
  - [subagent_announce.go](/Users/xuechenxi/Documents/company/code/SunClaw/internal/core/agent/subagent_announce.go)
- 最小任务层
  - [types.go](/Users/xuechenxi/Documents/company/code/SunClaw/internal/core/task/types.go)
  - [manager.go](/Users/xuechenxi/Documents/company/code/SunClaw/internal/core/task/manager.go)

结论：

- 现有实现不需要推倒重来
- 应该在现有 `agent/manager/task` 之上继续加层

---

## 3. 目标模块拆分

建议最终拆成 4 个核心包：

### 3.1 `internal/core/plan`

职责：

- 管理正式计划
- 管理步骤状态
- 记录当前步骤
- 将步骤与任务关联

建议文件：

- `types.go`
- `store.go`
- `manager.go`

### 3.2 `internal/core/coordinator`

职责：

- 判断当前是 `direct / plan / execute`
- 生成最小计划
- 为当前步骤做 dispatch 决策
- 做 `continue vs spawn fresh` 决策

建议文件：

- `frontdoor.go`
- `planner.go`
- `dispatch.go`
- `selector.go`

### 3.3 `internal/core/task`

职责：

- 承载统一任务对象
- 绑定 backend
- 生命周期控制
- 任务查询 / 停止 / 继续

当前已有，建议继续扩展：

- `types.go`
- `store.go`
- `manager.go`
- `backend.go`
- `events.go`
- `control.go`

### 3.4 `internal/core/task/backends`

职责：

- 各类 backend 的运行适配器

建议文件：

- `subagent_backend.go`
- `acp_backend.go`
- `shell_backend.go`
- `cron_backend.go`

---

## 4. Plan 层设计

## 4.1 数据结构

### `PlanStatus`

```go
type PlanStatus string

const (
    PlanDraft     PlanStatus = "draft"
    PlanActive    PlanStatus = "active"
    PlanCompleted PlanStatus = "completed"
    PlanBlocked   PlanStatus = "blocked"
    PlanCanceled  PlanStatus = "canceled"
)
```

### `StepStatus`

```go
type StepStatus string

const (
    StepPending   StepStatus = "pending"
    StepReady     StepStatus = "ready"
    StepRunning   StepStatus = "running"
    StepDone      StepStatus = "completed"
    StepBlocked   StepStatus = "blocked"
    StepFailed    StepStatus = "failed"
    StepSkipped   StepStatus = "skipped"
)
```

### `StepKind`

```go
type StepKind string

const (
    StepDirect         StepKind = "direct"
    StepResearch       StepKind = "research"
    StepSynthesis      StepKind = "synthesis"
    StepDesign         StepKind = "design"
    StepImplementation StepKind = "implementation"
    StepVerification   StepKind = "verification"
    StepReview         StepKind = "review"
    StepSummary        StepKind = "summary"
)
```

### `PlanStep`

```go
type PlanStep struct {
    ID            string     `json:"id"`
    Title         string     `json:"title"`
    Kind          StepKind   `json:"kind"`
    Goal          string     `json:"goal"`
    AgentHint     string     `json:"agent_hint,omitempty"`
    Strategy      string     `json:"strategy,omitempty"` // serial / parallel / continue_existing / spawn_fresh
    RelevantFiles []string   `json:"relevant_files,omitempty"`
    Constraints   []string   `json:"constraints,omitempty"`
    Deliverables  []string   `json:"deliverables,omitempty"`
    DoneWhen      []string   `json:"done_when,omitempty"`
    DependsOn     []string   `json:"depends_on,omitempty"`
    Status        StepStatus `json:"status"`
    TaskID        string     `json:"task_id,omitempty"`
    Notes         string     `json:"notes,omitempty"`
}
```

### `PlanRecord`

```go
type PlanRecord struct {
    ID            string      `json:"id"`
    SessionKey    string      `json:"session_key"`
    AgentID       string      `json:"agent_id"`
    Goal          string      `json:"goal"`
    Status        PlanStatus  `json:"status"`
    Steps         []PlanStep  `json:"steps"`
    CurrentStepID string      `json:"current_step_id,omitempty"`
    LastDecision  string      `json:"last_decision,omitempty"`
    CreatedAt     int64       `json:"created_at"`
    UpdatedAt     int64       `json:"updated_at"`
}
```

## 4.2 `plan.Manager` 必备接口

```go
type Manager struct { ... }

func (m *Manager) Load() error
func (m *Manager) Create(plan *PlanRecord) error
func (m *Manager) Get(id string) (*PlanRecord, bool)
func (m *Manager) GetActiveBySession(sessionKey string) (*PlanRecord, bool)
func (m *Manager) Replace(plan *PlanRecord) error
func (m *Manager) MarkStepRunning(planID, stepID, taskID string) error
func (m *Manager) MarkStepDone(planID, stepID, note string) error
func (m *Manager) MarkStepBlocked(planID, stepID, note string) error
func (m *Manager) Advance(planID string) (*PlanStep, error)
```

## 4.3 设计要点

- 同一 session 默认只允许一个 active plan
- `CurrentStepID` 只能指向一个 step
- step 与 task 是 1 对 1 主绑定
- 支持后续扩展 `parallel group`

---

## 5. Task 层扩展设计

当前 [types.go](/Users/xuechenxi/Documents/company/code/SunClaw/internal/core/task/types.go) 还太薄。

建议扩展为：

```go
type Record struct {
    ID            string   `json:"id"`
    Backend       Backend  `json:"backend"`
    Type          string   `json:"type,omitempty"`
    Status        Status   `json:"status"`
    Summary       string   `json:"summary,omitempty"`

    SessionKey    string   `json:"session_key,omitempty"`
    AgentID       string   `json:"agent_id,omitempty"`
    PlanID        string   `json:"plan_id,omitempty"`
    StepID        string   `json:"step_id,omitempty"`
    ParentTaskID  string   `json:"parent_task_id,omitempty"`
    ContinueOf    string   `json:"continue_of,omitempty"`
    CanContinue   bool     `json:"can_continue"`

    CreatedAt     int64    `json:"created_at"`
    StartedAt     *int64   `json:"started_at,omitempty"`
    EndedAt       *int64   `json:"ended_at,omitempty"`
    Result        *Result  `json:"result,omitempty"`

    Subagent      *SubagentPayload `json:"subagent,omitempty"`
}
```

### 新增字段的意义

- `PlanID`
  - 任务属于哪一个 plan
- `StepID`
  - 任务属于哪一个 step
- `ParentTaskID`
  - 预留给后续嵌套协作
- `ContinueOf`
  - 当前任务是否是某个旧任务的 continuation
- `CanContinue`
  - 该任务是否允许继续

## 5.1 `task.Manager` 必备接口

在现有基础上增加：

```go
func (m *Manager) ListByPlan(planID string) []*Record
func (m *Manager) Continue(taskID string, message string) error
func (m *Manager) Stop(taskID string) error
func (m *Manager) RegisterBackend(backend Backend, runner BackendRunner)
```

---

## 6. BackendRunner 接口

要把 Claude Code 的本地 / 远程 / worktree / teammate 思路映射过来，SunClaw 必须把 backend 抽象出来。

```go
type BackendRunner interface {
    Start(ctx context.Context, task *Record) error
    Continue(ctx context.Context, taskID string, message string) error
    Stop(ctx context.Context, taskID string) error
}
```

### 第一批 backend

#### `SubagentBackend`

职责：

- 承接当前的 `handleSubagentSpawn`
- 管理 child session
- 完成后触发统一结果回流

#### `ACPBackend`

职责：

- 把 ACP 会话/线程任务化
- 用统一 task 接口托管

#### `ShellBackend`

职责：

- 承接长运行 shell task
- 后续可与 `run_shell` 的 background 模式打通

#### `CronBackend`

职责：

- 用 task manager 记录 cron 的一次真实执行

---

## 7. Coordinator 层设计

## 7.1 `frontdoor.go`

职责：

- 接收用户消息
- 恢复 active plan
- 判断当前模式

建议接口：

```go
type InteractionMode string

const (
    ModeDirect  InteractionMode = "direct"
    ModePlan    InteractionMode = "plan"
    ModeExecute InteractionMode = "execute"
)

func DetermineMode(input string, activePlan *plan.PlanRecord, authorized bool) InteractionMode
```

判断规则：

- 没 active plan 且任务明显多步 -> `plan`
- 有 active plan 且用户说“继续” -> `execute`
- 纯提问 -> `direct`

## 7.2 `planner.go`

职责：

- 让主编排生成最小计划
- 解析成 `PlanRecord`

注意：

- 这里不建议一开始就做复杂 parser
- 第一版可以依赖 LLM 按固定 JSON / Markdown 模板输出
- 然后做严格校验

## 7.3 `selector.go`

职责：

- 为当前 step 选 agent

输入：

- step kind
- agent catalog
- user constraints

输出：

- `agent_id`
- `reason`
- `confidence`

建议规则优先级：

1. step kind 对应的职责边界
2. description 的 when-to-use
3. do-not-use
4. tools

## 7.4 `dispatch.go`

职责：

- 决定当前 step 是：
  - `spawn_fresh`
  - `continue_existing`
  - `stop_and_respawn`

建议输出：

```go
type DispatchMode string

const (
    DispatchSpawnFresh     DispatchMode = "spawn_fresh"
    DispatchContinue       DispatchMode = "continue_existing"
    DispatchStopAndRespawn DispatchMode = "stop_and_respawn"
)

type DispatchDecision struct {
    Mode       DispatchMode
    AgentID    string
    TaskID     string
    Reason     string
    StepSpec   StepSpec
}
```

---

## 8. StepSpec：派发前的综合对象

这是 SunClaw 最该补的东西。

Claude Code 最强的一点是：

**research 回来后，coordinator 先综合，再派发。**

SunClaw 也必须引入一个正式中间层：

```go
type StepSpec struct {
    Purpose      string
    Goal         string
    Relevant     []string
    Constraints  []string
    Deliverables []string
    DoneWhen     []string
}
```

要求：

- 任何 implementation / verification 派发，都必须来自 `StepSpec`
- 不允许直接把 raw research result 丢给下一个 worker

### `StepSpec` 的生成时机

1. research task 完成
2. coordinator 读取结果
3. coordinator 输出结构化 `StepSpec`
4. dispatch 用这个 spec 构造 `sessions_spawn`

---

## 9. 新增工具设计

SunClaw 要真正接近 Claude Code 的多 Agent 协作，至少要新增 4 个工具。

## 9.1 `task_continue`

用途：

- 给已有 task / child session 发送 follow-up

参数：

```json
{
  "task_id": "task-123",
  "message": "补跑局部测试并回报结果"
}
```

返回：

```json
{
  "status": "continued",
  "task_id": "task-123"
}
```

第一版只需要支持：

- `subagent` backend

## 9.2 `task_stop`

用途：

- 停止一个运行中的 task

参数：

```json
{
  "task_id": "task-123"
}
```

## 9.3 `task_get`

用途：

- 查看任务状态与摘要

## 9.4 `task_list`

用途：

- 查看当前 session / plan 下的任务

---

## 10. 结果回流协议升级

当前子 Agent 回流主要是文本化注入。

建议改成统一结构：

```xml
<task-notification>
  <task-id>task-123</task-id>
  <plan-id>plan-1</plan-id>
  <step-id>step-2</step-id>
  <status>completed</status>
  <summary>BackendCoder completed current step</summary>
  <result>...</result>
  <verification>...</verification>
  <artifacts>...</artifacts>
</task-notification>
```

### 统一字段

- `task-id`
- `plan-id`
- `step-id`
- `status`
- `summary`
- `result`
- `verification`
- `artifacts`

### 这样做的价值

- 主编排可先更新状态，再生成面向用户的总结
- plan manager 可自动推进 step
- dashboard / API / observability 可复用

---

## 11. 与现有代码的接入点

## 11.1 `AgentManager`

当前 [manager.go](/Users/xuechenxi/Documents/company/code/SunClaw/internal/core/agent/manager.go) 已经是最自然接入点。

建议新增字段：

```go
planManager        *plan.Manager
dispatchController *coordinator.DispatchController
```

### 新职责

- setup 时加载 plan manager
- subagent 完成时除了更新 task，也更新 step
- inbound message 到来时先走 frontdoor

## 11.2 `sessions_spawn`

当前要增强为：

- 支持 `plan_id`
- 支持 `step_id`

参数建议新增：

```go
PlanID string `json:"plan_id,omitempty"`
StepID string `json:"step_id,omitempty"`
```

## 11.3 `handleSubagentSpawn`

当前内部逻辑不要重写，只要：

- 启动前标记 step running
- 完成后更新 task
- 再推进 plan

---

## 12. Phase 切分与交付顺序

## Phase 1：正式引入 Plan

交付：

- `internal/core/plan/*`
- `PlanRecord / PlanStep`
- `GetActiveBySession`

完成标志：

- 一个 session 可以拥有 active plan
- 当前步骤可读

## Phase 2：把 `sessions_spawn` 接到 PlanStep

交付：

- `plan_id / step_id`
- task record 扩展

完成标志：

- 子任务与 step 强绑定

## Phase 3：新增 `task_continue`

交付：

- tool
- subagent backend 的 continue

完成标志：

- 主编排可继续现有 worker

## Phase 4：结果协议升级

交付：

- 统一 task notification
- step 自动推进

完成标志：

- 主编排不再靠纯文本猜测状态

## Phase 5：ACP / shell / cron backend 统一

交付：

- backend runner 抽象
- ACP backend
- shell backend

完成标志：

- `task.*` 工具对多 backend 统一可用

## Phase 6：并发治理

交付：

- write scope
- 冲突检测
- 并发策略

完成标志：

- 主编排能安全地并发 research 和 verification

---

## 13. 实施时最容易出错的地方

### 1. 先做太复杂的并行 DAG

建议：

- 第一版只支持单 active step
- 先把串行跑通

### 2. 没有 `task_continue` 就想做复杂多阶段协作

结果：

- 只能不断 fresh spawn
- 成本高、噪音大

### 3. 让 worker 自己继续综合

结果：

- research 和 implementation 混在一起
- prompt 质量退化

### 4. 结果回流仍然只是自然语言

结果：

- task/plan manager 无法稳定自动推进

---

## 14. 最小实现优先级

如果只能先做最小集合，按这个顺序：

1. `plan.Manager`
2. `PlanRecord / PlanStep`
3. `sessions_spawn + plan_id / step_id`
4. `task_continue`
5. `task_stop`
6. 统一 `task-notification`

做完这六项，SunClaw 就已经具备：

- 计划
- 派发
- 继续
- 停止
- 结果回流
- 按步骤推进

这就是 Claude Code 多 Agent 协作里最值钱的那部分。

---

## 15. 最终结论

SunClaw 当前不缺“多 Agent”的基础能力。

SunClaw 真正缺的是：

- **正式计划层**
- **正式继续机制**
- **正式 step 绑定**
- **正式结果协议**

只要把这四点补齐，SunClaw 就会从：

- “能开子 Agent”

升级为：

- “能写计划并按计划派发任务的 coordinator runtime”
