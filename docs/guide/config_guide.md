# SunClaw 配置文件详细指南

本指南基于当前仓库里的实际实现整理，主要对应以下代码：

- `internal/core/config/schema.go`
- `internal/core/config/loader.go`
- `internal/core/config/validator.go`
- `internal/config.example.yaml`

如果你要改配置，先看这份文档，再对照 `config.yaml` 和 `internal/config.example.yaml`。

## 1. 配置文件放哪里，按什么顺序加载

SunClaw 当前的配置加载规则是：

1. 如果启动时显式传了 `--config` / `-c`，优先使用这个文件。
2. 否则先找 `~/.goclaw/config.yaml`。
3. 再找当前目录下的 `./config.yaml`。

命令示例：

```bash
sunclaw start
sunclaw start -c /abs/path/to/config.yaml
```

补充说明：

- 默认自动查找只查 `config.yaml`，不自动查 `config.json`。
- 如果你想用 JSON 配置文件，必须显式传 `--config /path/to/config.json`。
- 首次执行 `sunclaw start` 或 `sunclaw onboard` 时，如果 `~/.goclaw/config.yaml` 不存在，程序会自动生成一份模板。

## 2. 环境变量覆盖规则

配置支持环境变量覆盖，前缀是当前代码里的兼容前缀：

- `GOSKILLS_`

点号 `.` 会自动替换成下划线 `_`。

例如：

```bash
export GOSKILLS_PROVIDERS_OPENAI_API_KEY="sk-xxx"
export GOSKILLS_AGENTS_DEFAULTS_MODEL="gpt-5.3-codex"
```

建议：

- 敏感字段优先走环境变量，比如 API Key、Bot Secret、Webhook Token。
- 结构复杂的数组和对象仍然更适合写在 YAML 里。

## 3. 先看一份最小可运行配置

下面这份配置不接入聊天渠道，但可以支撑本地命令、TUI、Agent 调试：

```yaml
workspace:
  path: /abs/path/to/your/workspace

agents:
  defaults:
    model: gpt-5.3-codex
    max_iterations: 15
    temperature: 0.7
    max_tokens: 4096
    max_history_messages: 100

providers:
  openai:
    api_key: YOUR_OPENAI_API_KEY
    base_url: https://api.openai.com/v1
    timeout: 600

tools:
  filesystem:
    allowed_paths:
      - /abs/path/to/your/workspace
  shell:
    enabled: true
    timeout: 120
    denied_cmds:
      - rm -rf
      - dd
      - mkfs
  web:
    search_engine: travily
    timeout: 10
  browser:
    enabled: false
    headless: true
    timeout: 30
  cron:
    enabled: true
    store_path: ~/.goclaw/cron/jobs.json

memory:
  backend: builtin
  builtin:
    enabled: true
    auto_index: true

log:
  level: info
```

说明：

- `agents.list` 可以先不写。当前实现里，如果没有配置任何 Agent，会自动创建一个 `default` Agent，模型取 `agents.defaults.model`。
- 但只要你开始定义 `agents.list`，每个 Agent 都应该显式写出自己的 `id`，并且至少配置 `model` 或 `provider`。

## 4. 顶层结构总览

当前主配置结构如下：

```yaml
workspace:
agents:
bindings:
channels:
providers:
gateway:
tools:
approvals:
memory:
log:
skills:
acp:
```

你可以把这 11 个部分理解成：

- `workspace`: 工作区和技能目录的根
- `agents`: Agent 默认值和具体 Agent 列表
- `bindings`: 某个渠道/账号默认路由到哪个 Agent
- `channels`: 聊天渠道配置
- `providers`: 大模型提供商和轮换策略
- `gateway`: HTTP / WebSocket 网关
- `tools`: 工具开关和权限
- `approvals`: 工具审批策略
- `memory`: 记忆后端
- `log`: 日志
- `skills`: 技能附加配置
- `acp`: ACP 运行时与线程绑定

