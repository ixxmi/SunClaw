# GoClaw 架构介绍

> Go 语言的个人 AI 助手 • OpenClaw 设计理念的实现

---

## 概述

GoClaw 是一个用 Go 语言编写的个人 AI 助手，灵感来自 OpenClaw（原 Clawdbot/Moltbot）。它运行在本地服务器上，通过 WebSocket/HTTP 暴露服务，支持多种消息channel（Telegram、WhatsApp、飞书、QQ、Slack 等）接入。

![](goclaw.png)

### 核心定位

| 特性 | 说明 |
|------|------|
| **语言** | Go（单一静态链接二进制） |
| **架构** | CLI 应用 + 网关服务器 |
| **部署** | 本地进程，暴露 WebSocket/HTTP |
| **扩展性** | 技能系统 (OpenClaw 兼容) |
| **可靠性** | 串行默认，故障转移，反思机制 |

---

## 核心架构

### 系统全景图

```
┌──────────────────────────────────────────────────────────────────────────────┐
│                      GoClaw 系统架构                                          │
├──────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│   ┌─────────────┐    ┌─────────────┐    ┌─────────────┐    ┌─────────────┐   │
│   │ 输入Channel  │── →│  网关服务器  │───→│  Agent Loop │───→│  LLM/工具    │   │
│   └─────────────┘    └─────────────┘    └─────────────┘    └─────────────┘   │
│        │                  │                   │                  │           │
│        │                  │                   │                  │           │
│        v                  v                   v                  v           │
│   Telegram/WhatsApp    MessageBus        SessionManager       Providers      │
│   飞书/QQ/Slack         串行队列            JSONL存储            OpenAI/AI...   │
│                                                                              │
└──────────────────────────────────────────────────────────────────────────────┘
```

### 消息处理流程

```
用户消息 → Channel适配器 → 网关服务器 → Agent Loop → LLM 调用 → 工具执行 → 响应返回
```

---

## Agent Loop 执行引擎

Agent Loop 是 GoClaw 的大脑，负责思考、规划和执行。它受到 OpenClaw 的 **PI Agent** 架构启发。

### PI Agent 理念

**PI** = 极简编码代理（Mario Zechner 创建）

> "LLMs 本身就擅长编写和运行代码，不需要过多的包装"

| 特性 | 传统 Agent (ReAct) | PI Agent | GoClaw |
|------|---------------------|----------|--------|
| 循环模式 | Observe → Think → Act → Plan | Stream → Execute → Continue | Stream → Execute → Reflect → Continue |
| 系统提示词 | 数千 token | < 1K token | ~1.5K token |
| 工具数量 | 许多专用工具 | 4 个核心工具 | 核心工具 + 可扩展 |
| 最大步数 | 固定上限 (20) | 无限制 | 15 + 反思判断 |
| 计划模式 | 内置计划阶段 | 文件可见计划 | 可选反思机制 |

### 执行流程图

```
┌───────────────────────────────────────────────────────────────────┐
│                       GoClaw Agent Loop                              │
├───────────────────────────────────────────────────────────────────┤
│                                                                    │
│  ┌──────────┐     ┌──────────┐     ┌──────────┐     ┌──────────┐   │
│  │ 接收消息   │────→│ 构建上下文 │────→│  LLM 调用 │────→│ 执行工具   │   │
│  └──────────┘     └──────────┘     └──────────┘     └──────────┘   │
│                                                        │            │
│                                                        v            │
│                                            ┌─────────────────┐      │
│                                            │   工具结果返回   │      │
│                                            └─────────────────┘      │
│                                                        │            │
│                                                        v            │
│                                            ┌─────────────────┐      │
│                                            │   反思机制判断   │      │
│                                            │   (可选)         │      │
│                                            └─────────────────┘      │
│                                                        │            │
│                                    ┌─────────────┴────────────┐     │
│                                    v                           v     │
│                             ┌─────────────┐           ┌─────────────┐ │
│                             │  继续迭代   │           │  返回响应   │ │
│                             └─────────────┘           └─────────────┘ │
│                                                                    │
└───────────────────────────────────────────────────────────────────┘
```

### 核心组件

#### 1. 上下文构建器 (ContextBuilder)

**文件**: `agent/context.go`

```go
type ContextBuilder struct {
    memory       *MemoryStore
    workspace    string
    agentID      string
    defaultModel string
    provider     string
}
```

