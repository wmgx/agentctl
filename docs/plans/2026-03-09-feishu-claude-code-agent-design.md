# 飞书 + Claude Code 远程操控系统 设计文档

> **日期**：2026-03-09
> **状态**：已审批
> **方案**：CLI 子进程模式（方案 A）

## 一、目标

构建一个 Go 后台服务，打通飞书与 Claude Code CLI，实现通过飞书远程操控 Claude Code。通过拉群的方式建立独立 session，支持流式输出、交互卡片、定时任务、飞书 MCP 全量集成。

## 二、核心决策

| 维度 | 决策 | 理由 |
|------|------|------|
| 部署模式 | 同机部署，Go 服务 fork claude CLI 子进程 | 稳定，功能完整 |
| 用户范围 | 仅自己使用，无多用户隔离 | 个人工具，简化设计 |
| Claude 集成 | CLI 子进程 + stream-json | 官方 CLI 最稳定，不依赖第三方 SDK |
| 存储 | JSON 文件 | 数据量极小，零依赖 |
| 飞书通道 | WebSocket 长连接 | 无需公网域名 |
| 飞书 MCP | 全量集成（文档/Wiki/多维表格/云盘/群聊） | 通过 claude CLI 原生 MCP 配置加载 |
| 交互方式 | 语义化（自然语言驱动） | 不记命令，用 Claude 做意图分类 |

## 三、整体架构

```
┌──────────────────────────────────────────────────────────┐
│                      飞书平台                             │
│                                                           │
│  ┌─────────┐   ┌─────────┐   ┌─────────┐                │
│  │ 私聊Bot  │   │Session群1│   │Session群2│   ...         │
│  │(路由入口)│   │(独立会话) │   │(独立会话) │               │
│  └────┬─────┘   └────┬─────┘   └────┬─────┘              │
└───────┼──────────────┼──────────────┼────────────────────┘
        │              │              │
        │    WebSocket 长连接（单连接收所有消息）
        │              │              │
┌───────▼──────────────▼──────────────▼────────────────────┐
│                   Go 后台服务（单进程）                    │
│                                                           │
│  ┌────────────┐  ┌──────────────┐  ┌──────────────────┐  │
│  │ EventRouter│  │SessionManager│  │  FeishuClient    │  │
│  │            │  │              │  │                  │  │
│  │ 三路路由    │  │ chatId→      │  │ 创建群/拉人      │  │
│  │ A:即时指令  │  │ Session 映射 │  │ 发消息/更新卡片   │  │
│  │ B:Session  │  │ 进程生命周期  │  │ 交互回调处理     │  │
│  │ C:系统管理  │  │ 超时挂起/恢复│  │                  │  │
│  └────────────┘  └──────┬───────┘  └──────────────────┘  │
│                         │                                  │
│  ┌──────────┐    ┌──────▼───────┐   ┌──────────────┐     │
│  │ Intent   │    │ CLIAdapter   │   │ CronScheduler│     │
│  │Classifier│    │              │   │              │     │
│  │          │    │ fork claude  │   │ robfig/cron  │     │
│  │ haiku API│    │ --stream-json│   │ 定时触发      │     │
│  │ 无状态    │    │ --resume     │   │ fork claude  │     │
│  └──────────┘    │ 解析事件流    │   └──────────────┘     │
│                  └──────────────┘                          │
│                                                           │
│  ┌──────────────────────────────────────────────────┐    │
│  │              Store (JSON 文件)                     │    │
│  │  sessions.json / config.json / cron_jobs.json     │    │
│  └──────────────────────────────────────────────────┘    │
└───────────────────────────────────────────────────────────┘
```

**只有一个飞书 Bot 应用**，通过私聊接收路由消息，通过群聊处理 Session 对话。Bot 通过一个 WebSocket 连接接收所有消息，靠 `chat_type`（p2p/group）区分路由。

## 四、数据模型

### 4.1 存储结构

```
~/.agent-for-im/
├── config.json          # 全局配置
├── data/
│   ├── sessions.json    # 所有 session 信息
│   └── cron_jobs.json   # 定时任务
└── logs/
    └── app.log          # 运行日志
```

### 4.2 Session

```go
type Session struct {
    ID            string    // uuid
    ChatID        string    // 飞书群 chat_id
    Name          string    // 群名/session名，如 "审查 order 模块"
    Tags          []string  // 用于复用筛选，如 ["order", "review", "go"]
    WorkingDir    string    // 代码仓库路径
    CLISessionID  string    // claude --resume 用的 session_id
    Status        string    // active / suspended / closed
    CreatedAt     time.Time
    LastActiveAt  time.Time
}
```

