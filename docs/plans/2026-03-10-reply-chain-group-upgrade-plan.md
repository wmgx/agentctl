# 私聊引用链自动升级群聊 实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** 私聊引用链深度 >= 4 时提示用户升级群聊，确认后建群、合并转发历史消息、注入对话历史到新 Claude session。

**Architecture:**
- 新增 `ReplyChainTracker`（LRU 内存，cap=1000）追踪 P2P 引用链深度；链路不在内存时调飞书 API 向上追溯
- 确认卡片用现有 `PendingAction` 机制等待用户响应
- 历史对话格式化为首条消息注入新 session，后续走 `--resume CLISessionID`

**Tech Stack:** Go, 飞书 oapi-sdk-go v3, 内存 LRU（手写，不引入外部库）

---

### Task 1: 提取 ParentMessageID

**Files:**
- Modify: `internal/feishu/event.go`

**Step 1: 在 `IncomingMessage` 加字段**

```go
type IncomingMessage struct {
    ChatID          string
    MessageID       string
    ParentMessageID string  // 新增：引用的父消息 ID，私聊引用时非空
    SenderID        string
    Text            string
    MsgType         string
    ChatType        string
}
```

**Step 2: 在事件处理中提取 `ParentId`**

在 `event.go` 的 `OnP2MessageReceiveV1` handler 里，`incoming` 赋值处加：

```go
if msg.ParentId != nil {
    incoming.ParentMessageID = *msg.ParentId
}
```

完整的 `incoming` 赋值块（替换原有的）：

```go
incoming := IncomingMessage{
    ChatID:    *msg.ChatId,
    MessageID: *msg.MessageId,
    SenderID:  *sender.SenderId.OpenId,
    MsgType:   *msg.MessageType,
    ChatType:  *msg.ChatType,
}
if msg.ParentId != nil {
    incoming.ParentMessageID = *msg.ParentId
}
```

**Step 3: 构建并确认编译通过**

```bash
go build ./...
```
Expected: 无报错

**Step 4: Commit**

```bash
git add internal/feishu/event.go
git commit -m "feat: extract ParentMessageID from feishu message event"
```

---

### Task 2: 实现 ReplyChainTracker

**Files:**
- Create: `internal/feishu/chain.go`

**Step 1: 写数据结构和构造函数**

```go
package feishu

import (
    "sync"
    "time"
)

const defaultChainCap = 1000

// ChainEntry 记录一个 sender 的引用链状态
type ChainEntry struct {
    MsgIDs     []string  // 按时间顺序的消息 ID 列表
    LastActive time.Time
    Dismissed  bool      // 用户选择继续私聊后为 true，不再触发升级提示
}

// ReplyChainTracker 追踪 P2P 私聊的引用链深度，使用 LRU 淘汰策略
type ReplyChainTracker struct {
    cap     int
    mu      sync.RWMutex
    entries map[string]*ChainEntry // key: senderID
    order   []string               // LRU 顺序，最近活跃的在末尾
}

func NewReplyChainTracker(cap int) *ReplyChainTracker {
    if cap <= 0 {
        cap = defaultChainCap
    }
    return &ReplyChainTracker{
        cap:     cap,
        entries: make(map[string]*ChainEntry),
    }
}
```

**Step 2: 实现 Track 方法**

```go
// Track 追踪一条新消息。返回当前引用链深度（1 表示独立消息，无引用）。
// 如果 parentMsgID 为空，视为新对话，重置该 sender 的链。
func (t *ReplyChainTracker) Track(senderID, msgID, parentMsgID string) int {
    t.mu.Lock()
    defer t.mu.Unlock()

    // 无引用：重置链
    if parentMsgID == "" {
        t.resetLocked(senderID)
        entry := &ChainEntry{
            MsgIDs:     []string{msgID},
            LastActive: time.Now(),
        }
        t.entries[senderID] = entry
        t.touchLocked(senderID)
        return 1
    }

    entry, exists := t.entries[senderID]
    if !exists {
        // 内存中没有该 sender 的链，先创建占位（调用方需额外追溯历史）
        entry = &ChainEntry{
            MsgIDs:     []string{msgID},
            LastActive: time.Now(),
        }
        t.entries[senderID] = entry
        t.touchLocked(senderID)
        return 1
    }

    // 检查 parentMsgID 是否在当前链里
    found := false
    for _, id := range entry.MsgIDs {
        if id == parentMsgID {
            found = true
            break
        }
    }

    if found {
        entry.MsgIDs = append(entry.MsgIDs, msgID)
    } else {
        // parentMsgID 不在内存中，追加到现有链末尾（调用方应已通过 API 追溯并调 PrependChain）
        entry.MsgIDs = append(entry.MsgIDs, msgID)
    }
    entry.LastActive = time.Now()
    t.touchLocked(senderID)
    t.evictLocked()
    return len(entry.MsgIDs)
}
```