**职责**:
- 动态组装系统提示词
- 注入可用工具定义
- 添加会话历史（从 JSONL）
- 整合相关记忆
- 上下文窗口保护与压缩

**上下文压缩策略**:
```go
// agent/loop.go:571
func (l *Loop) compressSession(sess *session.Session) {
    // 保留最近 10 轮对话
    // 保留所有系统消息
    // 丢弃早期历史，保留关键上下文
}
```

#### 2. 工具注册表 (Tools Registry)

**文件**: `agent/tools/registry.go`

**核心工具集（对应 PI 的四个工具）**:

| GoClaw 工具 | 功能 | PI 对应 |
|------------|------|--------|
| `read_file` | 读取文件（行号、glob） | `read` |
| `write_file` | 创建/覆盖文件 | `write` |
| `edit_file` | 精确字符串替换 | `edit` |
| `run_shell` | Shell 命令执行 | `bash` |

**GoClaw 扩展工具**:
- `browser_*` - 浏览器操作 (Chrome DevTools Protocol)
- `smart_search` - 混合搜索 (向量 + FTS5)
- `spawn` - 子代理管理
- `message` - 消息发送

**工具执行与重试**:
```go
// agent/loop.go:526
func (l *Loop) executeToolWithRetry(ctx context.Context, toolName string, params map[string]interface{}) (string, error) {
    // 网络错误自动重试
    // 错误分类与降级建议
    // 结构化错误返回给 LLM 自我校正
}
```

#### 3. 反思器 (Reflector)

**文件**: `agent/reflection.go`

解决传统 Agent "何时停止" 的难题：

```go
type Reflector struct {
    config   *ReflectionConfig
    provider providers.Provider
    workspace string
}

type TaskReflection struct {
    Status         TaskStatus  // "completed", "in_progress", "failed", "blocked"
    Confidence     float64     // 0.0 - 1.0
    CompletedSteps []string    // 已完成步骤
    RemainingSteps []string    // 剩余步骤
    Reasoning      string      // 思考过程
    NextAction     string      // 下一步行动
}
```

**反思流程**:
```go
// agent/loop.go:487
reflection, reflectErr := l.reflector.Reflect(ctx, userRequest, reflectionHistory)

if l.reflector.ShouldContinueIteration(reflection, iteration, l.maxIteration) {
    continuePrompt = l.reflector.GenerateContinuePrompt(reflection)
    continue
}
```

**与传统 max-iterations 的区别**:
- 传统：固定步数后强制停止
- 反思：动态判断任务状态，智能决定是否继续

#### 4. 错误分类器 (ErrorClassifier)

**文件**: `agent/error_classifier.go`

```go
type ErrorClassifier struct {
    authPatterns      []string
    rateLimitPatterns []string
    timeoutPatterns   []string
    billingPatterns   []string
}
```

**错误分类**:
- `auth` - 认证错误（需要故障转移）
- `rate_limit` - 限流错误（需要冷却）
- `timeout` - 超时错误（可重试）
- `billing` - 计费错误（需要故障转移）
- `tool` - 工具错误（返回给 LLM）

**智能降级策略**:
```go
// agent/loop.go:628
func (l *Loop) formatToolError(toolName string, params map[string]interface{}, err error) string {
    // 根据工具类型和错误提供具体建议
    // 例如：write_file 失败 → 建议输出到控制台
    // 例如：browser 失败 → 建议使用 web_fetch
}
```

---

## GoClaw vs OpenClaw PI Agent

### 架构对比

**OpenClaw PI (TypeScript) - 极简循环**:
```typescript
while (true) {
  const response = await streamCompletion(model, context);
  if (!response.toolCalls?.length) break;

  for (const call of response.toolCalls) {
    const result = await executeToolCall(call, context);
    context.messages.push(result);
  }
}
```

**GoClaw (Go) - 增强循环**:
```go
for iteration < l.maxIteration {
    response, err := l.provider.Chat(ctx, messages, tools)

    if len(response.ToolCalls) > 0 {
        for _, tc := range response.ToolCalls {
            result, err := l.executeToolWithRetry(ctx, tc.Name, tc.Params)
        }
        continue
    }

    if response.Content == "" && l.reflector != nil {
        reflection := l.reflector.Reflect(ctx, userRequest, history)
        if !l.reflector.ShouldContinueIteration(reflection, iteration, l.maxIteration) {
            break
        }
        continue
    }

    break
}
```

