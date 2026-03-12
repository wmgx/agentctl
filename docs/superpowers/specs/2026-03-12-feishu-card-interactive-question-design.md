# 飞书卡片交互式问题设计

## 概述

**目标**：在飞书触发的 Claude Code 会话中，当 Claude 使用 `AskUserQuestion` 工具展示选项时，自动转换为飞书交互式卡片，用户通过点击按钮选择答案，提升交互体验。

**适用场景**：所有飞书触发的交互式会话（brainstorming、技术方案选择、参数配置等）

**核心价值**：
- ✅ 更直观的选项展示（卡片 vs 文本列表）
- ✅ 更快的交互响应（点击 vs 打字）
- ✅ 更好的用户体验（视觉化选择界面）

---

## 整体架构

### 数据流

```
用户在飞书发送消息
  ↓
session/handler.go 启动 Claude CLI 流式会话
  ↓
注入系统提示词："优先使用 AskUserQuestion 工具展示选项"
  ↓
Claude 返回 tool_use 事件（工具名 = "AskUserQuestion"）
  ↓
handler.go 监听到 tool_use 事件
  ↓
检测工具名 == "AskUserQuestion"？
  ├─ 否 → 继续现有流程（危险工具检测等）
  └─ 是 → 进入卡片交互流程：
      1. 解析 ToolInput JSON，提取 questions 数组
      2. 生成飞书问题卡片（QuestionCard）
      3. 发送卡片到飞书群聊
      4. pending.Wait(requestID) 阻塞等待用户点击
      5. 用户点击后收到回调 → 提取用户选择
      6. 通过 tmux send-keys 将用户答案注入回 Claude CLI
      7. Claude 继续执行，处理 tool_result
```

### 系统提示词注入

在 `session/handler.go` 的 `Run()` 方法中，调用 `claude.Run()` 时通过 `AppendSystemPrompt` 参数注入：

```go
AppendSystemPrompt: `
当需要用户选择时（无论是 brainstorming、技术方案、参数选择等），
优先使用 AskUserQuestion 工具展示交互式卡片，
而非纯文本列表（如"A. 选项1 B. 选项2"）。
`
```

**注入范围**：仅飞书交互式会话（session/handler.go 的 Run 方法），不影响意图分类等单次调用。

---

## 数据结构与参数映射

### AskUserQuestion 工具参数结构

```json
{
  "questions": [
    {
      "question": "你希望如何解决这个问题？",
      "header": "解决方案",
      "multiSelect": false,
      "options": [
        {
          "label": "更新 brainstorming skill（推荐）",
          "description": "在 skill 文件中添加明确指导，所有 brainstorming 会话都遵循"
        },
        {
          "label": "记录到项目 CLAUDE.md",
          "description": "在当前项目的 CLAUDE.md 中添加偏好设置，仅本项目生效"
        }
      ]
    }
  ]
}
```

### 映射策略（简化方案 + description 拼接）

**步骤 1：提取第一个问题**
- 仅处理 `questions[0]`（多问题场景暂不支持，降级为文本展示）

**步骤 2：构造卡片标题**

```
标题 = question + "\n\n" + 所有 options 的 description 汇总
```

示例：
```
你希望如何解决这个问题？

- 更新 brainstorming skill（推荐）：在 skill 文件中添加明确指导，所有 brainstorming 会话都遵循
- 记录到项目 CLAUDE.md：在当前项目的 CLAUDE.md 中添加偏好设置，仅本项目生效
```

**步骤 3：提取按钮选项**

```go
options := []string{
    "更新 brainstorming skill（推荐）",
    "记录到项目 CLAUDE.md",
}
```

**步骤 4：判断是否添加"其他"**
- AskUserQuestion 工具总是自动提供"Other"选项
- 映射到飞书卡片时设置 `hasCustom=true`

**步骤 5：调用现有 QuestionCard**