### 4.3 Config

```go
type Config struct {
    Feishu struct {
        AppID     string
        AppSecret string
        BotName   string
    }
    Anthropic struct {
        APIKey    string // Anthropic API Key
        Model     string // 意图分类模型，默认 claude-haiku-4-5-20250929
        BaseURL   string // 代理地址，不配置则使用官方 https://api.anthropic.com
        AuthToken string // 代理鉴权 token，不配置则用 APIKey
    }
    DefaultCwd     string            // 默认工作目录
    Repos          map[string]string  // 别名→路径，如 {"order": "/path/to/order"}
    IdleTimeoutMin int               // 空闲挂起时间，默认30分钟
    DangerousTools []string          // 需审批的工具模式，如 ["rm", "git push"]
    ClaudeCLIPath  string            // claude CLI 路径，默认 "claude"
}
```

### 4.4 CronJob

```go
type CronJob struct {
    ID         string   // uuid
    Name       string   // "每日新闻"
    Cron       string   // cron 表达式, "0 9 * * *"
    Prompt     string   // 发给 Claude 的 prompt
    WorkingDir string   // 工作目录（可选）
    TargetChat string   // 结果发到哪个群（必填，不填则只执行不发送）
    Tags       []string // 自动打到 session 上的 tags
    Enabled    bool
}
```

### 4.5 Tag 机制

- **自动生成**：创建 session 时由意图分类器从用户消息中提取关键词
- **手动管理**：自然语言操作，如"给这个会话加个性能标签"
- **复用匹配**：按 tags 匹配度 + 最近活跃时间排序，列出候选让用户选择

## 五、主 Bot 三路路由（私聊触发）

```
用户私聊 Bot 发消息
  │
  ├─ 意图分类（Anthropic API, haiku, 无状态, ~500 tokens，支持代理）
  │
  ├─ [A] 即时指令 → 主Bot直接执行，原地文本回复
  │   特征：一问一答，不需要代码库上下文，不需要多轮
  │   示例："帮我申请个数据库权限" "翻译一下这段话"
  │   执行：简单问答→直接调API；需要工具→fork一次claude CLI（不建session）
  │   回复：私聊中直接文本回复
  │
  ├─ [B] Session 对话 → 路由到已有群 或 创建新群
  │   特征：需要持续操作代码，多轮对话
  │   示例："帮我审查 order 模块的代码" "写一个新的 API 接口"
  │   流程：匹配已有session → 有候选则文本提示/无候选则创建新群
  │
  └─ [C] 系统管理 → 私聊中直接处理，文本回复
      特征：管理本系统自身
      示例："我有哪些会话" "停掉每日新闻" "系统状态"
```

### 意图分类器

直接调 Anthropic API（haiku 模型），支持配置代理（`base_url` + `auth_token`），每次独立调用，无上下文积累：

```
System Prompt（固定 ~300 tokens）:
  你是意图分类器。根据用户消息和现有sessions列表返回JSON：
  {"intent": "direct|session|system", "params": {...}}

User Message: 当前消息 + 现有 sessions 摘要
```

上下文永远固定大小，不会膨胀。

## 六、Session 群对话流程

```
用户在 Session 群发消息
  │
  ├─ SessionManager 查找 chatId → Session
  │
  ├─ 状态判断
  │   ├─ active / suspended → fork claude CLI（suspended 自动 resume）
  │   └─ closed → 文本回复"此会话已关闭"
  │
  ├─ fork: claude -p "用户消息" \
  │     --cwd $working_dir \
  │     --output-format stream-json \
  │     --resume $cli_session_id
  │
  ├─ 解析 stream-json 事件流
  │   ├─ system → 存储 CLISessionID
  │   ├─ text delta → 更新飞书卡片内容（节流 1次/秒）
  │   ├─ tool_use → 判断危险工具
  │   │   ├─ 安全 → 自动批准
  │   │   └─ 危险 → 发审批卡片，阻塞等待用户点击
  │   ├─ tool_result → 追加工具结果到卡片
  │   └─ result → 卡片定稿 + token 统计
  │
  └─ 更新 Session.LastActiveAt
```

## 七、创建 Session 流程

