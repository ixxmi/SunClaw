# GoClaw 工具与技能编写规范

> 版本：1.0.0 · 更新：2026-02-27

---

## 目录

1. [概念区分](#1-概念区分)
2. [技能 Skill 编写规范](#2-技能skill编写规范)
3. [工具 Tool 编写规范](#3-工具tool编写规范)
4. [工具注册规范](#4-工具注册规范)
5. [选择指南](#5-选择指南)
6. [完整示例](#6-完整示例)

---

## 1. 概念区分

| 维度 | 技能（Skill） | 工具（Tool） |
|------|-------------|------------|
| **本质** | Markdown 提示词注入 | Go 代码，可执行逻辑 |
| **作用** | 告诉 LLM **怎么做** | 提供 LLM **能做什么** |
| **编写语言** | Markdown + YAML | Go |
| **生效方式** | 注入到 System Prompt | 注册到 ToolRegistry，LLM 直接调用 |
| **需要编译** | 不需要，修改立即生效 | 需要重新编译 |
| **适用场景** | 流程指导、命令示例、领域知识 | 需要程序化逻辑、类型安全、复杂处理 |

### 运行机制

```
【技能两阶段加载】

第一阶段（匹配）：
  用户消息 → LLM 扫描 Skills 摘要列表 → 调用 use_skill("技能名")

第二阶段（执行）：
  use_skill 触发 → 技能 SKILL.md 全文注入 System Prompt
  → LLM 读取技能中的操作指南 → 调用 run_shell / web_fetch 等工具执行
```

---

## 2. 技能（Skill）编写规范

### 2.1 目录结构

```
技能名/                     ← 目录名 = 技能名（小写+连字符）
├── SKILL.md               ← 必须，核心文件
├── scripts/               ← 可选，可执行脚本
│   └── my_script.py
├── references/            ← 可选，参考文档（按需加载）
│   └── api_docs.md
└── assets/                ← 可选，模板/资源文件
    └── template.html
```

禁止在技能目录中创建：README.md、CHANGELOG.md、INSTALLATION_GUIDE.md 等辅助文档。

### 2.2 技能存放路径（按优先级从低到高）

```
~/.goclaw/skills/技能名/SKILL.md      ← 全局用户目录（最低优先级）
{workspace}/skills/技能名/SKILL.md    ← 工作区目录
./skills/技能名/SKILL.md              ← 当前目录（最高优先级）
```

同名技能，后加载的覆盖先加载的。

### 2.3 SKILL.md 结构规范

```markdown
---
name: 技能名
description: 技能描述（触发机制，详见 2.4）
version: 1.0.0           # 可选
author: 作者名            # 可选
homepage: https://...    # 可选
metadata:
  openclaw:
    emoji: 🛠️            # 可选，界面显示图标
    always: false        # 可选，true=每次强制加载，false=按需触发
    requires:
      bins: [curl, jq]              # 可选，依赖的命令行工具
      env: [API_KEY]                # 可选，依赖的环境变量
      os: [darwin, linux, windows]  # 可选，支持的操作系统
      pythonPkgs: [requests]        # 可选，依赖的 Python 包
      nodePkgs: [typescript]        # 可选，依赖的 Node 包
    install:             # 可选，依赖安装配置
      - id: brew
        kind: brew       # brew | apt | npm | pip | uv | go
        formula: curl
        bins: [curl]
        label: "Install curl (brew)"
        os: [darwin]
---

# 技能正文（Markdown）

操作指南、命令示例、注意事项...
```

### 2.4 description 字段规范（最重要）

`description` 是 LLM 判断是否触发该技能的**唯一依据**（第一阶段只读摘要，不读正文）。

**必须包含：**
- 技能能做什么
- 什么情况下触发（触发词 / 场景）

**示例（差）：**
```yaml
description: 查询天气
```

**示例（好）：**
```yaml
description: 当用户询问天气、气温、天气预报、气候时使用。
  触发词：天气、气温、下雨、weather、forecast、温度。
  支持任意城市，无需 API Key。
```

### 2.5 name 字段命名规范

- 只允许：小写字母、数字、连字符 `-`
- 长度不超过 64 个字符
- 优先使用动词短语描述动作：`query-mysql`、`send-dingtalk`
- 按工具命名空间时加前缀：`gh-create-pr`、`k8s-deploy`

| 合法 | 非法 |
|------|------|
| `weather` | `Weather` |
| `mysql-query` | `mysql_query` |
| `github-pr` | `github pr` |

### 2.6 正文内容规范

#### 核心原则

1. **简洁优先**：LLM 已经很聪明，只写它不知道的信息
2. **示例驱动**：用代码示例代替冗长文字说明
3. **渐进披露**：核心内容在 SKILL.md，细节放 references/ 子文件
4. **正文 500 行以内**：超出则拆分到 references/ 子文件

#### 推荐正文结构

```markdown
# 技能名

## 快速开始（最常用操作）
[简洁代码示例]

## 常用场景
[按场景分组的命令示例]

## 注意事项
[关键提醒，非显而易见的内容]

## 参考文档
- 详细 API：见 [references/api.md](references/api.md)
- 高级用法：见 [references/advanced.md](references/advanced.md)
```

#### 内容放置原则

放正文：
- 核心工作流程
- 最常用命令的示例
- 关键注意事项（容易出错的地方）
- references/ 子文件的引用说明

放 references/（不放正文）：
- 完整的 API 文档
- 所有参数的详细说明
- 不常用的边缘场景

### 2.7 引用子文件规范

```markdown
## 高级功能

详细参数配置见 [references/config.md](references/config.md)
当用户需要处理大文件时，参考 [references/batch.md](references/batch.md)
```

- 引用层级最多一层：SKILL.md → references/xxx.md，不允许 references/ 下再引用子文件
- references/ 文件超过 100 行时，需在顶部添加目录（TOC）

---

## 3. 工具（Tool）编写规范

### 3.1 工具接口

所有工具必须实现 `tools.Tool` 接口（`agent/tools/base.go`）：

```go
type Tool interface {
    Name()        string
    Description() string
    Parameters()  map[string]interface{}
    Execute(ctx context.Context, params map[string]interface{}) (string, error)
}
```

### 3.2 文件存放位置

```
agent/tools/你的工具名.go
```

### 3.3 工具完整代码模板

```go
package tools

import (
    "context"
    "encoding/json"
    "fmt"
)

// MyTool 工具结构体
type MyTool struct {
    timeout int
    apiKey  string
}

// NewMyTool 构造函数
func NewMyTool(apiKey string, timeout int) *MyTool {
    if timeout <= 0 {
        timeout = 30
    }
    return &MyTool{
        apiKey:  apiKey,
        timeout: timeout,
    }
}

// Name 工具名称（唯一标识，snake_case）
func (t *MyTool) Name() string {
    return "my_tool"
}

// Description 工具描述（供 LLM 理解用途）
func (t *MyTool) Description() string {
    return "一句话说明工具能做什么，以及在什么场景下应该使用它。"
}

// Parameters JSON Schema 参数定义
func (t *MyTool) Parameters() map[string]interface{} {
    return map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "param1": map[string]interface{}{
                "type":        "string",
                "description": "参数1的说明",
            },
            "param2": map[string]interface{}{
                "type":        "integer",
                "description": "参数2的说明",
                "default":     10,
            },
        },
        "required": []string{"param1"},
    }
}

// Execute 执行工具逻辑
func (t *MyTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
    // 1. 参数提取与校验
    param1, ok := params["param1"].(string)
    if !ok || param1 == "" {
        return "", fmt.Errorf("param1 is required")
    }

    // 注意：JSON 数字默认解析为 float64
    param2 := 10
    if p, ok := params["param2"].(float64); ok {
        param2 = int(p)
    }

    // 2. 业务逻辑
    _ = param2

    // 3. 返回结构化 JSON
    result := map[string]interface{}{
        "status": "success",
        "data":   "结果内容",
    }
    data, err := json.Marshal(result)
    if err != nil {
        return "", fmt.Errorf("failed to marshal result: %w", err)
    }
    return string(data), nil
}
```

### 3.4 Name() 命名规范

- 使用 `snake_case`（小写 + 下划线）
- 格式：`动词_名词` 或 `名词_动作`

| 合法 | 非法 |
|------|------|
| `http_request` | `HttpRequest` |
| `send_email` | `send-email` |
| `query_database` | `queryDatabase` |

### 3.5 Description() 编写规范

LLM 依据此描述决定是否调用该工具，必须准确描述用途和场景：

```go
// 差
func (t *MyTool) Description() string {
    return "HTTP tool"
}

// 好
func (t *MyTool) Description() string {
    return "发送 HTTP 请求（GET/POST/PUT/DELETE），支持自定义请求头和请求体，" +
        "返回响应状态码和内容。适用于调用外部 REST API 或抓取网页数据。"
}
```

### 3.6 Parameters() JSON Schema 规范

```go
func (t *MyTool) Parameters() map[string]interface{} {
    return map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            // 字符串
            "url": map[string]interface{}{
                "type":        "string",
                "description": "目标 URL，必须以 http:// 或 https:// 开头",
            },
            // 枚举
            "method": map[string]interface{}{
                "type":    "string",
                "enum":    []string{"GET", "POST", "PUT", "DELETE"},
                "default": "GET",
            },
            // 整数
            "timeout": map[string]interface{}{
                "type":    "integer",
                "default": 30,
            },
            // 布尔
            "follow_redirects": map[string]interface{}{
                "type":    "boolean",
                "default": true,
            },
            // 对象（键值对）
            "headers": map[string]interface{}{
                "type":        "object",
                "description": "请求头，键值对格式",
            },
        },
        "required": []string{"url"},
    }
}
```

### 3.7 Execute() 规范

#### 参数类型处理（易错点）

```go
// 字符串
url, ok := params["url"].(string)
if !ok || url == "" {
    return "", fmt.Errorf("url is required")
}

// 数字：JSON 解析后为 float64，需手动转换
timeout := 30
if v, ok := params["timeout"].(float64); ok {
    timeout = int(v)
}

// 布尔
followRedirects := true
if v, ok := params["follow_redirects"].(bool); ok {
    followRedirects = v
}

// 对象
headers := map[string]string{}
if h, ok := params["headers"].(map[string]interface{}); ok {
    for k, v := range h {
        if vs, ok := v.(string); ok {
            headers[k] = vs
        }
    }
}
```

#### 错误处理规范

```go
// 包装错误，保留上下文
return "", fmt.Errorf("failed to connect: %w", err)

// 参数校验错误，明确说明
return "", fmt.Errorf("param 'url' is required and must start with http://")

// 禁止吞掉错误
if err != nil {
    return "", nil  // 错误！
}
```

#### 返回值规范

- 成功时返回结构化 JSON 字符串（便于 LLM 解析）
- 失败时返回 `("", error)`，error 信息要描述清楚问题
- 内容过长时截断（建议不超过 10 万字符）

```go
// 结构化返回（推荐）
result := map[string]interface{}{
    "success": true,
    "data":    responseBody,
    "status":  resp.StatusCode,
}
data, _ := json.Marshal(result)
return string(data), nil

// 纯文本返回（简单场景）
return fmt.Sprintf("操作成功，影响了 %d 行", rowsAffected), nil
```

#### 上下文取消处理

```go
// 将 ctx 传递给所有耗时操作
req, err := http.NewRequestWithContext(ctx, method, url, body)

// 循环中检查取消
select {
case <-ctx.Done():
    return "", ctx.Err()
default:
}
```

### 3.8 多工具封装模式（GetTools）

当一个功能域对应多个工具时，使用 `GetTools()` 封装：

```go
type FileSystemTool struct {
    allowedPaths []string
    workspace    string
}

// GetTools 返回该工具组的所有工具
func (t *FileSystemTool) GetTools() []Tool {
    return []Tool{
        NewBaseTool("read_file",  "读取文件内容", t.readFileParams(),  t.ReadFile),
        NewBaseTool("write_file", "写入文件内容", t.writeFileParams(), t.WriteFile),
        NewBaseTool("list_files", "列出目录内容", t.listFilesParams(), t.ListFiles),
    }
}
```

---

## 4. 工具注册规范

### 4.1 必须注册的三处位置

工具必须在以下三处同时注册（三个文件都是独立入口，都会启动 Agent）：

| 文件 | 对应命令 |
|------|---------|
| `cli/root.go` | `goclaw start`（服务模式） |
| `cli/agent.go` | `goclaw agent` |
| `cli/commands/tui.go` | `goclaw tui`（交互模式） |

### 4.2 注册代码模板

在每个文件中找到 `// 注册文件系统工具` 附近，添加：

```go
// 注册单个工具
myTool := tools.NewMyTool(cfg.Tools.MyTool.APIKey, 30)
if err := toolRegistry.RegisterExisting(myTool); err != nil {
    logger.Warn("Failed to register my_tool", zap.Error(err))
}

// 注册工具组（GetTools 模式）
myGroupTool := tools.NewMyGroupTool(cfg.Tools.MyGroup.Config)
for _, tool := range myGroupTool.GetTools() {
    if err := toolRegistry.RegisterExisting(tool); err != nil {
        logger.Warn("Failed to register tool", zap.String("tool", tool.Name()))
    }
}
```

### 4.3 配置集成（可选）

在 `config/schema.go` 中添加配置结构：

```go
type ToolsConfig struct {
    // ...existing fields...
    MyTool MyToolConfig `mapstructure:"my_tool" json:"my_tool"`
}

type MyToolConfig struct {
    APIKey  string `mapstructure:"api_key" json:"api_key"`
    Timeout int    `mapstructure:"timeout" json:"timeout"`
}
```

在 `~/.goclaw/config.json` 中配置：

```json
{
  "tools": {
    "my_tool": {
      "api_key": "your-api-key",
      "timeout": 30
    }
  }
}
```

---

## 5. 选择指南

```
需要实现新功能？
│
├─ 能用现有工具（run_shell / web_fetch / read_file）实现吗？
│   └─ 是 → 写技能（Skill）
│
├─ 需要程序化逻辑（类型处理 / 复杂计算 / 状态管理）？
│   └─ 是 → 写工具（Tool）
│
├─ 需要调用外部 SDK / 数据库驱动？
│   └─ 是 → 写工具（Tool）
│
└─ 需要指导 LLM 使用某个 CLI 工具或 API？
    └─ 是 → 写技能（Skill）
```

**黄金法则：** 优先写技能。技能不够用时，再补充 Go 工具。

---

## 6. 完整示例

### 6.1 技能示例：钉钉消息通知

目录结构：
```
~/.goclaw/skills/dingtalk-notify/
├── SKILL.md
└── references/
    └── message-types.md
```

**SKILL.md：**

```markdown
---
name: dingtalk-notify
description: 通过钉钉机器人 Webhook 发送通知消息。
  当用户需要发送钉钉消息、通知、告警时触发。
  触发词：发钉钉、钉钉通知、钉钉消息、dingtalk、发消息到钉钉群。
version: 1.0.0
author: xuechenxi
metadata:
  openclaw:
    emoji: 📫
    always: false
    requires:
      bins: [curl]
---

# 钉钉 Webhook 通知

## 快速发送文本消息

\`\`\`bash
curl -X POST "https://oapi.dingtalk.com/robot/send?access_token=YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"msgtype":"text","text":{"content":"消息内容"}}'
\`\`\`

## 发送 Markdown 消息

\`\`\`bash
curl -X POST "https://oapi.dingtalk.com/robot/send?access_token=YOUR_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "msgtype": "markdown",
    "markdown": { "title": "标题", "text": "## 标题\n\n正文内容" }
  }'
\`\`\`

## 注意事项

- access_token 从钉钉群机器人配置中获取
- 每分钟限发 20 条
- 更多消息类型见 [references/message-types.md](references/message-types.md)
```

---

### 6.2 工具示例：MySQL 数据库查询

**`agent/tools/database.go`：**

```go
package tools

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"
    _ "github.com/go-sql-driver/mysql"
)

type DatabaseTool struct {
    dsn string
}

func NewDatabaseTool(dsn string) *DatabaseTool {
    return &DatabaseTool{dsn: dsn}
}

func (t *DatabaseTool) Name() string { return "query_database" }

func (t *DatabaseTool) Description() string {
    return "执行 MySQL 数据库 SELECT 查询，返回 JSON 格式结果。" +
        "适用于：查询数据、统计分析、验证数据库内容。"
}

func (t *DatabaseTool) Parameters() map[string]interface{} {
    return map[string]interface{}{
        "type": "object",
        "properties": map[string]interface{}{
            "sql": map[string]interface{}{
                "type":        "string",
                "description": "要执行的 SELECT SQL 语句",
            },
            "limit": map[string]interface{}{
                "type":    "integer",
                "default": 100,
            },
        },
        "required": []string{"sql"},
    }
}

func (t *DatabaseTool) Execute(ctx context.Context, params map[string]interface{}) (string, error) {
    sqlStr, ok := params["sql"].(string)
    if !ok || sqlStr == "" {
        return "", fmt.Errorf("sql parameter is required")
    }

    limit := 100
    if v, ok := params["limit"].(float64); ok {
        limit = int(v)
    }

    db, err := sql.Open("mysql", t.dsn)
    if err != nil {
        return "", fmt.Errorf("failed to connect to database: %w", err)
    }
    defer db.Close()

    rows, err := db.QueryContext(ctx, sqlStr)
    if err != nil {
        return "", fmt.Errorf("query failed: %w", err)
    }
    defer rows.Close()

    cols, _ := rows.Columns()
    var results []map[string]interface{}
    count := 0

    for rows.Next() && count < limit {
        vals := make([]interface{}, len(cols))
        ptrs := make([]interface{}, len(cols))
        for i := range vals {
            ptrs[i] = &vals[i]
        }
        if err := rows.Scan(ptrs...); err != nil {
            continue
        }
        row := make(map[string]interface{})
        for i, col := range cols {
            row[col] = vals[i]
        }
        results = append(results, row)
        count++
    }

    out := map[string]interface{}{
        "columns": cols,
        "rows":    results,
        "count":   count,
    }
    data, _ := json.Marshal(out)
    return string(data), nil
}
```

**在 `cli/root.go`、`cli/agent.go`、`cli/commands/tui.go` 中注册：**

```go
// 注册数据库查询工具
if cfg.Tools.Database.DSN != "" {
    dbTool := tools.NewDatabaseTool(cfg.Tools.Database.DSN)
    if err := toolRegistry.RegisterExisting(dbTool); err != nil {
        logger.Warn("Failed to register query_database tool", zap.Error(err))
    }
}
```