```go
card := feishu.QuestionCard(
    title,      // 包含 question + descriptions 的完整标题
    options,    // 仅 label 数组
    true,       // hasCustom=true（对应 Other 选项）
    requestID,  // 使用 ToolID 作为 requestID
)
```

### 用户响应回传格式

用户在飞书点击后，回调返回：

```go
ActionResult{
    Action: "choose_option",
    Value: map[string]string{
        "chosen":     "更新 brainstorming skill（推荐）",  // 用户选择的 label
        "request_id": "toolu_xxx",
    },
    FormValue: map[string]string{
        "custom_answer": "我的自定义输入",  // 如果用户填写了自定义输入
    },
}
```

回传给 Claude CLI 的答案格式（通过 stdin）：

```json
{
  "answer": "更新 brainstorming skill（推荐）"
}
```

或者（自定义输入）：

```json
{
  "answer": "我的自定义输入"
}
```

---

## 具体实现改动点

### 改动 1：session/handler.go - 注入系统提示词

在 `Run()` 方法调用 `claude.Run()` 时，添加 `AppendSystemPrompt` 参数：

```go
// 位置：session/handler.go:97 附近
err = h.claudeAdapter.Run(ctx, claude.RunOptions{
    Prompt:          msg.Text,
    Cwd:             sess.Cwd,
    ResumeSessionID: sess.CLISessionID,
    Model:           sess.Model,
    AppendSystemPrompt: `当需要用户选择时（无论是 brainstorming、技术方案、参数选择等），优先使用 AskUserQuestion 工具展示交互式卡片，而非纯文本列表（如"A. 选项1 B. 选项2"）。`,
}, func(event claude.Event) {
    // ... 现有事件处理逻辑
})
```

### 改动 2：session/handler.go - 增加 AskUserQuestion 工具监听

在 `tool_use` 事件分支（line 122）中添加检测逻辑：

```go
case "tool_use":
    // 检测 AskUserQuestion 工具
    if event.ToolName == "AskUserQuestion" {
        h.handleAskUserQuestion(ctx, msg.ChatID, event)
        break
    }

    // 原有的危险工具检测逻辑
    if h.isDangerous(event.ToolName, event.ToolInput) {
        h.handleDangerousTool(ctx, msg.ChatID, event)
    }
```

### 改动 3：session/handler.go - 实现 handleAskUserQuestion 方法

新增方法处理 AskUserQuestion 工具调用：

```go
func (h *Handler) handleAskUserQuestion(ctx context.Context, chatID string, event claude.Event) {
    // 1. 解析 ToolInput JSON
    var input struct {
        Questions []struct {
            Question    string `json:"question"`
            Header      string `json:"header"`
            MultiSelect bool   `json:"multiSelect"`
            Options     []struct {
                Label       string `json:"label"`
                Description string `json:"description"`
            } `json:"options"`
        } `json:"questions"`
    }

    if err := json.Unmarshal([]byte(event.ToolInput), &input); err != nil {
        log.Printf("[AskUserQuestion] Failed to parse input: %v", err)
        return
    }

    // 仅处理第一个问题（多问题暂不支持）
    if len(input.Questions) == 0 {
        return
    }
    q := input.Questions[0]

    // 2. 构造卡片标题（question + descriptions）
    var titleBuilder strings.Builder
    titleBuilder.WriteString(q.Question)
    titleBuilder.WriteString("\n\n")
    for _, opt := range q.Options {
        titleBuilder.WriteString(fmt.Sprintf("- %s：%s\n", opt.Label, opt.Description))
    }

    // 3. 提取选项 labels
    options := make([]string, len(q.Options))
    for i, opt := range q.Options {
        options[i] = opt.Label
    }

    // 4. 生成飞书卡片
    requestID := event.ToolID
    card := feishu.QuestionCard(titleBuilder.String(), options, true, requestID)
    h.feishuCli.SendCard(ctx, chatID, card)

    // 5. 等待用户选择
    ch := h.pending.Wait(requestID)
    var answer string
    select {
    case result := <-ch:
        // 优先使用自定义输入
        if customAnswer := result.FormValue["custom_answer"]; customAnswer != "" {
            answer = customAnswer
        } else {
            answer = result.Value["chosen"]
        }
    case <-time.After(5 * time.Minute):
        log.Printf("[AskUserQuestion] Timeout waiting for user response")
        answer = "" // 超时则返回空答案
    }

    // 6. 通过 tmux 将答案注入回 Claude CLI
    h.sendAnswerToCLI(event.SessionID, answer)
}
```