**Step 3: 实现 PrependChain / GetChain / Reset / Dismiss / IsDismissed**

```go
// PrependChain 将向上追溯得到的历史消息 ID 前置到链里（用于内存未命中的场景）
func (t *ReplyChainTracker) PrependChain(senderID string, ancestorIDs []string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    entry, exists := t.entries[senderID]
    if !exists {
        return
    }
    entry.MsgIDs = append(ancestorIDs, entry.MsgIDs...)
}

// GetChain 返回该 sender 链上所有消息 ID 的副本
func (t *ReplyChainTracker) GetChain(senderID string) []string {
    t.mu.RLock()
    defer t.mu.RUnlock()
    entry, exists := t.entries[senderID]
    if !exists {
        return nil
    }
    result := make([]string, len(entry.MsgIDs))
    copy(result, entry.MsgIDs)
    return result
}

// Reset 清除该 sender 的链（建群后调用）
func (t *ReplyChainTracker) Reset(senderID string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    t.resetLocked(senderID)
}

// Dismiss 标记该 sender 不再触发升级提示，同时清空链
func (t *ReplyChainTracker) Dismiss(senderID string) {
    t.mu.Lock()
    defer t.mu.Unlock()
    entry := &ChainEntry{
        LastActive: time.Now(),
        Dismissed:  true,
    }
    t.entries[senderID] = entry
    t.touchLocked(senderID)
}

// IsDismissed 返回该 sender 是否已选择不升级
func (t *ReplyChainTracker) IsDismissed(senderID string) bool {
    t.mu.RLock()
    defer t.mu.RUnlock()
    entry, exists := t.entries[senderID]
    return exists && entry.Dismissed
}
```

**Step 4: 实现 LRU 内部方法**

```go
func (t *ReplyChainTracker) resetLocked(senderID string) {
    delete(t.entries, senderID)
    for i, id := range t.order {
        if id == senderID {
            t.order = append(t.order[:i], t.order[i+1:]...)
            break
        }
    }
}

// touchLocked 将 senderID 移到 order 末尾（最近活跃）
func (t *ReplyChainTracker) touchLocked(senderID string) {
    for i, id := range t.order {
        if id == senderID {
            t.order = append(t.order[:i], t.order[i+1:]...)
            break
        }
    }
    t.order = append(t.order, senderID)
}

// evictLocked 超过 cap 时淘汰最久未活跃的 entry
func (t *ReplyChainTracker) evictLocked() {
    for len(t.entries) > t.cap && len(t.order) > 0 {
        oldest := t.order[0]
        t.order = t.order[1:]
        delete(t.entries, oldest)
    }
}
```

**Step 5: 构建确认编译通过**

```bash
go build ./...
```
Expected: 无报错

**Step 6: Commit**

```bash
git add internal/feishu/chain.go
git commit -m "feat: add ReplyChainTracker with LRU eviction"
```

---

### Task 3: Feishu Client 新增 GetMessage 和 MergeForwardMessages

**Files:**
- Modify: `internal/feishu/client.go`

**Step 1: 新增 MessageInfo 结构体和 GetMessage 方法**

在 `client.go` 末尾追加：

