# 会话确认卡合并 + 流式中断 设计文档

**日期**: 2026-03-11
**状态**: 已确认，待实施

---

## 背景

两个独立需求：

1. **合并确认卡**：识别到新会话意图后，原来需要两步交互（确认建群 → 选择工作目录），用户觉得繁琐，希望合并为一张卡片。
2. **流式中断**：Claude 在群聊/私聊里流式回复时，用户无法中途打断。希望在卡片里加"停止"按钮，点击后立刻终止当前 Claude 进程，保留已输出内容并标记为"已中断"。

---

## Feature 1：会话确认卡合并

### 目标

将 `SessionConfirmCard`（确认建群）和 `CwdSelectionCard`（选择工作目录）合并为一张卡片，用户一步完成"是否建群 + 选目录"两个决策。

### 卡片结构

```
┌──────────────────────────────────────────────────────┐
│  🤔 需要建立独立会话吗？                               │
├──────────────────────────────────────────────────────┤
│  **主题**：xxx                                        │
│  **分析**：xxx                                        │
├──────────────────────────────────────────────────────┤
│  快速选择预设目录（有 repos 配置时才显示此区域）：        │
│  [~/projects/foo]  [~/work/bar]                      │
├──────────────────────────────────────────────────────┤
│  form (cwd_form):                                    │
│  ┌────────────────────────────────────────────┐     │
│  │ 工作目录（留空使用默认路径）                  │     │
│  └────────────────────────────────────────────┘     │
│  [✅ 建立群聊会话 (form_submit)]  [💬 直接回复就好]   │
└──────────────────────────────────────────────────────┘
```

### 交互逻辑

| 用户操作 | action | 数据来源 |
|---------|--------|---------|
| 点快捷目录按钮 | `confirm_session_with_cwd` | `value["cwd"]` = 预设路径 |
| 点"建立群聊"（form_submit） | `confirm_session_with_cwd` | `FormValue["custom_cwd"]`（空则用 `DefaultCwd`） |
| 点"直接回复" | `deny_session` | 无 |

### 代码变更

**`internal/feishu/card.go`**

- `SessionConfirmCard(topic, reason, repos, defaultCwd, requestID)` — 新增 `repos` 参数，函数内部：
  - 有 repos 时渲染快捷按钮行（action=`confirm_session_with_cwd`）
  - 底部始终渲染 form（input name=`custom_cwd` + form_submit 按钮 action=`confirm_session_with_cwd`）
  - "直接回复"按钮保持原 action=`deny_session`
- 删除或保留（供其他场景使用）`CwdSelectionCard` — 目前仅在 `selectCwdForUpgrade` 中还在使用，保留

**`internal/router/router.go`**

- `handleSession`：监听的 action 从 `confirm_session` 改为 `confirm_session_with_cwd`，直接从 action 里提取 cwd，调用 `createSession`
- 删除 `startSessionCreation` 函数（逻辑内联到 `handleSession`）
- `SessionConfirmCard` 调用传入 `r.cfg.Repos`

**`cmd/server/main.go`**

- `OnCardAction` handler：新增 `case "confirm_session_with_cwd"` 返回 `SessionConfirmCardDone(true)`
- 删除对 `confirm_session` 的处理

---

## Feature 2：流式输出中断

### 目标

在 Claude 流式回复进行中，卡片底部展示"🛑 停止"按钮。点击后：
- 立刻 kill Claude 进程（通过 context cancel → tmux kill）
- 卡片更新为"已中断"状态，保留已输出的文本
- 群聊 session 的 `CLISessionID` 保持不变，下一条消息可正常 `--resume`

### 技术方案

```
调用 adapter.Run 前：
  abortID = uuid.New()
  runCtx, runCancel = context.WithCancel(ctx)
  abortCh = pending.Wait(abortID)
  go func() {
    select {
    case <-abortCh:   runCancel()  // 用户点停止
    case <-runCtx.Done():          // Run 自然结束
    }
  }()

adapter.Run(runCtx, ...)  // 阻塞

Run 结束后：
  pending.Resolve(abortID, noop)  // 清理 pending，终止监听 goroutine
```

当 `runCancel()` 被调用时：
- `tailUntilDone` 的 `ctx.Done()` case 触发，返回 `ctx.Err()`
- `ExecStream` 的 `defer` 执行 `tmuxKill(name)`，Claude 进程被杀

### 卡片变更

**`internal/feishu/card.go`**

- `StreamingCardWithElapsed(content, isComplete, tokenInfo, elapsedSec, abortID)` — 新增 `abortID` 参数
  - `isComplete == false && abortID != ""` 时，在 elements 末尾添加 action 区域，包含"🛑 停止"按钮
  - `isComplete == true` 或 `abortID == ""` 时，不显示停止按钮
- 新增 `StreamingCardAborted(content, elapsedSec)` — 返回"已中断"状态卡片（橙色 header）
- `StreamingCard(content, isComplete, tokenInfo)` 保持签名不变（内部不传 abortID，向后兼容）

**`internal/session/handler.go`**

- `HandleMessage`：创建 `runCtx, runCancel, abortID`，注册 abort goroutine，传 abortID 给 `StreamingCard`，Run 完成后判断是否被中断并更新卡片

**`internal/router/router.go`**

- `handleDirect` 的 dialogLoop：同上，每轮创建 abortID，传给 `StreamingCard`，Run 完成后清理 pending

**`cmd/server/main.go`**

- `OnCardAction`：新增 `case "stop_stream"` — 直接 `pendingAction.Resolve(requestID, ActionResult{Action: "stop_stream"})`，不需要返回新卡片（后台 goroutine 处理后会 UpdateCard）

### 中断后的 session 状态

- **群聊 session**：`CLISessionID` 在 `session_init` 事件中更新，中断不会回滚它。下条消息可正常 `--resume`。
- **私聊 direct**：`resumeSessionID` 是 dialogLoop 内的局部变量，中断后该变量随 goroutine 结束而消失，下条消息重新开始（acceptable，私聊本来就无持久 session）。

---

## 变更文件汇总

| 文件 | 改动类型 | 说明 |
|------|---------|------|
| `internal/feishu/card.go` | 修改 | `SessionConfirmCard` 加 repos 参数；`StreamingCardWithElapsed` 加 abortID；新增 `StreamingCardAborted` |
| `internal/router/router.go` | 修改 | `handleSession` 监听新 action；删除 `startSessionCreation`；`handleDirect` 加中断逻辑 |
| `internal/session/handler.go` | 修改 | `HandleMessage` 加中断逻辑 |
| `cmd/server/main.go` | 修改 | 新增 `confirm_session_with_cwd` 和 `stop_stream` action 处理 |

---

## 不变更的内容

- `CwdSelectionCard`：保留，`selectCwdForUpgrade`（chain upgrade 流程）仍使用它
- `PendingAction` 结构：不变，复用现有机制
- `claude/adapter.go`、`claude/tmux.go`：不变，通过 context cancel 触发已有的 tmux kill 逻辑
