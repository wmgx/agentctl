# Session 创建时转发并自动回复设计文档

**日期**: 2026-03-12
**状态**: 已确认

## 需求概述

在用户确认创建 session（建群）后，需要：
1. 将用户的原始消息以文本形式转发到新群
2. Claude 立即自动回复该消息（流式输出）

**注意**: 第二个场景（P2P 引用链升级）已有合并转发历史的能力，无需改动。

## 整体架构

### 核心改动

将 `handleDirect` 中的流式回复逻辑提取为独立方法 `streamResponse`，供两个场景调用：
1. **P2P 直接回复**（现有场景）
2. **Session 创建后的自动回复**（新场景）

### 新增流程

在 `createSession` 函数末尾新增：
1. 发送用户原始消息文本到新群（如"帮我实现 xxx"）
2. 立即调用 `streamResponse` 方法开始流式回复
3. 完整支持流式输出、中断按钮、问题卡片交互（复用 handleDirect 的所有能力）

### 关键决策

- ✅ 不构造假的 `IncomingMessage` 对象
- ✅ `streamResponse` 方法接收核心参数（ctx, chatID, prompt, replyToMsgID 等）
- ✅ 保持原有 P2P 引用链逻辑在 handleDirect 中，不影响提取的 streamResponse
- ✅ 遵循 DRY 原则，避免代码重复

## 代码改动方案

### 1. 新增方法：`streamResponse`

提取 `handleDirect` 中的流式处理核心逻辑（line 375-511），改为独立方法：

```go
// streamResponse 执行流式回复，支持问题卡片交互、中断按钮、多轮对话循环
// 参数：
//   chatID: 目标群 ID
//   initialPrompt: 初始 prompt
//   replyToMsgID: 回复目标消息 ID（为空则发送新消息到 chatID）
//   resumeSessionID: 复用的 CLI session ID（为空则创建新 session）
// 返回：最终的 CLI session ID（用于 Session 绑定）
func (r *Router) streamResponse(ctx context.Context, chatID, initialPrompt, replyToMsgID, resumeSessionID string) string
```

**关键参数说明**：
- `replyToMsgID`: P2P 场景传入用户消息 ID（作为引用回复），Session 场景传空（发送独立消息）
- `resumeSessionID`: Session 场景可以传入已有的 CLI session ID，实现会话复用
- **返回值**: 最终的 CLI session ID，用于 Session 绑定

### 2. `handleDirect` 改动

- 保留 P2P 引用链处理逻辑（line 348-354）
- 构造 prompt 后调用：
  ```go
  r.streamResponse(ctx, msg.ChatID, prompt, msg.MessageID, "")
  ```

### 3. `createSession` 改动

在 line 648 之后（发送"会话已创建"消息后）新增：

```go
// 发送用户原始消息到新群
if _, err := r.feishuCli.SendText(ctx, chatID, msg.Text); err != nil {
    log.Printf("[session] send user message error: %v", err)
    // 非致命错误，继续尝试自动回复
}

// 自动开始流式回复，绑定 Session 的 CLI session ID
cliSessionID := r.streamResponse(ctx, chatID, msg.Text, "", sess.CLISessionID)

// 更新 Session 的 CLI session ID（如果之前为空）
if sess.CLISessionID == "" && cliSessionID != "" {
    sess.CLISessionID = cliSessionID
    r.store.Save()
}
```

## 实现细节

### streamResponse 实现要点

#### 1. 初始卡片发送

```go
var cardMsgID string
var err error

if replyToMsgID != "" {
    // P2P 场景：回复用户消息
    initCard := feishu.StreamingCard("正在思考...", false, "")
    cardMsgID, err = r.feishuCli.ReplyCard(ctx, replyToMsgID, initCard)
} else {
    // Session 场景：直接发到群里
    initCard := feishu.StreamingCard("正在思考...", false, "")
    cardMsgID, err = r.feishuCli.SendCard(ctx, chatID, initCard)
}

if err != nil {
    log.Printf("[stream] send card error: %v", err)
    return ""
}
```

#### 2. 对话循环

完全保留 handleDirect 的 dialogLoop 逻辑（line 378-511）：
- 流式更新（节流 1 秒）
- 中断检测（abort 按钮）
- 问题卡片提取和等待用户回答
- 多轮对话支持

#### 3. 返回值

```go
return resumeSessionID // 返回最终的 CLI session ID
```

## 错误处理和边界情况

### 错误处理策略

#### 1. 发送用户消息失败

- 记录错误日志
- 继续尝试调用 `streamResponse`（可能消息发送失败但群已建好）
- 如果 `streamResponse` 也失败，Session 依然有效

#### 2. streamResponse 执行失败

- 卡片已发送但 Claude 调用失败时，更新卡片显示错误信息
- 不影响 Session 的创建和保存
- 用户可以在群里重新发消息触发