```go
type MessageInfo struct {
    MessageID string
    ParentID  string
    SenderID  string
    Text      string
}

// GetMessage 获取单条消息的基本信息，用于向上追溯引用链
func (c *Client) GetMessage(ctx context.Context, messageID string) (*MessageInfo, error) {
    req := larkim.NewGetMessageReqBuilder().
        MessageId(messageID).
        Build()

    resp, err := c.api.Im.Message.Get(ctx, req)
    if err != nil {
        return nil, fmt.Errorf("get message: %w", err)
    }
    if !resp.Success() {
        return nil, fmt.Errorf("get message failed: %s", resp.Msg)
    }
    if len(resp.Data.Items) == 0 {
        return nil, fmt.Errorf("message not found: %s", messageID)
    }

    item := resp.Data.Items[0]
    info := &MessageInfo{
        MessageID: *item.MessageId,
    }
    if item.ParentId != nil {
        info.ParentID = *item.ParentId
    }
    if item.Sender != nil && item.Sender.Id != nil && item.Sender.Id.OpenId != nil {
        info.SenderID = *item.Sender.Id.OpenId
    }
    if item.Body != nil && item.Body.Content != nil {
        info.Text = extractText(*item.Body.Content)
    }
    return info, nil
}
```

**Step 2: 新增 MergeForwardMessages 方法**

```go
// MergeForwardMessages 将多条消息合并转发到目标群，生成「聊天记录」卡片
func (c *Client) MergeForwardMessages(ctx context.Context, messageIDs []string, toChatID string) error {
    req := larkim.NewMergeForwardMessageReqBuilder().
        ReceiveIdType("chat_id").
        Body(larkim.NewMergeForwardMessageReqBodyBuilder().
            ReceiveId(toChatID).
            MessageIdList(messageIDs).
            Build()).
        Build()

    resp, err := c.api.Im.Message.MergeForward(ctx, req)
    if err != nil {
        return fmt.Errorf("merge forward: %w", err)
    }
    if !resp.Success() {
        return fmt.Errorf("merge forward failed: %s", resp.Msg)
    }
    return nil
}
```

**Step 3: 构建确认编译通过**

```bash
go build ./...
```
Expected: 无报错（如果 SDK 方法名不对，按编译错误修正方法名）

**Step 4: Commit**

```bash
git add internal/feishu/client.go
git commit -m "feat: add GetMessage and MergeForwardMessages to feishu client"
```

---

### Task 4: 新增升级确认卡片

**Files:**
- Modify: `internal/feishu/card.go`

**Step 1: 新增 ChainUpgradeCard 函数**

在 `card.go` 末尾追加：

```go
// ChainUpgradeCard 生成引用链升级群聊的确认卡片
func ChainUpgradeCard(depth int, requestID string) map[string]interface{} {
    return map[string]interface{}{
        "header": map[string]interface{}{
            "title":    map[string]string{"tag": "plain_text", "content": "💬 对话较长，是否升级为群聊？"},
            "template": "wathet",
        },
        "elements": []interface{}{
            map[string]interface{}{
                "tag":     "markdown",
                "content": fmt.Sprintf("当前对话已延伸 **%d 轮**引用。\n升级为群聊后，历史对话将被转发并注入到新会话上下文中，Claude 可直接继续。", depth),
            },
            map[string]interface{}{
                "tag": "action",
                "actions": []interface{}{
                    map[string]interface{}{
                        "tag":  "button",
                        "text": map[string]string{"tag": "plain_text", "content": "🚀 升级为群聊"},
                        "type": "primary",
                        "value": map[string]string{
                            "action":     "upgrade_group",
                            "request_id": requestID,
                        },
                    },
                    map[string]interface{}{
                        "tag":  "button",
                        "text": map[string]string{"tag": "plain_text", "content": "继续私聊"},
                        "type": "default",
                        "value": map[string]string{
                            "action":     "dismiss_upgrade",
                            "request_id": requestID,
                        },
                    },
                },
            },
        },
    }
}
```

**Step 2: 构建确认编译通过**

```bash
go build ./...
```

**Step 3: Commit**

```bash
git add internal/feishu/card.go
git commit -m "feat: add ChainUpgradeCard for reply chain group upgrade prompt"
```

---

### Task 5: Router 集成链路检测与升级流程

**Files:**
- Modify: `internal/router/router.go`

**Step 1: Router 结构体加入 chainTracker 字段**