### 会话存储对比

| 特性 | OpenClaw PI | GoClaw |
|------|-------------|--------|
| 格式 | Append-Only DAG | Linear JSONL |
| 跨模型 | 支持（pi-ai 抽象） | 支持（Provider 抽象） |
| 持久化 | 单一文件 | JSONL（每行一条） |
| 分支 | 移动叶指针（高效） | 创建新会话（简单） |

### 扩展系统对比

**OpenClaw PI - TypeScript Hooks**:
```typescript
hooks: {
  onSessionStart: async (session) => { ... },
  onBeforeTurn: async (session) => { ... },
  onToolCall: async (tool, params) => { ... },
  // 20+ 生命周期钩子
}
```

**GoClaw - Skills System**:
```go
type Skill struct {
    Name        string
    Description string
    Triggers    []string    // 触发关键词
    Content     string      // Markdown 指令
    Requires    []string    // 环境依赖 (Gating)
}
```

### 设计哲学对比

| 理念 | OpenClaw PI | GoClaw |
|------|-------------|--------|
| 核心原则 | "LLMs 知道如何编码" | "可靠性优于复杂性" |
| 系统提示词 | 极简 (< 1K) | 适度 (~1.5K) |
| 工具哲学 | 少即是好 (4 个) | 核心工具 + 可扩展 |
| 错误处理 | 返回给模型自我校正 | 分类 + 重试 + 降级 |
| 终止条件 | 自然停止 | 反思机制 + 最大迭代 |
| 可观测性 | 完全透明（计划文件） | 结构化日志 + 会话持久化 |

### GoClaw 独特创新

1. **反思机制** - 解决 Agent "何时停止" 难题
2. **错误分类与重试** - 三层容错（工具级、迭代级、提供商级）
3. **混合搜索** - 向量 + FTS5 双重检索
4. **技能准入 (Gating)** - 根据环境自动加载技能
5. **上下文压缩** - 智能保留关键历史

---

## 网关服务器

**文件**: `gateway/server.go`

网关服务器是 GoClaw 的心脏，负责任务/会话协调：

```go
type Server struct {
    config        *config.GatewayConfig
    wsConfig      *WebSocketConfig
    bus           *bus.MessageBus
    channelMgr    *channels.Manager
    sessionMgr    *session.Manager
    connections   map[string]*Connection
}
```

**核心功能**:
- 处理多个重叠请求
- 基于队列的消息总线（串行默认）
- WebSocket 连接支持
- Webhook 回调处理
- HTTP 健康检查端点

---

## 内存系统

### 双重记忆架构

#### 会话记录 (JSONL)
- 每行一个 JSON 对象
- 包含用户消息、工具调用、结果、响应
- 会话基础的持久化记忆

#### 记忆文件 (Markdown)
- `MEMORY.md` 或 `memory/` 文件夹
- 由代理使用标准文件写入工具生成
- 没有特殊的记忆写入 API

### 混合搜索

| 类型 | 用途 | 实现 |
|------|------|------|
| **向量搜索** | 语义相似性 | SQLite + 嵌入 |
| **关键词搜索 (FTS5)** | 精确短语匹配 | SQLite 扩展 |

**文件**: `memory/search.go`

```go
type MemoryManager struct {
    store         Store
    provider      EmbeddingProvider
    cache         map[string]*VectorEmbedding
}
```

---

## 计算机使用

### Shell 执行

| 模式 | 说明 |
|------|------|
| **沙箱**（默认） | Docker 容器中运行 |
| **直接主机** | 直接在主机上运行 |
| **远程设备** | 在远程设备上执行 |

### 文件系统工具

- `read_file` - 读取文件
- `write_file` - 写入文件
- `edit_file` - 编辑文件
- `list_files` - 列出目录

### 浏览器工具 (Chrome DevTools Protocol)

- `browser_navigate` - 导航到 URL
- `browser_screenshot` - 截取页面截图
- `browser_execute_script` - 执行 JavaScript
- `browser_click` - 点击元素
- `browser_fill_input` - 填写输入框
- `browser_get_text` - 获取页面文本

**文件**: `agent/tools/browser.go`

```go
type BrowserTool struct {
    headless bool
    timeout  time.Duration
    outputDir string
}
```

### 进程管理

- 后台长期命令
- 终止进程

---

## 安全机制

### 允许列表 (Allowlist)