```
用户: "帮我看看 order 模块的性能问题"
  │
  ├─ 意图分类 → intent: session
  │
  ├─ 匹配已有 sessions（按 tags + lastActiveAt）
  │   ├─ 有候选 → 文本消息列出候选 + 提示"回复序号复用，或回复'新建'"
  │   └─ 无候选 → 进入创建流程
  │
  ├─ 发送工作目录选择卡片（按钮）
  │   [order-service] [contract-service] [默认目录] [自定义路径]
  │
  ├─ 用户选定目录
  │
  ├─ 创建飞书群: "[Claude] order性能分析"
  ├─ 拉用户进群
  ├─ 创建 Session 记录（tags 自动提取）
  ├─ 新群发送欢迎消息
  └─ 将原始问题转发到新群，触发首次对话
```

## 八、交互分级策略

| 场景 | 交互方式 | 理由 |
|------|----------|------|
| Claude 流式回复 | **卡片**（streaming 更新） | 核心体验，需流式 |
| 危险工具审批 | **卡片按钮**（approve/deny） | 二选一点击 |
| 选择工作目录 | **卡片按钮** | 有限选项点选 |
| 创建 session / 定时任务确认 | **卡片按钮** | 确认/取消 |
| 列出 sessions / 定时任务 | **文本消息** | 纯信息展示 |
| 关闭会话、启停任务 | **文本消息** | 操作反馈 |
| 路由结果、错误提示 | **文本消息** | 简单告知 |

**总结**：需要用户做选择/确认 → 卡片按钮；纯信息或操作反馈 → 文本消息。

## 九、飞书卡片设计

### 9.1 流式输出卡片

- Header: 蓝色 "Claude 回复中..." → 完成后变绿色 "Claude 回复完成"
- Body: Markdown 内容，每秒更新一次
- Footer: 生成中状态 / 完成后 token 统计

### 9.2 危险工具审批卡片

- Header: 橙色 "需要确认操作"
- Body: 展示工具名和参数（如命令内容）
- Actions: [批准] [拒绝] 按钮

### 9.3 工作目录选择卡片

- 列出 Config.Repos 中的所有别名按钮
- [默认目录] 按钮
- [输入自定义路径] 按钮

## 十、飞书 MCP 集成

不在 Go 服务里实现 MCP 协议。通过 claude CLI 原生 MCP 配置（`~/.claude/settings.json` 或 `.mcp.json`）加载飞书 MCP Server，每个 session 自动拥有飞书 API 能力。

可用工具：`feishu_doc`（文档CRUD）、`feishu_wiki`（知识库）、`feishu_drive`（云盘）、`feishu_bitable`（多维表格）、`feishu_chat`（群聊信息）。

## 十一、定时任务机制

### 调度

Go 服务内置 cron 调度器（`robfig/cron`），到达触发时间后：
1. fork claude CLI 执行 Prompt
2. 判断 TargetChat：有指定群→将结果发到该群；无→仅记录日志
3. 结果以文本消息发送（非流式）

### 管理

语义化操作，自然语言驱动：
- "每天早上9点帮我整理 Hacker News 热门文章" → 意图分类为 add_cron → 发确认卡片
- "我有哪些定时任务" → 文本列出
- "停掉每日新闻" → 文本确认

## 十二、系统生命周期

### 启动

1. 加载 config.json
2. 加载 sessions.json → 内存
3. 建立飞书 WebSocket 长连接
4. 注册消息事件 + 卡片回调处理器
5. 启动 cron 调度器
6. 启动空闲检查定时器（每分钟）

### 空闲挂起

CLI 子进程是短生命周期（每条消息 fork 一次，执行完自动退出）。"挂起"只是标记 session 状态为 suspended，影响路由匹配排序。超过更长时间（如 7 天）自动 close。

### 优雅关闭

1. 停止接收新消息
2. 等待正在执行的 CLI 子进程完成（最多 30s）
3. 持久化 sessions.json
4. 关闭飞书 WebSocket 连接

## 十三、Go 模块划分

