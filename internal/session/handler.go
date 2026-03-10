package session

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

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

	card := feishu.StreamingCard("正在思考...", false, "")
	cardMsgID, err := h.feishuCli.SendCard(ctx, msg.ChatID, card)
	if err != nil {
		log.Printf("send card error: %v", err)
		return
	}

	var (
		textBuf    strings.Builder
		lastUpdate time.Time
		throttle   = time.Second
	)

	h.adapter.Run(ctx, claude.RunOptions{
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
				card := feishu.StreamingCard(textBuf.String(), false, "")
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
			tokenInfo := ""
			if event.Usage != nil {
				tokenInfo = fmt.Sprintf("✅ Input: %d | Output: %d | Cost: $%.4f",
					event.Usage.InputTokens, event.Usage.OutputTokens, event.CostUSD)
			}
			finalText := textBuf.String()
			if finalText == "" {
				finalText = event.Text
			}
			card := feishu.StreamingCard(finalText, true, tokenInfo)
			h.feishuCli.UpdateCard(ctx, cardMsgID, card)
		}
	})

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