```go
type Router struct {
    cfg          *config.Config
    feishuCli    *feishu.Client
    classifier   *intent.Classifier
    store        *session.Store
    adapter      *claude.Adapter
    pending      *feishu.PendingAction
    chainTracker *feishu.ReplyChainTracker  // 新增
}

func New(cfg *config.Config, feishuCli *feishu.Client, classifier *intent.Classifier,
    store *session.Store, adapter *claude.Adapter, pending *feishu.PendingAction) *Router {
    return &Router{
        cfg:          cfg,
        feishuCli:    feishuCli,
        classifier:   classifier,
        store:        store,
        adapter:      adapter,
        pending:      pending,
        chainTracker: feishu.NewReplyChainTracker(1000),  // 新增
    }
}
```

**Step 2: 在 HandleRouterMessage 开头加 P2P 引用链检测**

在 `HandleRouterMessage` 函数的 `if msg.Text == ""` 检查之后、`r.classifier.Classify` 之前插入：

```go
// P2P 引用链深度检测
if msg.ChatType == "p2p" {
    if r.checkChainUpgrade(ctx, msg) {
        return // 已触发升级流程，本条消息暂不处理
    }
}
```

**Step 3: 实现 checkChainUpgrade 方法**

```go
const chainUpgradeThreshold = 4

// checkChainUpgrade 检测 P2P 引用链深度，达到阈值时发升级提示卡片。
// 返回 true 表示已触发升级流程（调用方应 return），false 表示继续正常处理。
func (r *Router) checkChainUpgrade(ctx context.Context, msg feishu.IncomingMessage) bool {
    if msg.ParentMessageID == "" {
        // 无引用，重置链
        r.chainTracker.Track(msg.SenderID, msg.MessageID, "")
        return false
    }

    if r.chainTracker.IsDismissed(msg.SenderID) {
        return false
    }

    depth := r.chainTracker.Track(msg.SenderID, msg.MessageID, msg.ParentMessageID)

    // 如果 depth==1 说明 parentMsgID 不在内存，尝试向上追溯
    if depth == 1 && msg.ParentMessageID != "" {
        ancestors := r.buildChainFromAPI(ctx, msg.ParentMessageID)
        if len(ancestors) > 0 {
            r.chainTracker.PrependChain(msg.SenderID, ancestors)
            depth = len(ancestors) + 1
        }
    }

    if depth < chainUpgradeThreshold {
        return false
    }

    // 触发升级提示
    requestID := uuid.New().String()
    card := feishu.ChainUpgradeCard(depth, requestID)
    r.feishuCli.SendCard(ctx, msg.ChatID, card)

    ch := r.pending.Wait(requestID)
    go func() {
        select {
        case action := <-ch:
            switch action.Action {
            case "upgrade_group":
                r.handleChainUpgrade(ctx, msg)
            case "dismiss_upgrade":
                r.chainTracker.Dismiss(msg.SenderID)
                // 继续处理当前消息
                r.HandleRouterMessage(ctx, feishu.IncomingMessage{
                    ChatID:    msg.ChatID,
                    MessageID: msg.MessageID,
                    SenderID:  msg.SenderID,
                    Text:      msg.Text,
                    MsgType:   msg.MsgType,
                    ChatType:  msg.ChatType,
                    // ParentMessageID 置空，避免再次触发链检测
                })
            }
        case <-time.After(10 * time.Minute):
            log.Printf("[chain] upgrade prompt timeout for sender %s", msg.SenderID)
        }
    }()

    return true
}
```

**Step 4: 实现 buildChainFromAPI（向上追溯历史）**

```go
// buildChainFromAPI 从给定消息 ID 向上追溯引用链，返回祖先消息 ID 列表（从旧到新）
// 最多追溯 10 层，防止无限循环
func (r *Router) buildChainFromAPI(ctx context.Context, startMsgID string) []string {
    const maxDepth = 10
    var chain []string
    currentID := startMsgID

    for i := 0; i < maxDepth; i++ {
        info, err := r.feishuCli.GetMessage(ctx, currentID)
        if err != nil {
            log.Printf("[chain] GetMessage %s error: %v", currentID, err)
            break
        }
        chain = append([]string{info.MessageID}, chain...) // 前置
        if info.ParentID == "" {
            break
        }
        currentID = info.ParentID
    }
    return chain
}
```

**Step 5: 实现 handleChainUpgrade（建群+转发+注入历史）**