## 5. `workspace`

示例：

```yaml
workspace:
  path: /Users/you/.goclaw/workspace
```

字段说明：

- `workspace.path`
  工作区路径。必须是绝对路径。

行为说明：

- 不配置时，默认使用 `~/.goclaw/workspace`。
- 这里也是技能目录、默认记忆目录等的根路径来源。
- 校验器会尝试自动创建这个目录。

## 6. `agents`

### 6.1 `agents.defaults`

示例：

```yaml
agents:
  defaults:
    model: openrouter:anthropic/claude-opus-4-5
    max_iterations: 15
    temperature: 0.7
    max_tokens: 4096
    max_history_messages: 100
    subagents:
      max_concurrent: 3
      archive_after_minutes: 60
      model: ""
      thinking: ""
      timeout_seconds: 300
```

字段说明：

- `model`
  全局默认模型。必填。
- `max_iterations`
  单个 Agent 最大迭代次数。范围 `1-100`。
- `temperature`
  温度。范围 `0-2`。
- `max_tokens`
  默认输出 token 上限。范围 `1-128000`。
- `max_history_messages`
  单会话默认保留的历史消息条数。
- `subagents`
  全局子 Agent 默认参数。

当前代码默认值：

- `model`: `openrouter:anthropic/claude-opus-4-5`
- `max_iterations`: `15`
- `temperature`: `0.7`
- `max_tokens`: `4096`
- `max_history_messages`: `100`

### 6.2 `agents.list`

示例：

```yaml
agents:
  list:
    - id: default
      name: General Default
      default: true
      provider: openai
    - id: reviewer
      name: Reviewer
      provider: claude
      model: claude-sonnet-4-20250514
      workspace: /abs/path/to/repo
      system_prompt: |
        你是代码审查 Agent。
```

字段说明：

- `id`
  Agent 唯一 ID。必填。
- `name`
  显示名。
- `description`
  给编排层看的用途说明，建议写。
- `default`
  是否设为默认 Agent。
- `provider`
  Provider profile 名称，或者内置 provider 名称：`openai` / `anthropic` / `openrouter`。
- `model`
  当前 Agent 的模型。若填写，会覆盖 `providers.profiles[*].model`。
- `workspace`
  该 Agent 专用工作区；不填时继承 `workspace.path`。
- `identity`
  身份名称与 emoji。
- `system_prompt`
  自定义提示词。注意：当前实现里这是“完全替换”内置提示词，不是追加。
- `metadata`
  自定义元数据。
- `subagents`
  当前 Agent 的子 Agent 策略。

当前实现里的关键规则：

1. 如果 `agents.list` 为空，系统会自动创建一个 `default` Agent。
2. 如果你显式定义了某个 Agent，建议总是给它写 `provider` 或 `model`。
3. `provider` 的优先级高于全局 provider。
4. `model` 的优先级是：
   `agent.model > profile.model > agents.defaults.model`

### 6.3 `agents.list[].subagents`

示例：

```yaml
agents:
  list:
    - id: vibecoding
      provider: codex
      subagents:
        allow_agents:
          - architect
          - coder
          - reviewer
        allow_tools:
          - read_file
          - edit_file
          - run_shell
        deny_tools:
          - browser_click
        timeout_seconds: 300
```

字段说明：

- `allow_agents`
  允许当前 Agent 派发到哪些 Agent。
- `model`
  子 Agent 使用的默认模型。
- `thinking`
  子 Agent 思考等级。
- `timeout_seconds`
  单次子任务超时秒数，范围 `1-3600`。
- `allow_tools`
  白名单工具。
- `deny_tools`
  黑名单工具。

注意：

- `allow_tools` 和 `deny_tools` 不允许有重叠。
- 一旦配置了 `allow_tools`，就会走严格白名单。

## 7. `bindings`

示例：

