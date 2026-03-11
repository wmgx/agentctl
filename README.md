[English](./README_en.md)

# agentctl

将 AI CLI 工具（如 Claude Code）接入飞书等消息渠道的多渠道 Agent 框架。

> **当前支持**：飞书（Lark）IM 渠道
> **规划中**：更多 IM 平台、CLI 工具、通知渠道

---

## 核心机制

### 1. 三路意图路由

每条消息进入后，先由 AI 进行意图分类，再分发到对应处理路径：

```
收到消息
    │
    ├─ 直接对话（Direct）  → 单次 AI 回复，流式输出到卡片
    ├─ 建立会话（Session） → 弹出确认卡片 → 选工作目录 → 建群 → 持续对话
    └─ 系统操作（System）  → 查看活跃会话列表、系统状态等
```

分类由 Anthropic API 完成（使用轻量模型，速度快、成本低），分类失败时自动降级为直接对话。

---

### 2. Session 会话机制

Session 是 agentctl 的核心概念，每个 Session 对应一个飞书群 + 一个独立的 AI CLI 子进程。

**创建流程**：
1. 用户发送复杂任务消息（如「帮我重构 user.go」）
2. AI 判断为 Session 意图，弹出确认卡片，展示识别到的主题和理由
3. 用户确认后，弹出工作目录选择卡片
4. 自动建群、拉用户入群、转让群主
5. 在选定目录下启动 AI CLI 子进程，绑定到该群

**持续对话**：
- 群内每条消息都发送给对应的 AI CLI 子进程
- CLI Session ID 持久化，进程重启后可恢复上下文
- 并发保护：同一 Session 同时只处理一条消息

**空闲超时**：
- 超过 `idle_timeout_min` 分钟无活动的 Session 自动挂起
- 再次发消息时自动恢复

---

### 3. P2P 引用链升级

私聊中频繁引用回复时，上下文越来越长，agentctl 会自动检测并提示升级为群聊 Session。

**触发机制**：
- 追踪每个用户的引用链深度（内存 LRU，容量 1000 条）
- 深度达到 `chain_upgrade_threshold`（默认 4）时，在当前消息回复后弹出升级卡片
- 用户可选择「升级群聊」或「不再提示」

**升级流程**：
1. 从飞书 API 追溯历史消息链（最多 10 层）
2. 格式化为 `[用户]: ...` / `[Claude]: ...` 对话历史
3. 建群、拉人、转让群主
4. 合并转发历史消息到新群（保留可读性）
5. 将历史上下文注入 AI，获取 CLI Session ID
6. 创建 Session，用户无需重复描述背景

---

### 4. 流式输出 & 交互卡片

所有 AI 回复均通过飞书交互式卡片展示，支持实时更新：

- **流式更新**：每秒最多更新一次卡片（避免 API 限流）
- **简洁模式**：配置 `compact_stream: true` 隐藏工具执行过程和代码块，只显示思考和结论
- **完成状态**：显示总耗时、Token 用量和费用（Input / Output / Cost）

> 💡 推荐启用简洁模式，卡片内容减少 70%-90%，更清爽易读。详见 [配置说明](docs/config-compact-stream.md)

---

### 5. 危险工具审批

Session 中若 AI 要执行危险操作（如 `rm`、`git push`），会先暂停并向群内发送审批卡片：

- 用户点击「批准」才继续执行
- 点击「拒绝」则跳过该工具调用
- 审批超时（5 分钟）自动跳过

危险工具通过配置项 `dangerous_tools` 定义，支持关键词匹配。

---

### 6. 定时任务

内置 Cron 调度器，支持周期性执行 AI 任务并将结果推送到指定飞书群。

---

## 快速开始

### 安装

```bash
curl -fsSL https://raw.githubusercontent.com/wmgx/agentctl/main/install.sh | bash
```

安装完成后会自动注册系统守护进程（macOS 用 launchd，Linux 用 systemd），开机自启。

首次运行会进入**交互式配置向导**，引导填写飞书应用信息和 Anthropic API Key。

### 手动安装

```bash
git clone https://github.com/wmgx/agentctl.git
cd agentctl
go build -o agentctl ./cmd/server
./agentctl --config config.json
```

### 更新

```bash
bash install.sh update
```

拉取最新 tag，重新编译，自动重启服务。`config.json` 不受影响。

---

## 配置项详解

配置文件为 JSON 格式，默认路径 `~/.agentctl/config.json`。

### 飞书应用（必填）

```json
"feishu": {
  "app_id": "cli_xxxx",
  "app_secret": "xxxx",
  "bot_name": "ClaudeBot"
}
```

| 字段 | 说明 | 必填 |
|------|------|------|
| `app_id` | 飞书开放平台应用 ID | ✅ |
| `app_secret` | 飞书应用 Secret | ✅ |
| `bot_name` | Bot 显示名称（用于群名生成等） | 否，默认 `ClaudeBot` |

