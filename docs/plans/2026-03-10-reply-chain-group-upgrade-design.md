# 私聊引用链自动升级群聊设计

## 目标

在私聊（P2P）场景下，当用户与 Bot 的引用对话链深度达到 4 轮时，提示用户是否升级为群聊。确认后自动建群、合并转发历史消息、并将历史对话注入新 Claude session。

## 触发条件

- 仅限 P2P 私聊（`chat_type == "p2p"`）
- 消息携带 `ParentMessageID`（即用户引用了上一条消息回复）
- 引用链深度 >= 4

## 数据流

```
P2P 消息到达（有 ParentMessageID）
  ↓
ReplyChainTracker.Track(senderID, msgID, parentMsgID)
  → 内存命中：直接追加
  → 未命中：调 GetMessage API 向上追溯，重建链（最多 10 层）
  → 返回 depth
  ↓
该 senderID 是否已 dismissed？
  是 → 直接走正常 router 逻辑
  ↓
depth >= 4？
  否 → 正常走 router 逻辑
  是 → 发确认卡片：「对话已延伸 4 轮，是否升级为群聊继续？」
        [确认升级] [继续私聊]
```

### 用户点「确认升级」

1. `CreateGroup`（群名取第一条消息的文本摘要）
2. `AddMember`（拉入用户）
3. `MergeForwardMessages`（把链上所有 msgID 合并转发到新群）
4. 获取链上所有消息 text + sender，组装 `[]Turn{role, text}`
5. 在新群发一条「历史上下文已注入，继续聊吧 👋」
6. 创建 Session，通过逐条 `adapter.RunOnce` 把 user/assistant 历史轮次注入，形成真实对话历史（方式 B）
7. `ReplyChainTracker.Reset(senderID)`

### 用户点「继续私聊」

1. `ReplyChainTracker.Dismiss(senderID)` —— 打 dismissed 标记，后续不再触发提醒
2. 当前消息正常交给 Claude 处理并回复

## 核心组件

### ReplyChainTracker（`internal/feishu/chain.go`）

```go
type ChainEntry struct {
    MsgIDs     []string  // 链上消息 ID，按时间顺序
    LastActive time.Time
    Dismissed  bool      // 用户选择继续私聊后标记
}

type ReplyChainTracker struct {
    cap     int
    mu      sync.RWMutex
    entries map[string]*ChainEntry  // key: senderID
    order   []string                // LRU 顺序（最近在后）
}
```

方法：
- `Track(senderID, msgID, parentMsgID string) int` — 追踪链，返回当前深度
- `BuildChainFromAPI(ctx, parentMsgID) []string` — 向上追溯，最多 10 层
- `GetChain(senderID) []string` — 获取链上所有 msgID
- `Reset(senderID)` — 清空链（建群后调用）
- `Dismiss(senderID)` — 标记 dismissed，不再触发
- `IsDismissed(senderID) bool`
- `evict()` — 超过 cap 时淘汰最久未活跃的 entry

容量：默认 cap=1000，无需持久化，重启后从零开始。

### Feishu Client 新增方法

```go
// 获取单条消息（用于向上追溯链路）
GetMessage(ctx, messageID) (text, parentMsgID string, senderID string, err error)

// 合并转发多条消息到目标群
MergeForwardMessages(ctx context.Context, messageIDs []string, toChatID string) error
```

### Session 历史注入

在 `router.go` 的 `handleChainUpgrade` 中：

```go
// 组装历史轮次
turns := buildTurns(chainMsgs, botOpenID)
// 逐条注入（user turn 调 RunOnce，assistant turn 用 --print 模式）
for _, turn := range turns {
    adapter.RunOnce(ctx, turn.Text, sess.CLISessionID, turn.Role == "user")
}
```

## 涉及改动的文件

| 文件 | 改动内容 |
|------|---------|
| `internal/feishu/event.go` | `IncomingMessage` 增加 `ParentMessageID` 字段，从 `msg.ParentId` 提取 |
| `internal/feishu/chain.go` | **新建**：`ReplyChainTracker`，LRU + dismissed 标记 |
| `internal/feishu/client.go` | 新增 `GetMessage`、`MergeForwardMessages` 方法 |
| `internal/router/router.go` | P2P 消息前置检查链深度；新增 `handleChainUpgrade` 和确认卡片流程 |
| `internal/feishu/card.go` | 新增升级提示卡片 `ChainUpgradeCard` |

## 不在范围内（YAGNI）

- 链路持久化（重启后从零计数即可）
- 群聊内的引用链检测
- 自定义触发深度（固定 4 轮）