```yaml
bindings:
  - agent_id: default
    match:
      channel: wework
      account_id: default
  - agent_id: reviewer
    match:
      channel: telegram
      account_id: bot2
```

作用：

- 指定“某个渠道 + 某个账号”默认路由到哪个 Agent。

字段说明：

- `agent_id`
  要绑定的 Agent ID，必须已经存在于 `agents.list` 中。
- `match.channel`
  渠道名，例如 `wework`、`telegram`。
- `match.account_id`
  账号 ID。

重要说明：

- 单账号模式下，运行时 account_id 会被规范化成 `default`。
- 因此单账号绑定建议显式写成 `account_id: default`。

## 8. `providers`

### 8.1 单一 Provider 配置

示例：

```yaml
providers:
  openai:
    api_key: YOUR_OPENAI_API_KEY
    base_url: https://api.openai.com/v1
    timeout: 600
  anthropic:
    api_key: ""
    base_url: ""
    timeout: 600
  openrouter:
    api_key: ""
    base_url: ""
    timeout: 600
    max_retries: 3
```

字段说明：

- `api_key`
  API Key。
- `base_url`
  OpenAI 兼容中转地址或官方地址。
- `timeout`
  超时时间，单位是秒。
- `max_retries`
  仅 OpenRouter 配置里使用。

校验规则：

- 至少要有一个 provider 的 `api_key` 不为空。
- API Key 长度至少 10 个字符，不能包含空格。

### 8.2 不写 `agent.provider` 时如何决定走哪个 provider

当前实现规则：

1. 先看 `agents.defaults.model` 的前缀。
2. 如果模型以 `openrouter:` 开头，走 OpenRouter。
3. 如果模型以 `anthropic:` 开头，或模型名本身像 `claude-*`，走 Anthropic。
4. 如果模型以 `openai:` 开头，或模型名像 `gpt-*`，走 OpenAI。
5. 如果模型前缀不明显，则按 API Key 存在顺序回退：
   `openrouter -> anthropic -> openai`

### 8.3 `providers.profiles`

示例：

```yaml
providers:
  profiles:
    - name: codex
      provider: openai
      api_key: YOUR_OPENAI_API_KEY
      base_url: https://api.openai.com/v1
      model: gpt-5.3-codex
      priority: 1
    - name: claude
      provider: anthropic
      api_key: YOUR_ANTHROPIC_API_KEY
      base_url: https://api.anthropic.com
      model: claude-sonnet-4-20250514
      priority: 1
    - name: gemini
      provider: openai
      api_key: YOUR_GEMINI_COMPAT_API_KEY
      base_url: https://your-gemini-compatible-endpoint/v1
      model: gemini-2.5-pro
      priority: 1
```

用途：

- 给不同 Agent 指定不同模型供应商。
- `agents.list[].provider` 就是引用这里的 `name`。

### 8.4 `providers.failover`

示例：

```yaml
providers:
  failover:
    enabled: true
    strategy: round_robin
    default_cooldown: 5m
    circuit_breaker:
      failure_threshold: 5
      timeout: 5m
  profiles:
    - name: openai-primary
      provider: openai
      api_key: YOUR_OPENAI_KEY_1
      base_url: https://api.openai.com/v1
      model: gpt-5.3-codex
      priority: 1
    - name: openai-backup
      provider: openai
      api_key: YOUR_OPENAI_KEY_2
      base_url: https://api.openai.com/v1
      model: gpt-5.3-codex
      priority: 2
```

字段说明：

- `enabled`
  是否启用轮换。
- `strategy`
  只能是 `round_robin`、`least_used`、`random`。
- `default_cooldown`
  冷却时间，推荐写 `5m` 这种带单位的 duration 字符串。
- `circuit_breaker.failure_threshold`
  连续失败多少次打开断路器。
- `circuit_breaker.timeout`
  断路器保持打开的时间，推荐写 `5m`。