```go
// handleChainUpgrade 执行升级流程：建群、转发历史、注入 Claude 上下文
func (r *Router) handleChainUpgrade(ctx context.Context, msg feishu.IncomingMessage) {
    chainMsgIDs := r.chainTracker.GetChain(msg.SenderID)
    if len(chainMsgIDs) == 0 {
        log.Printf("[chain] upgrade: no chain for sender %s", msg.SenderID)
        return
    }

    // 1. 获取链上所有消息内容（用于构造历史上下文）
    var historyLines []string
    for _, msgID := range chainMsgIDs {
        info, err := r.feishuCli.GetMessage(ctx, msgID)
        if err != nil || info.Text == "" {
            continue
        }
        role := "用户"
        if info.SenderID == "" || info.SenderID == r.cfg.BotOpenID {
            role = "Claude"
        }
        historyLines = append(historyLines, fmt.Sprintf("[%s]: %s", role, info.Text))
    }

    // 2. 建群
    groupName := fmt.Sprintf("[Claude] 私聊升级 - %s", time.Now().Format("01-02 15:04"))
    if len(historyLines) > 0 {
        // 取第一条用户消息作为群名摘要（截断到 20 字）
        firstLine := historyLines[0]
        if len([]rune(firstLine)) > 20 {
            firstLine = string([]rune(firstLine)[:20]) + "..."
        }
        groupName = fmt.Sprintf("[Claude] %s", firstLine)
    }

    chatID, err := r.feishuCli.CreateGroup(ctx, groupName)
    if err != nil {
        r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("建群失败: %v", err))
        return
    }

    // 3. 拉入用户
    if err := r.feishuCli.AddMember(ctx, chatID, msg.SenderID); err != nil {
        log.Printf("[chain] add member error: %v", err)
    }

    // 4. 合并转发历史消息
    if err := r.feishuCli.MergeForwardMessages(ctx, chainMsgIDs, chatID); err != nil {
        log.Printf("[chain] merge forward error: %v", err)
        // 非致命错误，继续
    }

    // 5. 构造历史上下文注入消息
    var contextPrompt string
    if len(historyLines) > 0 {
        contextPrompt = "以下是我们之前的对话历史，请了解背景后继续：\n\n" +
            strings.Join(historyLines, "\n") +
            "\n\n---\n请基于以上历史继续对话。"
    } else {
        contextPrompt = "这是一个从私聊升级的群聊会话，请继续之前的对话。"
    }

    // 6. 创建 Session，通过历史消息初始化 CLISessionID
    sess := &session.Session{
        ID:           uuid.New().String(),
        ChatID:       chatID,
        Name:         groupName,
        WorkingDir:   r.cfg.DefaultCwd,
        Status:       session.StatusActive,
        CreatedAt:    time.Now(),
        LastActiveAt: time.Now(),
    }

    // 先用历史上下文初始化 session，得到 CLISessionID
    initText, err := r.adapter.RunOnce(ctx, contextPrompt, "", false)
    if err != nil {
        log.Printf("[chain] history injection error: %v", err)
    } else {
        log.Printf("[chain] history injected, response: %s", initText)
    }
    // 注意：RunOnce 目前不返回 sessionID，需要改用 Run 获取 CLISessionID
    // 见 Task 6 处理

    r.store.Put(sess)
    r.store.Save()

    r.feishuCli.SendText(ctx, chatID, "✅ 历史对话已注入，Claude 已了解背景，请继续聊吧！")
    r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("已升级为群聊，请到新群继续对话 👆"))

    // 7. 清空链
    r.chainTracker.Reset(msg.SenderID)
}
```

**Step 6: 构建确认编译通过**

```bash
go build ./...
```
Expected: 无报错。如有缺少 import，补充 `"strings"` / `"agentctl/internal/session"`。

**Step 7: Commit**

```bash
git add internal/router/router.go
git commit -m "feat: integrate reply chain upgrade flow in router"
```

---

### Task 6: 修改 adapter.RunOnce 支持返回 CLISessionID

**背景：** 当前 `RunOnce` 只返回文本，不返回 CLISessionID。历史注入需要在 `RunOnce` 结束后拿到 session ID，以便后续群聊消息使用 `--resume`。