**飞书应用所需权限**：
- `im:message` — 读取消息
- `im:message:send_as_bot` — 发送消息
- `im:chat` — 读取群信息
- `im:chat:create` — 创建群

---

### Anthropic（意图分类用）

```json
"anthropic": {
  "api_key": "sk-ant-xxxx",
  "model": "claude-haiku-4-5-20250929",
  "base_url": "",
  "auth_token": ""
}
```

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `api_key` | Anthropic API Key | — |
| `model` | 意图分类使用的模型，推荐用轻量模型 | `claude-haiku-4-5-20250929` |
| `base_url` | API 代理地址，留空则直连官方 | `https://api.anthropic.com` |
| `auth_token` | 代理鉴权 Token，留空则使用 `api_key` | — |

> 此配置**仅用于意图分类**，AI 主体能力由 `claude_cli_path` 指向的 CLI 工具提供。

---

### 工作目录

```json
"default_cwd": "/path/to/your/workspace",
"repos": {
  "order": "/path/to/order-service",
  "contract": "/path/to/contract-service"
}
```

| 字段 | 说明 |
|------|------|
| `default_cwd` | 新建 Session 时的默认工作目录 |
| `repos` | 预定义仓库路径，创建 Session 时可通过卡片快速选择 |

---

### AI CLI 工具

```json
"claude_cli_path": "claude",
"session_model": "claude-sonnet-4-5"
```

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `claude_cli_path` | AI CLI 可执行文件路径或命令名 | `claude` |
| `session_model` | Session 对话使用的模型（传给 CLI 的 `--model` 参数） | `claude-sonnet-4-5` |

---

### Session 行为

```json
"idle_timeout_min": 30,
"bot_open_id": "ou_xxxx",
"chain_upgrade_threshold": 4
```

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `idle_timeout_min` | Session 空闲超时时间（分钟），超时后自动挂起 | `30` |
| `bot_open_id` | Bot 自身的飞书 open_id，用于在引用链中区分 Bot 回复和用户消息 | — |
| `chain_upgrade_threshold` | P2P 引用链触发升级群聊的深度阈值 | `4` |

> `bot_open_id` 可在飞书开放平台「应用信息」页面获取，不填则引用链中 Bot 消息无法被正确标记角色。

---

### 危险工具拦截

```json
"dangerous_tools": ["rm ", "git push", "git reset"]
```

| 字段 | 说明 |
|------|------|
| `dangerous_tools` | 关键词列表，AI 工具调用中包含这些关键词时触发人工审批 |

匹配方式：工具名 + 工具参数拼接后做大小写不敏感的子串匹配。

---

### 日志清理

```json
"log_retention_days": 7
```

| 字段 | 说明 | 默认值 |
|------|------|--------|
| `log_retention_days` | 日志文件保留天数，超过后自动删除 | `7` |

清理策略：
- 启动时立即扫描一次，清理历史遗留的过期日志
- 之后每天凌晨 3 点自动执行
- 仅删除 `.log` 文件，目录结构保留

---

### 完整配置示例

```json
{
  "feishu": {
    "app_id": "cli_xxxx",
    "app_secret": "xxxx",
    "bot_name": "AgentBot"
  },
  "anthropic": {
    "api_key": "sk-ant-xxxx",
    "model": "claude-haiku-4-5-20250929",
    "base_url": "",
    "auth_token": ""
  },
  "default_cwd": "/home/user/workspace",
  "repos": {
    "order": "/home/user/order-service",
    "contract": "/home/user/contract-service"
  },
  "idle_timeout_min": 30,
  "dangerous_tools": ["rm ", "git push", "git reset", "drop table"],
  "claude_cli_path": "claude",
  "session_model": "claude-sonnet-4-5",
  "bot_open_id": "ou_xxxx",
  "chain_upgrade_threshold": 4
}
```

---

## 服务管理

| 平台 | 后端 | 常用命令 |
|------|------|---------|
| macOS | launchd | `launchctl list \| grep agentctl` |
| Linux | systemd (user) | `systemctl --user status agentctl` |
| Windows | ❌ 暂不支持 | 建议使用 WSL2 |

日志位置：`~/.agentctl/log/`

---

## 项目结构

```
agentctl/
├── cmd/server/         # 程序入口
├── internal/
│   ├── claude/         # AI CLI 子进程适配器 & tmux 会话管理
│   ├── config/         # 配置加载 & 交互式首次配置向导
│   ├── cron/           # 定时任务调度
│   ├── feishu/         # 飞书客户端、事件处理、卡片模板、引用链追踪
│   ├── intent/         # 意图分类（Direct / Session / System）
│   ├── router/         # 消息路由 & P2P 引用链升级
│   └── session/        # Session 生命周期管理
├── .github/workflows/  # CI/CD：自动编译 & 多平台 Release
├── config.example.json # 配置模板
└── install.sh          # 安装 / 更新 / 卸载脚本
```

---

## License

MIT