注意：

- 只有在 `failover.enabled: true` 且 `profiles` 非空时，轮换才会真正启用。
- 如果只配置了一个 profile，实际上还是单 provider 行为。

## 9. `channels`

当前 schema 内置的渠道配置有：

- `telegram`
- `whatsapp`
- `weixin`
- `imessage`
- `feishu`
- `dingtalk`
- `qq`
- `wework`
- `infoflow`
- `gotify`

### 9.1 单账号和多账号的关系

每个渠道都支持两种写法：

1. 顶层单账号
2. `accounts` 多账号

示例：

```yaml
channels:
  telegram:
    enabled: true
    token: "123456:ABC"
```

或：

```yaml
channels:
  telegram:
    enabled: true
    accounts:
      bot1:
        enabled: true
        name: 主号
        token: "123456:ABC"
      bot2:
        enabled: true
        name: 备用号
        token: "654321:XYZ"
```

当前实现规则：

- 大多数渠道只要 `accounts` 非空，就优先走多账号配置。
- 企业微信稍微特殊：
  如果 `accounts` 非空，但没有任何一个账号成功注册，才会回退到顶层单账号配置。

建议：

- 新配置统一优先使用 `accounts`。
- 单账号也可以继续用顶层写法，但绑定时请记得用 `account_id: default`。

### 9.2 各渠道必填字段

#### Telegram

单账号必填：

- `channels.telegram.enabled: true`
- `channels.telegram.token`

多账号必填：

- `channels.telegram.accounts.<id>.enabled: true`
- `channels.telegram.accounts.<id>.token`

#### WhatsApp

必填：

- `bridge_url`

要求：

- 必须是绝对 URL。

#### Weixin

支持两种模式：

- `mode: bridge`
- `mode: direct`

Bridge 模式必填：

- `bridge_url`

Direct 模式必填：

- `mode: direct`
- `token`

Direct 模式可选：

- `base_url`
- `cdn_base_url`
- `proxy`

要求：

- `bridge_url` / `base_url` / `cdn_base_url` / `proxy` 如填写，必须是绝对 URL。

说明：

- `bridge` 模式保持现有 HTTP bridge 协议，适合继续复用外部 bridge。
- `direct` 模式直接对接腾讯 iLink API，通过 `getupdates/sendmessage/getuploadurl` 收发消息。
- `direct` 模式的消息发送依赖会话里的 `context_token`，因此更偏向“收到消息后继续回复”。
- 可以先运行 `sunclaw weixin login` 完成扫码登录，拿到 `token` 和 `base_url` 后再写入配置。

#### iMessage

必填：

- `bridge_url`

要求：

- 必须是绝对 URL。

#### Feishu

必填：

- `app_id`
- `app_secret`

可选：

- `encrypt_key`
- `verification_token`
- `webhook_port`
- `domain`
- `group_policy`
- `dm_policy`
- `cron_output_chat_id`

#### QQ

必填：

- `app_id`
- `app_secret`

#### WeWork

企业微信分两种模式。

Webhook 模式必填：

- `mode: webhook`
- `corp_id`
- `agent_id`
- 顶层单账号时填 `secret`
- 多账号时填 `app_secret`

WebSocket 模式必填：

- `mode: websocket`
- `bot_id`
- `bot_secret`

可选：

- `websocket_url`
- `token`
- `encoding_aes_key`
- `webhook_port`
- `allowed_ids`

重要说明：

- `mode` 不填时，当前实现默认按 `webhook` 处理。
- 顶层单账号 webhook 用的是 `secret`。
- `accounts` 里的 webhook 用的是 `app_secret`。
- 这是当前 schema 和装配逻辑里的历史兼容差异，配置时要特别注意。

企业微信 WebSocket 示例：

