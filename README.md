# SunClaw

SunClaw 是一个基于 Go 构建的多 Agent 协作助手，面向企业微信、飞书、Telegram、Slack、Discord 等聊天场景，支持任务拆解、子 Agent 并行执行、结果汇总、工具调用、会话持久化和技能扩展。

当前仓库模块名和默认可执行文件名仍为 `goclaw`，配置目录也仍使用 `~/.goclaw/`，这是为了保持兼容；产品对外名称统一为 `SunClaw`。

## 核心能力

- 多 Agent 协作：主 Agent 可按任务类型调度 `architect`、`coder`、`frontend`、`reviewer`、`researcher`、`analyst` 等专长 Agent。
- 会话级 Agent 切换：支持在聊天内使用 `/agent <id>` 按会话切换当前 Agent。
- 子任务自动汇总：主 Agent 会等待所有子 Agent 完成后再统一回复；如果子 Agent 没有有效输出，则直接回复“执行完毕”。
- 丰富工具体系：支持文件读写、Shell、Web 检索、浏览器自动化、记忆系统、定时任务、ACP 等。
- 多渠道接入：支持企业微信、飞书、Telegram、钉钉、QQ、Slack、Discord、Google Chat、Teams 等。
- 会话持久化：支持历史消息、工具调用链、会话恢复和上下文重放。
- Skills 扩展：兼容 `SKILL.md` 规范，可按目录自动发现和加载技能。
- Gateway 与运维能力：支持 WebSocket Gateway、日志、健康检查、状态诊断和定时任务管理。

## 适用场景

- 让一个主助手自动分派“架构设计 + 后端实现 + 前端实现 + 代码审查”这类复合任务
- 在企业微信或飞书中把 SunClaw 当作团队协作机器人使用
- 把 AI Agent 接入本地工作区，直接读写代码、跑命令、查资料、整理结果
- 为不同团队成员绑定不同渠道账号或会话路由策略

## 工作方式

SunClaw 的典型执行链路如下：

1. 用户在聊天渠道发送消息
2. 路由层根据 `session route > binding > default` 选择当前 Agent
3. 当前 Agent 判断是直接处理，还是调用 `sessions_spawn` 派发子任务
4. 子 Agent 并行执行各自子任务
5. 主 Agent 等待全部子 Agent 完成后汇总并统一回复用户

这意味着 SunClaw 不只是“一个大模型回复器”，而是一个带状态、可路由、可分工、可汇总的 Agent 系统。

## 会话内指令

SunClaw 支持在聊天中直接切换 Agent：

- `/agent`
  查看当前会话实际生效的 Agent
- `/agent list`
  查看所有可用 Agent
- `/agent <id>`
  将当前会话切换到指定 Agent，例如 `/agent reviewer`
- `/agent default`
  显式切换到 ID 为 `default` 的 Agent
- `/agent clear`
  清除会话级切换，恢复自动路由
- `/new`
  重置当前会话上下文，开启新会话

## 快速开始

### 1. 环境要求

- Go `1.25.6` 或更高版本
- 至少一个可用的大模型 Provider 配置
- 如果需要浏览器工具，请准备 Chrome/Chromium 环境

### 2. 编译

```bash
git clone https://github.com/smallnest/goclaw.git
cd goclaw
go mod tidy
go build -o goclaw .
```

### 3. 初始化配置

首次运行时，如果本地不存在配置文件，程序会自动生成：

- `~/.goclaw/config.json`
- `~/.goclaw/skills/`

你也可以先运行交互式初始化：

```bash
./goclaw onboard
```

或者直接启动：

```bash
./goclaw start
```

如果配置文件不存在，程序会先生成一份默认配置模板。

### 4. 修改配置

重点关注以下配置项：

- `providers`
  配置实际使用的大模型 Provider 和 API Key
- `agents.list`
  定义 `default`、`main` 以及各专长 Agent
- `bindings`
  配置某个渠道 / 账号默认落到哪个 Agent
- `tools`
  控制 Shell、Browser、Memory、Cron 等工具能力
- `channels`
  配置企业微信、飞书、Telegram 等接入信息

示例：

