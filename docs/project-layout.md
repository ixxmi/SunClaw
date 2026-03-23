# Project Layout

仓库已按三层职责重新整理，避免业务核心、运行时装配和平台细节全部散落在顶层。

## 当前布局

```text
SunClaw/
├── cmd/
│   └── goclaw/              # 可执行入口
├── internal/
│   ├── app/
│   │   ├── cli/             # CLI 装配层
│   │   └── gateway/         # Gateway、HTTP/WebSocket 入口
│   ├── core/
│   │   ├── acp/             # ACP runtime 与协议适配
│   │   ├── agent/           # Agent 编排与工具调度
│   │   ├── bus/             # 消息总线
│   │   ├── channels/        # 各聊天渠道适配器
│   │   ├── config/          # 配置模型、加载与校验
│   │   ├── cron/            # 定时任务
│   │   ├── memory/          # 记忆与检索
│   │   ├── providers/       # LLM provider 与 OAuth
│   │   └── session/         # 会话状态与持久化
│   ├── logger/              # 通用日志能力
│   ├── platform/
│   │   ├── errors/          # 错误分类与日志封装
│   │   └── pairing/         # 配对流程与状态存储
│   └── workspace/           # 内置工作区模板
├── ui/                      # 前端资源与嵌入产物
├── docs/                    # 文档
└── docker/                  # 容器相关资源
```

## 放置规则

- `cmd/` 只放启动入口，不承载业务逻辑。
- `internal/app/` 负责把核心能力组装成可运行的 CLI、Gateway、后台服务。
- `internal/core/` 放跨运行模式复用的主业务能力。
- `internal/platform/` 放依赖环境或基础设施的支撑模块。
- 新增大模块时，优先判断它属于 `app`、`core` 还是 `platform`，不要再回到顶层平铺。

## 迁移原则

- 对外构建入口统一使用 `./cmd/goclaw`。
- 业务代码优先依赖 `internal/core/*`，避免在不同入口层之间横向耦合。
- 文档如果引用源码路径，优先使用当前目录结构；历史设计稿可以逐步补齐。