#### 3. CLI Session ID 绑定

- `createSession` 时 Session 的 `CLISessionID` 初始为空
- `streamResponse` 返回实际的 session ID 后更新
- 如果 streamResponse 失败返回空字符串，Session 依然存在
- 下次用户在群里发消息时会创建新的 CLI session

### 关键边界情况

| 场景 | 处理方式 |
|------|---------|
| 建群成功但添加成员失败 | 继续流程，用户可以手动加入群 |
| 转移群主失败 | 不影响核心功能，机器人保持群主身份 |
| 流式回复被用户中断 | 正常行为，Session 依然有效 |
| streamResponse 完全失败 | Session 已创建，用户可在群里重新发消息 |
| 发送用户消息失败但 streamResponse 成功 | 可以正常回复，只是没有用户消息上下文展示 |

## 数据流图

```
用户在私聊确认建群
    ↓
createSession: 建群、拉人、转移群主
    ↓
发送用户原始消息到新群 (msg.Text)
    ↓
调用 streamResponse(ctx, chatID, msg.Text, "", sess.CLISessionID)
    ↓
┌─────────────────────────────────────┐
│ streamResponse                      │
│                                     │
│ 1. 发送初始卡片（流式中）           │
│ 2. 调用 Claude API 流式输出         │
│ 3. 检测中断按钮                     │
│ 4. 提取问题卡片标记                 │
│ 5. 如有问题，发卡片等待回答         │
│ 6. 循环直到完成                     │
│                                     │
│ 返回: cliSessionID                  │
└─────────────────────────────────────┘
    ↓
更新 Session.CLISessionID (如果为空)
    ↓
完成
```

## 与现有功能的对比

| 场景 | 消息转发方式 | 是否自动回复 | 现状 |
|------|-------------|------------|------|
| P2P 引用链升级 | 合并转发 API（聊天记录卡片）| 是（注入历史上下文）| 已实现 ✓ |
| Session 创建 | 文本形式重新发送 | 是（立即流式回复）| **本次新增** |
| P2P 直接回复 | N/A | 是 | 已实现 ✓ |

## 测试计划

### 单元测试

- `streamResponse` 方法的参数处理（replyToMsgID 为空 vs 非空）
- CLI session ID 的返回和绑定逻辑

### 集成测试

1. **正常流程**：
   - 用户触发 session 创建
   - 确认卡片交互
   - 验证新群中出现用户消息
   - 验证 Claude 立即开始流式回复

2. **中断流程**：
   - 用户在流式回复中点击停止按钮
   - 验证卡片更新为"已中断"状态
   - 验证 Session 依然有效

3. **问题卡片流程**：
   - Claude 回复包含问题标记
   - 验证问题卡片发送
   - 用户选择答案后继续对话

4. **错误恢复**：
   - 发送用户消息失败但 streamResponse 成功
   - streamResponse 失败但 Session 已创建
   - 用户在群里手动发消息可以继续对话

### 手动测试 Checklist

- [ ] 在私聊中触发"帮我实现 xxx"（session intent）
- [ ] 确认卡片选择工作目录
- [ ] 验证新群创建成功
- [ ] 验证用户消息出现在新群
- [ ] 验证 Claude 立即开始流式回复
- [ ] 测试中断按钮功能
- [ ] 测试问题卡片交互
- [ ] 验证 Session CLI ID 正确绑定

## 实施顺序

1. **提取 streamResponse 方法**：
   - 从 handleDirect 提取流式处理逻辑
   - 参数化 chatID, replyToMsgID 等
   - 返回 CLI session ID

2. **修改 handleDirect**：
   - 调用 streamResponse 替换原有逻辑
   - 保持 P2P 引用链处理不变

3. **修改 createSession**：
   - 发送用户消息到新群
   - 调用 streamResponse
   - 绑定 CLI session ID

4. **测试验证**：
   - 单元测试
   - 集成测试
   - 手动测试

5. **文档更新**：
   - 更新 CLAUDE.md 的功能说明
   - 更新 README（如需要）

## 风险评估

| 风险 | 影响 | 缓解措施 |
|------|------|---------|
| streamResponse 提取错误导致现有功能受损 | 高 | TDD 开发，先写测试覆盖现有行为 |
| CLI session ID 绑定失败 | 中 | 失败时 Session 依然可用，用户重发消息即可 |
| 流式回复性能问题（大量并发） | 低 | 复用现有节流机制（1 秒） |
| 卡片更新乱序到达飞书 | 低 | 复用现有 cardMu 锁机制 |

## 后续优化

1. **Session 列表显示 CLI session ID**：方便调试和排查问题
2. **统计 streamResponse 调用耗时**：监控性能
3. **支持 Session 预设 system prompt**：不同类型的 session 有不同的初始 prompt