```yaml
channels:
  wework:
    enabled: true
    accounts:
      ai-bot-websocket:
        enabled: true
        name: WeWork AI Bot
        mode: websocket
        bot_id: your-bot-id
        bot_secret: your-bot-secret
        websocket_url: wss://openws.work.weixin.qq.com
        allowed_ids:
          - alice
          - bob
```

企业微信 Webhook 示例：

```yaml
channels:
  wework:
    enabled: true
    accounts:
      corp-webhook:
        enabled: true
        name: WeWork Corp
        mode: webhook
        corp_id: ww123456
        agent_id: 1000002
        app_secret: your-app-secret
        token: your-token
        encoding_aes_key: your-encoding-aes-key
        webhook_port: 8766
```

#### DingTalk

顶层单账号必填：

- `client_id`
- `secret`

多账号必填：

- `client_id`
- `client_secret`

这也是一个字段名差异点：

- 顶层用 `secret`
- `accounts` 里用 `client_secret`

#### Infoflow

必填：

- `webhook_url`
- `token`

可选：

- `aes_key`
- `webhook_port`

要求：

- `webhook_url` 必须是绝对 URL。

#### Gotify

必填：

- `server_url`
- `app_token`

可选：

- `priority`

要求：

- `server_url` 必须是绝对 URL。

### 9.3 `allowed_ids`

几乎所有渠道都支持：

```yaml
allowed_ids:
  - user1
  - group1
```

作用：

- 限制哪些用户或群可以与当前渠道账号交互。
- 为空时通常表示不限制。

## 10. `gateway`

示例：

```yaml
gateway:
  host: localhost
  port: 8080
  read_timeout: 30
  write_timeout: 30
  websocket:
    host: localhost
    port: 28789
    path: /ws
    enable_auth: false
    auth_token: ""
    ping_interval: 30s
    pong_timeout: 60s
    read_timeout: 60s
    write_timeout: 10s
```

字段说明：

- `gateway.host`
  HTTP 网关监听地址。
- `gateway.port`
  HTTP 网关端口，范围 `1024-65535`。
- `gateway.read_timeout`
- `gateway.write_timeout`
  当前实现按“秒数整数”使用，推荐写 `30`，不要写 `30s`。
- `gateway.websocket.*`
  WebSocket 子配置。

重要说明：

- `gateway.read_timeout` 和 `gateway.write_timeout` 虽然在 schema 里是 `time.Duration`，但当前实现是按“整数秒”处理的。
- `gateway.websocket.ping_interval`、`pong_timeout`、`read_timeout`、`write_timeout` 则推荐写标准 duration 字符串，如 `30s`、`60s`。

## 11. `tools`

示例：

```yaml
tools:
  filesystem:
    allowed_paths:
      - /abs/path/to/workspace
    denied_paths:
      - /etc
      - /root

  shell:
    enabled: true
    allowed_cmds: []
    denied_cmds:
      - rm -rf
      - dd
      - mkfs
      - format
    timeout: 120
    working_dir: ""
    sandbox:
      enabled: false
      image: goclaw/sandbox:latest
      workdir: /workspace
      remove: true
      network: none
      privileged: false

  web:
    search_api_key: ""
    search_engine: travily
    timeout: 10

  browser:
    enabled: false
    headless: true
    timeout: 30
    relay_url: ws://127.0.0.1:18789
    relay_mode: auto

  cron:
    enabled: true
    store_path: ~/.goclaw/cron/jobs.json
```

### 11.1 `tools.filesystem`

- `allowed_paths`
  允许访问的路径前缀。
- `denied_paths`
  拒绝访问的路径前缀。

### 11.2 `tools.shell`

- `enabled`
  是否启用。
- `allowed_cmds`
  白名单。
- `denied_cmds`
  黑名单。
- `timeout`
  单位秒。
- `working_dir`
  默认工作目录。
- `sandbox`
  Docker 沙箱配置。

当前校验器要求：