```
agentctl/
├── cmd/
│   └── server/
│       └── main.go            # 入口
├── internal/
│   ├── config/
│   │   └── config.go          # 配置加载与数据结构
│   ├── feishu/
│   │   ├── client.go          # 飞书 API 封装（创建群/发消息/更新卡片）
│   │   ├── event.go           # WebSocket 事件监听与分发
│   │   ├── card.go            # 卡片模板构建
│   │   └── callback.go        # 卡片交互回调处理
│   ├── session/
│   │   ├── manager.go         # Session 生命周期管理
│   │   ├── store.go           # JSON 文件持久化
│   │   └── model.go           # Session 数据结构
│   ├── router/
│   │   └── router.go          # 主Bot三路路由（即时/session/系统）
│   ├── intent/
│   │   ├── classifier.go      # Anthropic API (haiku) 意图分类
│   │   └── model.go           # Intent 结构定义
│   ├── claude/
│   │   ├── adapter.go         # CLI 子进程管理与事件解析
│   │   └── stream.go          # stream-json 解析器
│   ├── permission/
│   │   └── gateway.go         # 危险工具审批（卡片按钮→阻塞/放行）
│   └── cron/
│       ├── scheduler.go       # cron 调度器
│       └── model.go           # CronJob 数据结构
├── config.example.json
└── go.mod
```

## 十四、CLI 执行层：tmux 方案

### 14.1 问题背景

`exec.Command` 直接调用 Claude CLI 存在 TTY 依赖问题：CLI 可能需要伪终端环境才能正常运行，导致子进程挂死或长时间无响应。

### 14.2 方案：tmux 会话化执行

用 tmux 替代 `exec.Command` 作为 Claude CLI 的运行容器。每次调用 CLI 在独立 tmux session 中执行，通过 `pipe-pane` 捕获输出，Go 侧轮询式 tail 实时读取解析。

```
之前：Go → exec.Command("claude", args...) → stdout pipe → 解析
现在：Go → tmux new-session → send-keys "bash script.sh" → pipe-pane → 输出文件 → Go tail → 解析
```

### 14.3 脚本化执行

为避免 shell 转义问题，将 prompt 写入临时文件，生成 bash 脚本通过 `$(cat prompt.txt)` 安全读取：

```bash
#!/bin/bash
export ANTHROPIC_BASE_URL='...'
export ANTHROPIC_AUTH_TOKEN='...'
PROMPT=$(cat '/path/to/prompt.txt')
echo '<<CC_START>>'
claude -p "$PROMPT" --output-format text --max-turns 1
echo "<<CC_END:$?>>"
```

使用 `<<CC_START>>` 和 `<<CC_END:N>>` 标记界定有效输出区域，过滤 shell prompt、命令回显等噪声。

### 14.4 输出捕获

- `tmux pipe-pane -o` 将终端输出追加到日志文件
- Go 用轮询式 tail（100ms 间隔）实时读取
- ANSI 转义码通过正则剥离
- 标记之间的内容即为 CLI 输出

### 14.5 tmux session 生命周期

| 类型 | 命名规则 | 生命周期 |
|------|---------|---------|
| RunOnce（意图分类/直接对话） | `cc-once-{uuid[:8]}` | 执行完立即销毁 |
| Run（session 流式对话） | `cc-stream-{uuid[:8]}` | 执行完立即销毁 |

后台 goroutine 每 5 分钟扫描，超过 1h 无活动的 tmux session 自动清理（兜底防泄漏）。

### 14.6 文件结构

```
~/.agent-for-im/
├── data/
│   └── tmux/          # tmux 临时文件目录
│       ├── cc-once-abc12345.log      # 输出文件
│       ├── cc-once-abc12345.sh       # 脚本文件
│       └── cc-once-abc12345.prompt   # prompt 文件
```

### 14.7 接口不变

`Adapter.RunOnce` 和 `Adapter.Run` 的外部签名不变，仅内部实现切换到 tmux。`NewAdapter` 增加 `dataDir` 参数。

## 十五、依赖

| 库 | 用途 |
|----|------|
| `github.com/larksuite/oapi-sdk-go/v3` | 飞书官方 Go SDK |
| `github.com/robfig/cron/v3` | Cron 调度器 |
| `github.com/google/uuid` | UUID 生成 |
| `claude` CLI | Claude Code 执行（通过 tmux 会话） |
| `tmux` | CLI 执行容器（提供 PTY 环境） |

## 十六、飞书应用权限

| 权限 | 用途 |
|------|------|
| `im:chat` | 创建群聊 |
| `im:chat.member:write` | 拉人入群 |
| `im:message:send_as_bot` | 发消息/更新卡片 |
| `im:message` | 接收消息事件 |
| `im:message.group_at_msg` | 群@消息 |
| `im:message.p2p_msg` | 私聊消息 |