```json
{
  "agents": {
    "defaults": {
      "model": "gpt-5.3-codex",
      "max_iterations": 50
    },
    "list": [
      {
        "id": "default",
        "name": "General Default",
        "default": true,
        "provider": "gemini"
      },
      {
        "id": "main",
        "name": "Main Agent",
        "provider": "codex",
        "system_prompt": "你是 SunClaw，负责理解用户意图、拆解任务、调度合适的 Agent 并行执行、最终汇总结果。"
      }
    ]
  },
  "bindings": [
    {
      "agent_id": "default",
      "match": {
        "channel": "wework",
        "account_id": "default"
      }
    }
  ]
}
```

完整配置可参考：

- `config.json`
- `internal/config.example.json`

### 5. 启动服务

```bash
./goclaw start
```

常用本地调试方式：

```bash
./goclaw tui
./goclaw agent --message "介绍一下你自己"
./goclaw config show
./goclaw health
```

## 常用命令

### 服务与诊断

- `goclaw start`
- `goclaw status`
- `goclaw health`
- `goclaw logs -f`
- `goclaw config show`

### 交互与会话

- `goclaw tui`
- `goclaw agent --message "你好"`
- `goclaw sessions list`
- `goclaw agents list`

### 渠道与网关

- `goclaw channels list`
- `goclaw channels status`
- `goclaw gateway run`
- `goclaw gateway status`

### Skills 与工具

- `goclaw skills list`
- `goclaw skills validate <skill-name>`
- `goclaw skills install <url-or-path>`
- `goclaw memory status`
- `goclaw browser status`

### 定时任务

- `goclaw cron status`
- `goclaw cron list`
- `goclaw cron add`
- `goclaw cron run <job-id>`

## Skills 系统

SunClaw 支持通过 `SKILL.md` 扩展 Agent 行为。技能本质上是可控、可组合的提示词和执行规范，适合把某一类固定工作流程沉淀下来。

Skills 加载目录优先级：

1. `~/.goclaw/skills/`
2. `${WORKSPACE}/skills/`
3. `./skills/`

常用命令：

```bash
./goclaw skills list
./goclaw skills search review
./goclaw skills validate <skill-name>
./goclaw skills install <url-or-path>
```

## 项目结构

```text
goclaw/
├── agent/          # Agent 核心逻辑、调度、路由、工具接入
├── channels/       # 聊天渠道接入
├── bus/            # 消息总线
├── config/         # 配置加载与校验
├── providers/      # 大模型 Provider
├── session/        # 会话管理
├── gateway/        # WebSocket Gateway
├── cli/            # 命令行入口
├── cron/           # 定时任务调度
├── memory/         # 记忆与检索
├── docs/           # 补充文档
└── main.go         # 主入口
```

## 推荐阅读

- [子 Agent 机制](docs/subagent.md)
- [ACP 文档](docs/acp.md)
- [快速开始](docs/guide/quickstart.md)
- [配置指南](docs/guide/config_guide.md)
- [工具与技能规范](docs/guide/tool-and-skill-spec.md)

## 常见问题

### 1. 为什么我切换了 `/agent default`，回复还是像主 Agent？

先确认两件事：

- 当前会话是否真的切到了 `default`
- `bindings` 是否把该渠道 / 账号默认绑定到了其他 Agent

现在的行为是：

- `/agent default`：显式切到 `default`
- `/agent clear`：清除会话切换，恢复自动路由

### 2. 子 Agent 执行完后为什么主 Agent 没回复？

当前逻辑已经修正为：

- 主 Agent 会在所有子 Agent 完成后汇总回复
- 如果子 Agent 没有产出可汇总内容，主 Agent 会直接回复“执行完毕”

### 3. 配置文件放哪里？

默认在：

- `~/.goclaw/config.json`

可通过命令行参数覆盖：

```bash
./goclaw start --config /path/to/config.json
```

### 4. 如何把 SunClaw 接到企业微信或飞书？

在 `channels.wework` 或 `channels.feishu` 中填入对应的凭证，并在 `bindings` 中指定默认路由 Agent，然后执行：

```bash
./goclaw start
```

### 5. 如何排查问题？

优先使用下面这组命令：

```bash
./goclaw status
./goclaw health
./goclaw channels status
./goclaw logs -f
```

## License

MIT