### 改动 4：claude/tmux.go - 实现 SendKeys 方法

在 `TmuxRunner` 中新增方法支持 stdin 注入：

```go
// SendKeys 向指定 tmux session 发送按键（模拟用户输入）
func (tr *TmuxRunner) SendKeys(sessionID, text string) error {
    tr.mu.Lock()
    defer tr.mu.Unlock()

    sess, ok := tr.sessions[sessionID]
    if !ok {
        return fmt.Errorf("session %s not found", sessionID)
    }

    // 转义特殊字符
    escapedText := strings.ReplaceAll(text, `"`, `\"`)

    cmd := exec.Command("tmux", "send-keys", "-t", sess.name, escapedText, "Enter")
    return cmd.Run()
}
```

### 改动 5：session/handler.go - 实现 sendAnswerToCLI 方法

```go
func (h *Handler) sendAnswerToCLI(sessionID, answer string) {
    // 构造 JSON 格式的答案
    answerJSON := fmt.Sprintf(`{"answer": "%s"}`, answer)

    // 通过 tmux send-keys 注入
    if err := h.claudeAdapter.SendAnswerToSession(sessionID, answerJSON); err != nil {
        log.Printf("[AskUserQuestion] Failed to send answer: %v", err)
    }
}
```

### 改动 6：claude/adapter.go - 暴露 SendAnswerToSession 方法

```go
// SendAnswerToSession 向指定会话发送用户答案（用于 AskUserQuestion 等交互式工具）
func (a *Adapter) SendAnswerToSession(sessionID, answer string) error {
    return a.tmux.SendKeys(sessionID, answer)
}
```

---

## 错误处理

### 场景 1：多问题场景（暂不支持）

**检测**：`len(input.Questions) > 1`

**降级策略**：记录日志，仅处理第一个问题，其他问题忽略。

```go
if len(input.Questions) > 1 {
    log.Printf("[AskUserQuestion] Multiple questions not supported yet, only processing first one")
}
```

### 场景 2：JSON 解析失败

**检测**：`json.Unmarshal` 返回错误

**降级策略**：记录错误日志，跳过卡片生成，让 Claude CLI 继续执行（可能回退到文本模式）。

```go
if err := json.Unmarshal([]byte(event.ToolInput), &input); err != nil {
    log.Printf("[AskUserQuestion] Failed to parse input: %v", err)
    return
}
```

### 场景 3：用户响应超时

**检测**：`pending.Wait()` 超时（5 分钟）

**降级策略**：返回空答案，记录超时日志。

```go
case <-time.After(5 * time.Minute):
    log.Printf("[AskUserQuestion] Timeout waiting for user response")
    answer = ""
```

### 场景 4：tmux send-keys 失败

**检测**：`SendKeys()` 返回错误

**降级策略**：记录错误日志，不中断会话（可能导致 Claude CLI 卡住，但不会崩溃）。

```go
if err := h.claudeAdapter.SendAnswerToSession(sessionID, answerJSON); err != nil {
    log.Printf("[AskUserQuestion] Failed to send answer: %v", err)
}
```

---

## 测试策略

### 单元测试

**测试 1：handleAskUserQuestion - 正常流程**
- Mock ToolInput JSON
- 验证卡片标题包含 question + descriptions
- 验证选项数组仅包含 labels
- 验证 hasCustom=true

**测试 2：handleAskUserQuestion - 多问题降级**
- Mock 包含 2 个问题的 JSON
- 验证仅处理第一个问题
- 验证日志记录

**测试 3：handleAskUserQuestion - JSON 解析失败**
- Mock 非法 JSON
- 验证错误日志
- 验证不抛出 panic

**测试 4：SendKeys - 特殊字符转义**
- 输入包含双引号的答案
- 验证转义正确（`"` → `\"`）

