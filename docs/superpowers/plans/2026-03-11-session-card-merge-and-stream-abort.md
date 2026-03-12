# 会话确认卡合并 + 流式中断 实施计划

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将会话建群确认卡与工作目录选择合并为一张卡片；在流式输出卡片中添加"停止"按钮支持中断 Claude 执行。

**Architecture:** Feature 1 修改 `SessionConfirmCard` 使其直接包含目录选择（快捷按钮 + form 输入），`handleSession` 合并两步为一步监听。Feature 2 在每次 Run 调用前注册 abort pending，通过 context cancel 触发 tmux kill；`StreamingCardWithAbort` 展示停止按钮。

**Tech Stack:** Go, 飞书互动卡片 v2（form/action），`sync/atomic`，`context.WithCancel`

**Spec:** `docs/superpowers/specs/2026-03-11-session-card-merge-and-stream-abort-design.md`

---

## 文件变更清单

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/feishu/card.go` | 修改 | `SessionConfirmCard` 加 repos 参数；新增 `StreamingCardWithAbort`、`StreamingCardAborted` |
| `internal/router/router.go` | 修改 | `handleSession` 监听新 action 并内联目录提取；`handleDirect` 加 abort 逻辑；删 `startSessionCreation` |
| `internal/session/handler.go` | 修改 | `HandleMessage` 加 abort 逻辑 |
| `cmd/server/main.go` | 修改 | 新增 `confirm_session_with_cwd`、`stop_stream` action 处理；删旧 `confirm_session` |

---

## Chunk 1: Feature 1 — 合并会话确认卡

### Task 1: 更新 `SessionConfirmCard` 签名与卡片结构

**Files:**
- Modify: `internal/feishu/card.go`

**背景知识：**
- 飞书互动卡片的 `form` 元素会在 `form_submit` 按钮点击时把表单内 input 的值通过 `FormValue` 传回
- 快捷目录按钮使用普通按钮（非 form_submit），直接在 `value` 里携带 `cwd`
- `action_type: "form_submit"` 表示按钮点击时同时提交 form 内容

- [ ] **Step 1: 找到 `SessionConfirmCard` 函数并理解当前签名**

  当前签名（`card.go` 约第 93 行）：
  ```go
  func SessionConfirmCard(topic, reason, defaultCwd, requestID string) map[string]interface{}
  ```
  当前只有"建立群聊"和"直接回复"两个按钮，无目录选择逻辑。

- [ ] **Step 2: 修改 `SessionConfirmCard` 函数**

  将签名改为：
  ```go
  func SessionConfirmCard(topic, reason string, repos map[string]string, defaultCwd, requestID string) map[string]interface{}
  ```

  完整新实现（替换原函数体）：
  ```go
  func SessionConfirmCard(topic, reason string, repos map[string]string, defaultCwd, requestID string) map[string]interface{} {
      body := fmt.Sprintf("**主题**：%s\n\n**分析**：%s", topic, reason)

      var elements []interface{}

      // 主题/分析文本
      elements = append(elements, map[string]interface{}{
          "tag":     "markdown",
          "content": body,
      })
      elements = append(elements, map[string]interface{}{"tag": "hr"})

      // 预设目录快捷按钮（有配置才显示）
      if len(repos) > 0 {
          elements = append(elements, map[string]interface{}{
              "tag":     "markdown",
              "content": "**快速选择预设目录：**",
          })
          var quickActions []interface{}
          for name, path := range repos {
              quickActions = append(quickActions, map[string]interface{}{
                  "tag":  "button",
                  "text": map[string]string{"tag": "plain_text", "content": name},
                  "type": "default",
                  "value": map[string]string{
                      "action":     "confirm_session_with_cwd",
                      "cwd":        path,
                      "request_id": requestID,
                  },
              })
          }
          elements = append(elements, map[string]interface{}{
              "tag":     "action",
              "actions": quickActions,
          })
          elements = append(elements, map[string]interface{}{"tag": "hr"})
      }

      // form：手动输入路径 + 建立群聊按钮（form_submit）+ 直接回复按钮
      placeholder := defaultCwd
      if placeholder == "" {
          placeholder = "请输入工作目录绝对路径（留空使用默认）"
      }
      elements = append(elements, map[string]interface{}{
          "tag":  "form",
          "name": "session_form",
          "elements": []interface{}{
              map[string]interface{}{
                  "tag":        "input",
                  "name":       "custom_cwd",
                  "max_length": 500,
                  "placeholder": map[string]string{
                      "tag":     "plain_text",
                      "content": placeholder,
                  },
              },
              map[string]interface{}{
                  "tag":         "button",
                  "action_type": "form_submit",
                  "text":        map[string]string{"tag": "plain_text", "content": "✅ 建立群聊会话"},
                  "type":        "primary",
                  "value": map[string]string{
                      "action":     "confirm_session_with_cwd",
                      "request_id": requestID,
                  },
              },
          },
      })

      // 直接回复按钮（不在 form 内，避免被 form_submit 影响）
      elements = append(elements, map[string]interface{}{
          "tag": "action",
          "actions": []interface{}{
              map[string]interface{}{
                  "tag":  "button",
                  "text": map[string]string{"tag": "plain_text", "content": "💬 直接回复就好"},
                  "type": "default",
                  "value": map[string]string{
                      "action":     "deny_session",
                      "request_id": requestID,
                  },
              },
          },
      })

      return map[string]interface{}{
          "header": map[string]interface{}{
              "title":    map[string]string{"tag": "plain_text", "content": "🤔 需要建立独立会话吗？"},
              "template": "blue",
          },
          "elements": elements,
      }
  }
  ```

- [ ] **Step 3: 确认编译无报错**

  ```bash
  cd /Users/bytedance/go/agentctl && go build ./internal/feishu/...
  ```
  Expected: 无输出（编译成功）。此时 `router.go` 调用 `SessionConfirmCard` 的地方会报参数不匹配错误，先不管，下一个 Task 修复。

---

### Task 2: 更新 `handleSession` 监听新 action，删除 `startSessionCreation`

**Files:**
- Modify: `internal/router/router.go`

**背景知识：**
- `handleSession` 目前发送 `SessionConfirmCard`（旧签名），然后监听 `confirm_session` / `deny_session`
- 用户点"建立群聊"后调用 `startSessionCreation`，后者再发 `CwdSelectionCard`
- 新流程：`handleSession` 直接监听 `confirm_session_with_cwd`，从 action 里提取 cwd，直接调用 `createSession`
- `extractCwd` 工具函数保留，直接复用

- [ ] **Step 1: 修改 `handleSession`**

  找到 `handleSession`（约第 514 行），替换整个函数体：

  ```go
  func (r *Router) handleSession(ctx context.Context, msg feishu.IncomingMessage, result *intent.ClassifyResult) {
      topic := result.Topic
      if topic == "" {
          topic = "未识别主题"
      }
      reason := result.Reason
      if reason == "" {
          reason = "该任务预计需要多轮交互，建议建立独立会话以保持上下文。"
      }

      confirmID := uuid.New().String()
      log.Printf("[router] handleSession: sending confirm card, confirm_id=%s", confirmID)
      card := feishu.SessionConfirmCard(topic, reason, r.cfg.Repos, r.cfg.DefaultCwd, confirmID)
      if _, err := r.feishuCli.ReplyCard(ctx, msg.MessageID, card); err != nil {
          log.Printf("[router] handleSession: ReplyCard error: %v", err)
          r.feishuCli.ReplyText(ctx, msg.MessageID, fmt.Sprintf("⚠️ 发送卡片失败: %v", err))
          return
      }

      ch := r.pending.Wait(confirmID)
      go func() {
          select {
          case action := <-ch:
              log.Printf("[router] handleSession: got action=%s for confirm_id=%s", action.Action, confirmID)
              switch action.Action {
              case "confirm_session_with_cwd":
                  cwd := extractCwd(action, r.cfg.DefaultCwd)
                  r.maybeAddRepo(cwd)
                  r.createSession(ctx, msg, result, cwd)
              case "deny_session":
                  r.handleDirect(ctx, msg)
              default:
                  log.Printf("[router] handleSession: unknown action=%s", action.Action)
              }
          case <-time.After(5 * time.Minute):
              log.Printf("[router] handleSession: confirm timeout for msg %s", msg.MessageID)
          }
      }()
  }
  ```

- [ ] **Step 2: 删除 `startSessionCreation` 函数**

  找到 `startSessionCreation`（约第 552 行），删除整个函数（从 `func (r *Router) startSessionCreation` 到对应的闭合 `}`）。

- [ ] **Step 3: 确认编译**

  ```bash
  cd /Users/bytedance/go/agentctl && go build ./internal/router/...
  ```
  Expected: 无输出。

---

### Task 3: 更新 `main.go` card action handler

**Files:**
- Modify: `cmd/server/main.go`

**背景知识：**
- 飞书卡片 action handler 需要**同步**返回新卡片 JSON 来立即禁用按钮（防止 3 秒后恢复原状）
- `confirm_session_with_cwd` 和旧的 `confirm_session` 都应返回 `SessionConfirmCardDone(true)`
- 新增 `stop_stream` action：不需要返回卡片（由后台 goroutine 处理后 UpdateCard）

- [ ] **Step 1: 在 `OnCardAction` handler 里替换 `confirm_session` 为 `confirm_session_with_cwd`**

  找到（约第 139 行）：
  ```go
  case "confirm_session":
      if b, err := json.Marshal(map[string]interface{}{
          "card": feishu.SessionConfirmCardDone(true),
      }); err == nil {
          immediateCard = string(b)
      }
  ```

  替换为：
  ```go
  case "confirm_session_with_cwd":
      if b, err := json.Marshal(map[string]interface{}{
          "card": feishu.SessionConfirmCardDone(true),
      }); err == nil {
          immediateCard = string(b)
      }
  ```

- [ ] **Step 2: 新增 `stop_stream` case**

  在 `switch action.Action` 里新增（放在 `case "choose_option":` 之后）：
  ```go
  case "stop_stream":
      // 不返回新卡片，由后台 goroutine UpdateCard 处理
      // immediateCard 保持为空即可
  ```

- [ ] **Step 3: 确认编译并验证整体 Feature 1**

  ```bash
  cd /Users/bytedance/go/agentctl && go build ./...
  ```
  Expected: 无报错。

- [ ] **Step 4: 提交**

  ```bash
  cd /Users/bytedance/go/agentctl
  git add internal/feishu/card.go internal/router/router.go cmd/server/main.go
  git commit --author="haotian <lhtsgx@gmail.com>" -m "feat(session): merge confirm card with cwd selection into one step"
  ```

---

## Chunk 2: Feature 2 — 流式输出中断按钮

### Task 4: 新增流式卡片的 abort 变体

**Files:**
- Modify: `internal/feishu/card.go`

**背景知识：**
- `StreamingCardWithElapsed` 是核心函数，`StreamingCard` 是它的包装
- 新增两个函数，不修改现有签名（零破坏）：
  - `StreamingCardWithAbort(content, tokenInfo string, elapsedSec int, abortID string)` — 流式中，含停止按钮
  - `StreamingCardAborted(content, tokenInfo string, elapsedSec int)` — 已中断状态

- [ ] **Step 1: 在 `card.go` 末尾新增 `StreamingCardWithAbort`**

  ```go
  // StreamingCardWithAbort 生成流式回复卡片（进行中），底部附加停止按钮。
  // abortID 是停止按钮的 request_id，用于 PendingAction 匹配。
  func StreamingCardWithAbort(content, tokenInfo string, elapsedSec int, abortID string) map[string]interface{} {
      headerTitle := "Claude 回复中..."
      if elapsedSec > 0 {
          headerTitle = fmt.Sprintf("Claude 回复中...（已用 %ds）", elapsedSec)
      }

      elements := []interface{}{
          map[string]interface{}{
              "tag":     "markdown",
              "content": content,
          },
      }

      if tokenInfo != "" {
          elements = append(elements,
              map[string]interface{}{"tag": "hr"},
              map[string]interface{}{
                  "tag": "note",
                  "elements": []interface{}{
                      map[string]string{"tag": "plain_text", "content": tokenInfo},
                  },
              },
          )
      }

      // 停止按钮
      elements = append(elements, map[string]interface{}{
          "tag": "action",
          "actions": []interface{}{
              map[string]interface{}{
                  "tag":  "button",
                  "text": map[string]string{"tag": "plain_text", "content": "🛑 停止"},
                  "type": "danger",
                  "value": map[string]string{
                      "action":     "stop_stream",
                      "request_id": abortID,
                  },
              },
          },
      })

      return map[string]interface{}{
          "header": map[string]interface{}{
              "title":    map[string]string{"tag": "plain_text", "content": headerTitle},
              "template": "blue",
          },
          "elements": elements,
      }
  }
  ```

- [ ] **Step 2: 新增 `StreamingCardAborted`**

  ```go
  // StreamingCardAborted 生成流式回复卡片的已中断状态。
  // 保留已输出内容，header 标记为"已中断"。
  func StreamingCardAborted(content, tokenInfo string, elapsedSec int) map[string]interface{} {
      headerTitle := "⛔ 已中断"
      if elapsedSec > 0 {
          headerTitle = fmt.Sprintf("⛔ 已中断（用时 %ds）", elapsedSec)
      }

      elements := []interface{}{
          map[string]interface{}{
              "tag":     "markdown",
              "content": content,
          },
      }

      if tokenInfo != "" {
          elements = append(elements,
              map[string]interface{}{"tag": "hr"},
              map[string]interface{}{
                  "tag": "note",
                  "elements": []interface{}{
                      map[string]string{"tag": "plain_text", "content": tokenInfo + "（已中断）"},
                  },
              },
          )
      }

      return map[string]interface{}{
          "header": map[string]interface{}{
              "title":    map[string]string{"tag": "plain_text", "content": headerTitle},
              "template": "orange",
          },
          "elements": elements,
      }
  }
  ```

- [ ] **Step 3: 确认编译**

  ```bash
  cd /Users/bytedance/go/agentctl && go build ./internal/feishu/...
  ```

---

### Task 5: 在 `session/handler.go` 的 `HandleMessage` 里加中断逻辑

**Files:**
- Modify: `internal/session/handler.go`

**背景知识：**
- `HandleMessage` 调用 `h.adapter.Run(ctx, ...)` 阻塞直到 Claude 完成
- 新流程：
  1. 创建 `runCtx, runCancel = context.WithCancel(ctx)` + `abortID`
  2. 注册 `abortCh = h.pending.Wait(abortID)`
  3. 启动监听 goroutine：收到 stop 信号 → `runCancel()`；Run 自然完成 → 清理 pending
  4. 初始卡片使用 `StreamingCardWithAbort` 展示停止按钮
  5. Run 完成后判断是否被中断，选择对应卡片
- **关键**：中断后 `sess.CLISessionID` 不变，下条消息可继续 `--resume`

需要新增 import：`"sync/atomic"`

- [ ] **Step 1: 在 `HandleMessage` 里增加 abort 逻辑**

  找到 `HandleMessage` 函数（约第 36 行），替换函数体为：

  ```go
  func (h *Handler) HandleMessage(ctx context.Context, msg feishu.IncomingMessage) {
      sess := h.store.GetByChatID(msg.ChatID)
      if sess == nil {
          return
      }

      if sess.Status == StatusClosed {
          h.feishuCli.SendText(ctx, msg.ChatID, "此会话已关闭，请到主群创建新会话")
          return
      }

      if _, loaded := h.locks.LoadOrStore(sess.ID, true); loaded {
          h.feishuCli.SendText(ctx, msg.ChatID, "上一个请求正在处理中，请稍候...")
          return
      }
      defer h.locks.Delete(sess.ID)

      sess.Status = StatusActive
      sess.LastActiveAt = time.Now()
      h.store.Put(sess)

      // 中断支持：为本次 Run 创建独立 context 和 abort 监听
      abortID := uuid.New().String()
      runCtx, runCancel := context.WithCancel(ctx)
      defer runCancel()

      abortCh := h.pending.Wait(abortID)
      var userAborted atomic.Bool
      go func() {
          select {
          case action := <-abortCh:
              if action.Action == "stop_stream" {
                  userAborted.Store(true)
                  runCancel()
              }
          case <-runCtx.Done():
              // Run 自然结束，清理 pending（防止卡片点击后找不到 channel）
              h.pending.Resolve(abortID, feishu.ActionResult{Action: "cleanup"})
          }
      }()

      // 发送带停止按钮的初始卡片
      initCard := feishu.StreamingCardWithAbort("正在思考...", "", 0, abortID)
      cardMsgID, err := h.feishuCli.SendCard(ctx, msg.ChatID, initCard)
      if err != nil {
          log.Printf("send card error: %v", err)
          runCancel()
          return
      }

      var (
          textBuf    strings.Builder
          lastUpdate time.Time
          throttle   = time.Second
          tokenInfo  string
      )
      startTime := time.Now()

      h.adapter.Run(runCtx, claude.RunOptions{
          Prompt:          msg.Text,
          Cwd:             sess.WorkingDir,
          ResumeSessionID: sess.CLISessionID,
          Model:           sess.Model,
      }, func(event claude.Event) {
          switch event.Type {
          case "session_init":
              sess.CLISessionID = event.SessionID
              h.store.Put(sess)
              h.store.Save()

          case "text":
              textBuf.WriteString(event.Text)
              if time.Since(lastUpdate) > throttle {
                  elapsed := int(time.Since(startTime).Seconds())
                  card := feishu.StreamingCardWithAbort(textBuf.String(), "", elapsed, abortID)
                  h.feishuCli.UpdateCard(ctx, cardMsgID, card)
                  lastUpdate = time.Now()
              }

          case "tool_use":
              if h.isDangerous(event.ToolName, event.ToolInput) {
                  h.handleDangerousTool(ctx, msg.ChatID, event)
              }
              textBuf.WriteString(fmt.Sprintf("\n\n🔧 **%s** 执行中...\n", event.ToolName))

          case "tool_result":
              resultText := event.Text
              if len(resultText) > 500 {
                  resultText = resultText[:500] + "...(已截断)"
              }
              textBuf.WriteString(fmt.Sprintf("```\n%s\n```\n", resultText))

          case "result":
              if event.Usage != nil {
                  tokenInfo = fmt.Sprintf("✅ Input: %d | Output: %d | Cost: $%.4f",
                      event.Usage.InputTokens, event.Usage.OutputTokens, event.CostUSD)
              }
          }
      })

      // Run 结束：判断是否被用户中断
      elapsed := int(time.Since(startTime).Seconds())
      finalText := textBuf.String()
      if finalText == "" {
          finalText = "（无输出）"
      }

      if userAborted.Load() {
          h.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardAborted(finalText, tokenInfo, elapsed))
      } else {
          h.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithElapsed(finalText, true, tokenInfo, elapsed))
      }

      h.store.Save()
  }
  ```

- [ ] **Step 2: 更新 `import` 块**

  确保 `session/handler.go` 的 import 包含 `"sync/atomic"` 和 `"github.com/google/uuid"`：

  ```go
  import (
      "context"
      "fmt"
      "log"
      "strings"
      "sync"
      "sync/atomic"
      "time"

      "github.com/google/uuid"

      "github.com/wmgx/agentctl/internal/claude"
      "github.com/wmgx/agentctl/internal/config"
      "github.com/wmgx/agentctl/internal/feishu"
  )
  ```

- [ ] **Step 3: 确认编译**

  ```bash
  cd /Users/bytedance/go/agentctl && go build ./internal/session/...
  ```

---

### Task 6: 在 `router.go` 的 `handleDirect` 里加中断逻辑

**Files:**
- Modify: `internal/router/router.go`

**背景知识：**
- `handleDirect` 的 dialogLoop 每轮调用一次 `r.adapter.Run`
- 每轮需要独立的 abortID 和 runCtx（一轮被中断不影响卡片后续）
- 中断后退出 dialogLoop（不继续下一轮）
- `resumeSessionID` 是 dialogLoop 内的局部变量，中断后随函数返回而消失（私聊无持久 session，可接受）

需要新增 import：`"sync/atomic"`

- [ ] **Step 1: 替换 `handleDirect` 里的 dialogLoop**

  找到 `handleDirect` 函数（约第 373 行），将 dialogLoop 部分替换（从 `var resumeSessionID string` 到函数末尾）：

  ```go
      // 支持问题卡片交互的对话循环
      var resumeSessionID string
      startTime := time.Now()

  dialogLoop:
      for {
          // 每轮独立的 abort 控制
          abortID := uuid.New().String()
          runCtx, runCancel := context.WithCancel(ctx)

          abortCh := r.pending.Wait(abortID)
          var userAborted atomic.Bool
          go func(id string, cancel context.CancelFunc) {
              select {
              case action := <-abortCh:
                  if action.Action == "stop_stream" {
                      userAborted.Store(true)
                      cancel()
                  }
              case <-runCtx.Done():
                  r.pending.Resolve(id, feishu.ActionResult{Action: "cleanup"})
              }
          }(abortID, runCancel)

          // 更新卡片为带停止按钮的进行中状态
          elapsed := int(time.Since(startTime).Seconds())
          r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithAbort("正在思考...", "", elapsed, abortID))

          var (
              textBuf    strings.Builder
              tokenInfo  string
              lastUpdate time.Time
          )

          r.adapter.Run(runCtx, claude.RunOptions{
              Prompt:             prompt,
              ResumeSessionID:    resumeSessionID,
              AppendSystemPrompt: questionSystemHint,
          }, func(event claude.Event) {
              switch event.Type {
              case "session_init":
                  if resumeSessionID == "" {
                      resumeSessionID = event.SessionID
                  }
              case "text":
                  textBuf.WriteString(event.Text)
                  elapsed := int(time.Since(startTime).Seconds())
                  if time.Since(lastUpdate) > streamThrottle {
                      displayText := filterCodeBlocks(textBuf.String(), r.cfg.CompactStream)
                      r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithAbort(displayText, "", elapsed, abortID))
                      lastUpdate = time.Now()
                  }
              case "result":
                  if event.Usage != nil {
                      tokenInfo = fmt.Sprintf("✅ Input: %d | Output: %d | Cost: $%.4f",
                          event.Usage.InputTokens, event.Usage.OutputTokens, event.CostUSD)
                  }
                  if resumeSessionID == "" && event.SessionID != "" {
                      resumeSessionID = event.SessionID
                  }
              }
          })

          runCancel() // 确保 goroutine 退出

          elapsed = int(time.Since(startTime).Seconds())
          rawText := textBuf.String()

          // 用户中断：显示中断卡片，退出循环
          if userAborted.Load() {
              cleanText := removeQuestionMark(rawText)
              if cleanText == "" {
                  cleanText = "（已中断）"
              }
              r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardAborted(cleanText, tokenInfo, elapsed))
              break dialogLoop
          }

          question := extractQuestion(rawText)
          cleanText := removeQuestionMark(rawText)
          if cleanText == "" {
              cleanText = "（无输出）"
          }

          if question == nil {
              r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithElapsed(cleanText, true, tokenInfo, elapsed))
              break dialogLoop
          }

          // 有问题标记
          r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithElapsed(cleanText, true, tokenInfo, elapsed))

          requestID := uuid.New().String()
          questionCard := feishu.QuestionCard(question.Title, question.Options, question.HasCustom, requestID)
          if _, err := r.feishuCli.SendCard(ctx, msg.ChatID, questionCard); err != nil {
              log.Printf("[router] send question card error: %v", err)
              break dialogLoop
          }
          log.Printf("[router] question card sent, waiting for answer: %s", question.Title)

          ch := r.pending.Wait(requestID)
          select {
          case action := <-ch:
              chosen := action.Value["chosen"]
              if chosen == "" {
                  chosen = strings.TrimSpace(action.FormValue["custom_answer"])
              }
              if chosen == "" {
                  break dialogLoop
              }
              log.Printf("[router] user answered: %s", chosen)
              prompt = chosen
          case <-time.After(10 * time.Minute):
              log.Printf("[router] question card timeout for sender %s", msg.SenderID)
              break dialogLoop
          }

          // 新一轮回复新建卡片
          newCard := feishu.StreamingCard("正在思考...", false, "")
          cardMsgID, _ = r.feishuCli.SendCard(ctx, msg.ChatID, newCard)
      }
  ```

- [ ] **Step 2: 在 `handleDirect` 的初始发卡片后，删除原来的初始 `StreamingCard` 更新**

  注意：原代码在进入 dialogLoop 前发送了初始卡片（`ReplyCard`），这个保留。进入 loop 后第一件事是 UpdateCard 为带 abortID 的状态。

  检查 `handleDirect` 开头发卡片部分（约第 386-401 行）是否正常：
  ```go
  initCard := feishu.StreamingCard("正在思考...", false, "")
  cardMsgID, err := r.feishuCli.ReplyCard(ctx, msg.MessageID, initCard)
  ```
  这部分保留不动，loop 开头会立即 UpdateCard 为带 abortID 的版本。

- [ ] **Step 3: 更新 `router.go` 的 import**

  确保包含 `"sync/atomic"`：
  ```go
  import (
      "context"
      "encoding/json"
      "fmt"
      "log"
      "os"
      "regexp"
      "strings"
      "sync/atomic"
      "time"

      "github.com/google/uuid"

      "github.com/wmgx/agentctl/internal/claude"
      "github.com/wmgx/agentctl/internal/config"
      "github.com/wmgx/agentctl/internal/feishu"
      "github.com/wmgx/agentctl/internal/intent"
      "github.com/wmgx/agentctl/internal/session"
  )
  ```

- [ ] **Step 4: 确认整体编译**

  ```bash
  cd /Users/bytedance/go/agentctl && go build ./...
  ```
  Expected: 无报错。

- [ ] **Step 5: 提交**

  ```bash
  cd /Users/bytedance/go/agentctl
  git add internal/feishu/card.go internal/session/handler.go internal/router/router.go cmd/server/main.go
  git commit --author="haotian <lhtsgx@gmail.com>" -m "feat(stream): add stop button for interrupting Claude mid-stream"
  ```

---

## 验证检查清单

完成后人工验证（需运行服务并通过飞书测试）：

**Feature 1:**
- [ ] 发一条私聊消息触发会话意图（如"帮我用 Go 写一个 REST API 框架"）
- [ ] 确认只弹出一张卡片，包含：主题/分析 + 预设目录按钮（如配置了 repos）+ 路径输入框 + 两个按钮
- [ ] 点快捷目录按钮 → 群聊正常创建，工作目录为对应路径
- [ ] 输入自定义路径 + 点"建立群聊" → 群聊正常创建，工作目录为输入路径
- [ ] 空输入 + 点"建立群聊" → 使用默认路径创建群聊
- [ ] 点"直接回复" → Claude 正常在私聊里回复

**Feature 2:**
- [ ] 在私聊或群聊发一条需要 Claude 思考较久的消息
- [ ] 确认流式卡片上有"🛑 停止"按钮
- [ ] 点停止 → 卡片变为橙色"⛔ 已中断"，显示已输出内容
- [ ] 中断后在群聊发新消息 → Claude 正常继续（--resume 有效）
- [ ] Claude 自然完成时 → 停止按钮消失，卡片正常变绿色完成状态