- `timeout` 范围 `1-3600`
- 如果 `shell.enabled: true`，`denied_cmds` 里必须覆盖这些危险命令：
  - `rm -rf`
  - `dd`
  - `mkfs`

### 11.3 `tools.web`

- `search_api_key`
- `search_engine`
  当前默认值是 `travily`。
- `timeout`
  单位秒，范围 `1-300`。

### 11.4 `tools.browser`

- `enabled`
- `headless`
- `timeout`
  单位秒，范围 `1-600`
- `relay_url`
- `relay_mode`
  当前支持 `auto`、`direct`、`relay`

### 11.5 `tools.cron`

- `enabled`
- `store_path`

## 12. `approvals`

示例：

```yaml
approvals:
  behavior: manual
  allowlist:
    - read_file
    - list_dir
```

字段说明：

- `behavior`
  当前设计值是 `auto`、`manual`、`prompt`。
- `allowlist`
  允许直接通过的工具列表。

## 13. `memory`

示例：

```yaml
memory:
  backend: builtin
  builtin:
    enabled: true
    database_path: ""
    auto_index: true
  qmd:
    command: qmd
    enabled: false
    include_default: true
    paths:
      - name: notes
        path: ~/notes
        pattern: '**/*.md'
    sessions:
      enabled: false
      export_dir: ~/.goclaw/sessions/export
      retention_days: 30
    update:
      interval: 5m
      on_boot: true
      embed_interval: 60m
      command_timeout: 30s
      update_timeout: 120s
    limits:
      max_results: 6
      max_snippet_chars: 700
      timeout_ms: 4000
```

字段说明：

- `memory.backend`
  只能是 `builtin` 或 `qmd`。
- `builtin`
  内置 SQLite 记忆。
- `qmd`
  外部 QMD 记忆引擎配置。

QMD 部分的单位：

- `update.interval`
- `update.embed_interval`
- `update.command_timeout`
- `update.update_timeout`

这些都推荐写成带单位的 duration 字符串，例如：

- `5m`
- `60m`
- `30s`

`limits.timeout_ms` 则是整数毫秒。

## 14. `log`

示例：

```yaml
log:
  level: info
  dir: ""
  split_by_day: false
  max_size_mb: 100
  max_backups: 7
  max_age_days: 30
  compress: true
```

字段说明：

- `level`
  `debug` / `info` / `warn` / `error`
- `dir`
  日志目录
- `split_by_day`
- `max_size_mb`
- `max_backups`
- `max_age_days`
- `compress`

默认值：

- `level: info`
- `split_by_day: false`
- `max_size_mb: 100`
- `max_backups: 7`
- `max_age_days: 30`
- `compress: true`

兼容性提醒：

- `schema.go` 注释里写的是 `~/.goclaw/logs`
- 但当前 CLI 启动流程里，若 `log.dir` 为空，默认日志目录仍然落在 `~/.sunclaw/logs`

如果你不想受历史兼容影响，建议显式配置：

```yaml
log:
  dir: /abs/path/to/logs
```

## 15. `skills`

`skills` 当前是原样透传的 map：

```yaml
skills:
  some_skill:
    enabled: true
    foo: bar
```

用途：

- 给技能系统预留扩展配置。
- 当前没有统一强校验；按具体技能约定使用。

## 16. `acp`

示例：

```yaml
acp:
  enabled: false
  backend: acp-go-sdk
  agent_path: /abs/path/to/acp-agent
  agent_args: []
  agent_env: []
  default_agent: main
  max_concurrent_sessions: 5
  idle_timeout_ms: 300000
  allowed_agents:
    - main
    - coding
  thread_bindings:
    "wework:default":
      enabled: true
      spawn_enabled: true
      idle_timeout_ms: 300000
      max_age_ms: 3600000
```

字段说明：

- `enabled`
  是否启用 ACP。
- `backend`
  ACP 运行时后端。
- `agent_path`
  ACP agent 可执行文件路径。