**Files:**
- Modify: `internal/claude/adapter.go`
- Modify: `internal/router/router.go`（更新调用处）
- Modify: `internal/intent/classifier.go`（如有调用 RunOnce 需确认签名兼容）

**Step 1: 新增 RunOnceWithSession 方法**

不改动原有 `RunOnce`（避免影响 intent classifier），新增一个方法：

```go
// RunOnceResult 包含 RunOnce 的输出文本和 CLISessionID
type RunOnceResult struct {
    Text      string
    SessionID string
}

// RunOnceWithSession 执行一次性 prompt 并返回文本输出和 CLISessionID
func (a *Adapter) RunOnceWithSession(ctx context.Context, prompt, cwd string) (*RunOnceResult, error) {
    ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
    defer cancel()

    var result RunOnceResult
    err := a.tmux.ExecStream(ctx, a.envMap(), a.CLIPath, prompt, []string{
        "--output-format", "stream-json",
        "--verbose",
        "--dangerously-skip-permissions",
        "--max-turns", "1",
        "--allowedTools", "",
        "--cwd", cwd,
    }, func(line string) {
        if len(line) == 0 {
            return
        }
        msg, err := ParseStreamLine([]byte(line))
        if err != nil {
            return
        }
        switch msg.Type {
        case MsgSystem:
            if msg.SessionID != "" {
                result.SessionID = msg.SessionID
            }
        case MsgResult:
            result.Text = msg.Result
            if result.SessionID == "" && msg.SessionID != "" {
                result.SessionID = msg.SessionID
            }
        }
    })
    if err != nil {
        return nil, fmt.Errorf("claude cli: %w", err)
    }
    return &result, nil
}
```

**Step 2: 更新 handleChainUpgrade 中的历史注入调用**

将 Task 5 Step 5 中的初始化部分替换为：

```go
// 先用历史上下文初始化 session，得到 CLISessionID
initResult, err := r.adapter.RunOnceWithSession(ctx, contextPrompt, r.cfg.DefaultCwd)
if err != nil {
    log.Printf("[chain] history injection error: %v", err)
} else {
    sess.CLISessionID = initResult.SessionID
    log.Printf("[chain] history injected, sessionID=%s", initResult.SessionID)
}
```

**Step 3: 构建确认编译通过**

```bash
go build ./...
```

**Step 4: Commit**

```bash
git add internal/claude/adapter.go internal/router/router.go
git commit -m "feat: add RunOnceWithSession to capture CLISessionID after history injection"
```

---

### Task 7: 配置 BotOpenID（用于区分用户和 Claude 的历史消息）

**Background:** `handleChainUpgrade` 里用 `r.cfg.BotOpenID` 判断消息是否来自 Bot。需要在配置里加这个字段。

**Files:**
- Modify: `internal/config/config.go`

**Step 1: 在 Config 结构体加 BotOpenID 字段**

```go
type Config struct {
    // ... 现有字段 ...
    BotOpenID string `json:"bot_open_id"` // 新增：Bot 自身的 open_id，用于区分历史消息发送者
}
```

**Step 2: 构建并确认**

```bash
go build ./...
```

**Step 3: Commit**

```bash
git add internal/config/config.go
git commit -m "feat: add BotOpenID to config for chain history sender identification"
```

---

### Task 8: 集成测试

**Step 1: 重新构建并启动服务**

```bash
go build -o server ./cmd/server/ && ./server > log/server.log 2>&1 &
tail -f log/server.log
```

**Step 2: 手动测试场景**

1. 在飞书与 Bot 私聊，发一条消息
2. 引用 Bot 的回复，再发一条消息
3. 持续引用，直到第 4 轮
4. 确认出现升级确认卡片

**Step 3: 测试「继续私聊」分支**

点「继续私聊」→ 确认 Bot 正常回复当前消息，后续不再弹升级卡片

**Step 4: 测试「升级群聊」分支**

点「升级群聊」→ 确认：
- 新群被创建，自己被拉入
- 群里出现合并转发的历史消息卡片
- 群里出现「历史对话已注入」提示
- 在群里继续发消息，Claude 能理解历史上下文

**Step 5: 最终 Commit**

```bash
git add .
git commit -m "feat: reply chain auto group upgrade complete"
```