```json
{
  "agents": {
    "main": {
      "allowlist": [
        { "pattern": "/usr/bin/npm", "lastUsedAt": 1706644800 },
        { "pattern": "/opt/homebrew/bin/git", "lastUsedAt": 1706644900 }
      ]
    }
  }
}
```

### 预批准的安全命令

`jq`、`grep`、`cut`、`sort`、`uniq`、`head`、`tail`、`tr`、`wc` 等

### 危险构造阻止

```bash
# 这些在执行前会被拒绝：
npm install $(cat /etc/passwd)  # 命令替换
cat file > /etc/hosts           # 重定向
rm -rf / || echo "failed"       # 用 || 链接
(sudo rm -rf /)                 # 子shell
```

---

## LLM 提供商与故障转移

### 支持的提供商

- OpenAI（兼容接口）
- Anthropic
- OpenRouter

### 故障转移机制

**文件**: `providers/failover.go`

```go
type FailoverProvider struct {
    primary         Provider
    fallback        Provider
    circuitBreaker  *CircuitBreaker
    errorClassifier types.ErrorClassifier
}
```

**特性**:
- **断路器模式** - 防止连续失败时持续调用失败的提供商
- **错误分类** - 区分可重试和不可重试错误
- **自动切换** - 主提供商失败时自动切换到备用

---

## 技能系统 (Skills)

### 特性

- **Prompt-Driven** - 技能本质上是注入到 System Prompt 中的指令集
- **OpenClaw 兼容** - 完全兼容 OpenClaw 的技能生态
- **自动准入 (Gating)** - 智能检测系统环境

### 技能加载顺序

1. 传入的自定义目录
2. `workspace/skills/`
3. `workspace/.goclaw/skills/`
4. 可执行文件路径 `/skills/`
5. `./skills/`（当前目录，优先级最高）

---

## 项目结构

```
goclaw/
├── agent/                  # Agent 核心逻辑
│   ├── loop.go             # Agent 循环（含重试逻辑）
│   ├── context.go          # 上下文构建器
│   ├── memory.go           # 记忆系统
│   ├── skills.go           # 技能加载器
│   ├── reflection.go       # 反思机制
│   ├── error_classifier.go  # 错误分类器
│   └── tools/              # 工具系统
│       ├── registry.go
│       ├── browser.go
│       ├── filesystem.go
│       ├── shell.go
│       └── ...
├── channels/               # 消息通道
├── bus/                    # 消息总线
├── config/                 # 配置管理
├── providers/              # LLM 提供商
│   ├── failover.go
│   ├── circuit.go
│   └── ...
├── session/                # 会话管理
├── memory/                 # 记忆存储
├── cli/                    # 命令行界面
└── gateway/                # 网关服务器
```

---

## 与 OpenClaw 的主要区别

| 特性 | OpenClaw (TypeScript) | GoClaw (Go) |
|------|----------------------|-------------|
| 语言 | TypeScript/Node.js | Go |
| 并发模型 | 异步/事件循环 | Goroutines + Channels |
| 浏览器工具 | Playwright | Chrome DevTools Protocol |
| 部署 | npm 包 | 单一静态链接二进制 |
| 内存管理 | V8 垃圾回收 | Go 垃圾回收 |
| 类型系统 | 结构化类型 | 静态强类型 |
| Agent Loop | PI 极简循环 | 增强循环（+反思） |
| 会话存储 | Append-Only DAG | Linear JSONL |
| 扩展机制 | TypeScript Hooks | Skills (Markdown) |

---

## 快速开始

```bash
# 构建
go build -o goclaw .

# 配置
cat > config.json << EOF
{
  "agents": {
    "defaults": {
      "model": "openrouter:anthropic/claude-opus-4-5",
      "max_iterations": 15
    }
  },
  "providers": {
    "openrouter": {
      "api_key": "your-key"
    }
  },
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "your-bot-token"
    }
  }
}
EOF

# 启动
./goclaw start
```

---

## 总结

GoClaw 继承了 OpenClaw 的核心设计理念，同时利用 Go 语言的特性构建了一个更加健壮、高性能的 AI 助手。

**设计原则**:
- 极简核心 + 可扩展技能
- 串行默认，显式并行
- 可靠性优于复杂性
- 完全可观测（日志 + 会话持久化）

> "这种简单性可能是优势也可能是陷阱，取决于你的视角。但我总是倾向于可解释的简单性，而不是复杂的意大利面条代码。"