- `agent_args`
- `agent_env`
- `default_agent`
  默认 ACP Agent ID，不填时当前策略回退到 `main`。
- `max_concurrent_sessions`
- `idle_timeout_ms`
  单位毫秒。
- `allowed_agents`
  允许使用 ACP 的 Agent 列表。
- `thread_bindings`
  渠道级线程绑定策略。

`thread_bindings` 的 key 格式是：

```yaml
<channel>:<account_id>
```

例如：

```yaml
thread_bindings:
  "wework:default":
    enabled: true
    spawn_enabled: true
    idle_timeout_ms: 300000
    max_age_ms: 3600000
  "telegram:bot2":
    enabled: true
    spawn_enabled: false
    idle_timeout_ms: 600000
    max_age_ms: 7200000
```

## 17. 单位速查表

这是当前配置里最容易写错的一部分。

### 推荐写整数秒的字段

- `providers.openai.timeout`
- `providers.anthropic.timeout`
- `providers.openrouter.timeout`
- `tools.shell.timeout`
- `tools.web.timeout`
- `tools.browser.timeout`
- `gateway.read_timeout`
- `gateway.write_timeout`

### 推荐写 duration 字符串的字段

- `providers.failover.default_cooldown`
- `providers.failover.circuit_breaker.timeout`
- `gateway.websocket.ping_interval`
- `gateway.websocket.pong_timeout`
- `gateway.websocket.read_timeout`
- `gateway.websocket.write_timeout`
- `memory.qmd.update.interval`
- `memory.qmd.update.embed_interval`
- `memory.qmd.update.command_timeout`
- `memory.qmd.update.update_timeout`

### 推荐写整数毫秒的字段

- `memory.qmd.limits.timeout_ms`
- `acp.idle_timeout_ms`
- `acp.thread_bindings.*.idle_timeout_ms`
- `acp.thread_bindings.*.max_age_ms`

## 18. 常见配置坑

### 18.1 `workspace.path` 不是绝对路径

会直接校验失败。

正确示例：

```yaml
workspace:
  path: /Users/you/project/workspace
```

### 18.2 `config.json` 放在当前目录却没被自动加载

默认自动搜索只找 `config.yaml`。

如果你想用 JSON，请显式指定：

```bash
sunclaw start -c ./config.json
```

### 18.3 配了 `bindings`，但没生效

优先检查：

1. `agent_id` 是否真的存在
2. `channel` 是否写对
3. 单账号场景下 `account_id` 是否写成了 `default`

### 18.4 企业微信 / 钉钉账号字段名写错

这两个渠道存在顶层单账号和 `accounts` 字段名差异：

- 企业微信 webhook：
  顶层用 `secret`，账号配置里用 `app_secret`
- 钉钉：
  顶层用 `secret`，账号配置里用 `client_secret`

### 18.5 `agents.list` 写了 Agent，但没写 `provider` 或 `model`

建议总是给每个显式定义的 Agent 配上 `provider` 或 `model`。

### 18.6 同时写了顶层单账号和 `accounts`

当前实现里，多数渠道会优先使用 `accounts`，顶层配置只作为兼容回退。

建议：

- 新配置统一只保留一种写法，不要混用。

## 19. 推荐起步方式

如果你是第一次落地 SunClaw，建议按下面顺序配：

1. 先只配 `workspace`、`agents.defaults`、`providers`
2. 确认 `sunclaw start` 能正常启动
3. 再接一个最熟悉的渠道，例如 `wework` 或 `telegram`
4. 渠道稳定后，再加 `bindings`
5. 最后再开 `browser`、`shell`、`acp`、`qmd`

## 20. 配置示例索引

你可以同时参考这些文件：

- `internal/config.example.yaml`
- `config.yaml`
- `README.md`

如果只想先跑起来，优先从 `internal/config.example.yaml` 复制一份开始，再按本指南逐段修改。
