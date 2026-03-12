package session

import (
	"context"
	"encoding/json"
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

type Handler struct {
	cfg       *config.Config
	feishuCli *feishu.Client
	store     *Store
	adapter   *claude.Adapter
	pending   *feishu.PendingAction
	locks     sync.Map
}

func NewHandler(cfg *config.Config, feishuCli *feishu.Client, store *Store,
	adapter *claude.Adapter, pending *feishu.PendingAction) *Handler {
	return &Handler{
		cfg:       cfg,
		feishuCli: feishuCli,
		store:     store,
		adapter:   adapter,
		pending:   pending,
	}
}

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
			// Run 自然结束，清理 pending
			h.pending.Resolve(abortID, feishu.ActionResult{Action: "cleanup"})
		}
	}()

	startTime := time.Now()
	card := feishu.StreamingCardWithAbort("正在思考...", "", 0, abortID)
	cardMsgID, err := h.feishuCli.SendCard(ctx, msg.ChatID, card)
	if err != nil {
		log.Printf("send card error: %v", err)
		return
	}

	var (
		textBuf    strings.Builder
		tokenInfo  string
		lastUpdate time.Time
		throttle   = time.Second
		// cardMu 序列化所有 UpdateCard 调用，防止流式更新和中断卡片乱序到达飞书
		cardMu       sync.Mutex
		cardFinished bool
	)

	h.adapter.Run(runCtx, claude.RunOptions{
		Prompt:             msg.Text,
		Cwd:                sess.WorkingDir,
		ResumeSessionID:    sess.CLISessionID,
		Model:              sess.Model,
		AppendSystemPrompt: `当需要用户选择时（无论是 brainstorming、技术方案、参数选择等），优先使用 AskUserQuestion 工具展示交互式卡片，而非纯文本列表（如"A. 选项1 B. 选项2"）。`,
	}, func(event claude.Event) {
		switch event.Type {
		case "session_init":
			sess.CLISessionID = event.SessionID
			h.store.Put(sess)
			h.store.Save()

		case "text":
			textBuf.WriteString(event.Text)
			if !userAborted.Load() && time.Since(lastUpdate) > throttle {
				elapsed := int(time.Since(startTime).Seconds())
				displayText := feishu.FilterCodeBlocks(textBuf.String(), h.cfg.CompactStream)
				cardMu.Lock()
				if !cardFinished {
					h.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithAbort(displayText, "", elapsed, abortID))
					lastUpdate = time.Now()
				}
				cardMu.Unlock()
			}

		case "tool_use":
			if h.isDangerous(event.ToolName, event.ToolInput) {
				h.handleDangerousTool(ctx, msg.ChatID, event)
			}

			// 检测 AskUserQuestion 工具调用
			if event.ToolName == "AskUserQuestion" {
				go h.handleAskUserQuestion(ctx, sess, event.ToolInput)
				// 不使用 break，因为需要让事件处理继续进行（简洁模式的注释提示等）
			}
			// 简洁模式：不显示工具执行提示，只保留思考和结果
			// textBuf.WriteString(fmt.Sprintf("\n\n🔧 **%s** 执行中...\n", event.ToolName))

		case "tool_result":
			// CompactStream 模式下跳过 tool_result 输出，只显示过程和最终结果
			if h.cfg.CompactStream {
				break
			}
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

	elapsed := int(time.Since(startTime).Seconds())
	finalText := feishu.FilterCodeBlocks(textBuf.String(), h.cfg.CompactStream)

	// 持锁发送最终卡片，确保它一定在所有流式更新之后到达飞书
	cardMu.Lock()
	cardFinished = true
	if userAborted.Load() {
		h.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardAborted(finalText, tokenInfo, elapsed))
	} else {
		h.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithElapsed(finalText, true, tokenInfo, elapsed))
	}
	cardMu.Unlock()

	h.store.Save()
}

func (h *Handler) isDangerous(toolName, toolInput string) bool {
	for _, pattern := range h.cfg.DangerousTools {
		if strings.Contains(strings.ToLower(toolName+toolInput), strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func (h *Handler) handleDangerousTool(ctx context.Context, chatID string, event claude.Event) {
	requestID := event.ToolID
	card := feishu.ApprovalCard(event.ToolName, event.ToolInput, requestID)
	h.feishuCli.SendCard(ctx, chatID, card)

	ch := h.pending.Wait(requestID)
	select {
	case result := <-ch:
		if result.Action != "approve" {
			log.Printf("Tool %s denied by user", event.ToolName)
		}
	case <-time.After(5 * time.Minute):
		log.Printf("Tool approval timeout for %s", event.ToolName)
	}
}

// handleAskUserQuestion 处理 AskUserQuestion 工具调用
// 解析 tool_input，生成飞书交互式卡片，等待用户选择后注入答案
func (h *Handler) handleAskUserQuestion(ctx context.Context, sess *Session, toolInput string) {
	// 解析 tool_input
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

	if err := json.Unmarshal([]byte(toolInput), &input); err != nil {
		log.Printf("Failed to parse AskUserQuestion input: %v", err)
		return
	}

	if len(input.Questions) == 0 {
		log.Printf("No questions in AskUserQuestion input")
		return
	}

	// 当前只处理第一个问题（多问题场景需要 future work）
	// TODO: 支持多问题场景
	q := input.Questions[0]

	// 提取选项标签
	var options []string
	for _, opt := range q.Options {
		// 如果有 description，展示为 "标签 - 描述"
		if opt.Description != "" {
			options = append(options, fmt.Sprintf("%s - %s", opt.Label, opt.Description))
		} else {
			options = append(options, opt.Label)
		}
	}

	// 生成唯一的 action ID 用于 pending 等待
	actionID := uuid.New().String()

	// 构造问题标题
	title := q.Question
	if q.Header != "" {
		title = q.Header
	}

	// 发送交互式卡片
	card := feishu.QuestionCard(title, options, false, actionID)
	_, err := h.feishuCli.SendCard(ctx, sess.ChatID, card)
	if err != nil {
		log.Printf("Failed to send question card: %v", err)
		return
	}

	// 等待用户选择（超时 5 分钟）
	actionCh := h.pending.Wait(actionID)
	select {
	case action := <-actionCh:
		if action.Action == "choose_option" {
			// 提取用户选择的答案（移除 description 部分，只保留标签）
			chosen := action.Value["chosen"]
			for _, opt := range q.Options {
				expectedWithDesc := fmt.Sprintf("%s - %s", opt.Label, opt.Description)
				if chosen == expectedWithDesc || chosen == opt.Label {
					chosen = opt.Label
					break
				}
			}
			// 将答案注入回 CLI
			h.sendAnswerToCLI(sess, chosen)
		}
	case <-time.After(5 * time.Minute):
		log.Printf("Timeout waiting for user answer")
		h.feishuCli.SendText(ctx, sess.ChatID, "⏱️ 选择超时，请重新发送消息")
	}
}

// sendAnswerToCLI 将用户选择的答案注入回 Claude CLI 的 stdin
func (h *Handler) sendAnswerToCLI(sess *Session, answer string) {
	if err := h.adapter.SendAnswerToSession(sess.CLISessionID, answer); err != nil {
		log.Printf("Failed to send answer to CLI: %v", err)
	}
}