### 集成测试

**测试 1：端到端卡片交互流程**
1. 启动飞书会话
2. 触发 brainstorming（自动使用 AskUserQuestion）
3. 验证飞书收到问题卡片
4. 模拟用户点击按钮
5. 验证回调被正确处理
6. 验证答案被注入回 Claude CLI
7. 验证 Claude 继续执行

**测试 2：自定义输入场景**
1. 启动飞书会话
2. 触发问题卡片
3. 模拟用户填写自定义输入并提交
4. 验证自定义输入被优先使用

**测试 3：超时场景**
1. 启动飞书会话
2. 触发问题卡片
3. 不响应，等待超时（5 分钟）
4. 验证日志记录超时
5. 验证会话不崩溃

---

## 性能与资源消耗

### 内存

**增量消耗**：约 1KB / 问题卡片（标题 + 选项 + requestID）

**并发卡片**：每个会话最多 1 个待处理卡片（阻塞等待），无内存泄漏风险。

### CPU

**卡片生成**：可忽略（简单 JSON 序列化）

**等待用户响应**：阻塞但不消耗 CPU（channel select）

### 网络

**飞书 API 调用**：
- 发送卡片：1 次 HTTP 请求
- 接收回调：1 次 HTTP 请求

**Claude CLI**：无额外网络消耗（本地 tmux 通信）

---

## 安全考虑

### 1. 注入攻击防护

**风险**：用户自定义输入可能包含恶意内容，注入到 Claude CLI stdin。

**防护**：
- tmux send-keys 本身不执行 shell 命令，仅模拟键盘输入
- JSON 格式化确保答案被正确转义
- 双引号转义：`answer = strings.ReplaceAll(answer, `"`, `\"`)`

### 2. 超时保护

**风险**：用户不响应导致会话永久阻塞。

**防护**：5 分钟超时机制，超时后返回空答案并记录日志。

### 3. requestID 冲突

**风险**：多个卡片使用相同 requestID 导致回调混乱。

**防护**：使用 Claude ToolID（全局唯一）作为 requestID。

---

## 未来扩展

### 1. 多问题支持

当前仅处理 `questions[0]`，未来可支持：
- 分步展示多个问题（逐个发送卡片）
- 合并多个问题到一个卡片（飞书卡片支持多个表单）

### 2. 多选题支持

当前仅支持单选（`multiSelect=false`），未来可支持：
- 飞书卡片多选框（checkbox）
- 回传答案格式调整为数组：`{"answers": ["选项1", "选项2"]}`

### 3. 卡片样式增强

- 添加 emoji 图标（根据 header 自动选择）
- 高亮推荐选项（标记"推荐"的选项）
- description 使用更清晰的排版（分隔线、缩进）

### 4. 卡片状态更新

- 用户点击后更新卡片状态（显示已选择的选项）
- 超时后显示"已超时"状态

---

## 总结

本设计实现了飞书会话中 `AskUserQuestion` 工具到交互式卡片的自动转换，核心优势：

✅ **最小侵入**：仅在飞书交互式会话注入系统提示词，不影响其他场景
✅ **复用现有机制**：利用现有的 `pending.Wait()` 和 `QuestionCard` 基础设施
✅ **简化映射**：description 拼接到标题，label 作为按钮，降低实现复杂度
✅ **完善错误处理**：超时、解析失败、多问题降级等场景均有对策
✅ **易于扩展**：未来可支持多问题、多选题、卡片样式增强等

**关键技术点**：
1. 系统提示词注入（AppendSystemPrompt）
2. tool_use 事件监听（handleAskUserQuestion）
3. tmux send-keys 实现 stdin 注入（SendKeys）
4. pending.Wait() 阻塞等待用户响应
