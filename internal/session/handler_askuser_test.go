package session

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/bytedance/mockey"
	"github.com/smartystreets/goconvey/convey"
	"github.com/wmgx/agentctl/internal/claude"
	"github.com/wmgx/agentctl/internal/config"
	"github.com/wmgx/agentctl/internal/feishu"
)

func TestHandler_handleAskUserQuestion(t *testing.T) {
	convey.Convey("handleAskUserQuestion should generate interactive card", t, func() {
		var sentCard string
		var sentChatID string
		cardSent := make(chan bool, 1)

		defer mockey.Mock((*feishu.Client).SendCard).To(func(cli *feishu.Client, ctx context.Context, chatID string, card interface{}) (string, error) {
			sentChatID = chatID
			cardJSON, _ := json.Marshal(card)
			sentCard = string(cardJSON)
			cardSent <- true
			return "card-msg-id", nil
		}).Build().UnPatch()

		cfg := &config.Config{}
		feishuCli := &feishu.Client{}
		store, _ := NewStore("")
		adapter := &claude.Adapter{}
		pending := feishu.NewPendingAction()

		handler := NewHandler(cfg, feishuCli, store, adapter, pending)

		sess := &Session{
			ID:           "test-session",
			ChatID:       "chat-123",
			CLISessionID: "cli-123",
		}

		toolInput := `{
			"questions": [
				{
					"question": "选择一个选项",
					"header": "方案选择",
					"options": [
						{"label": "选项A", "description": "这是A"},
						{"label": "选项B", "description": "这是B"}
					],
					"multiSelect": false
				}
			]
		}`

		// 在 goroutine 中运行
		go handler.handleAskUserQuestion(context.Background(), sess, toolInput)

		// 等待卡片发送完成
		select {
		case <-cardSent:
			// 卡片已发送
		case <-time.After(2 * time.Second):
			t.Fatal("timeout waiting for card to be sent")
		}

		convey.So(sentChatID, convey.ShouldEqual, "chat-123")
		convey.So(sentCard, convey.ShouldContainSubstring, "方案选择")
		convey.So(sentCard, convey.ShouldContainSubstring, "选项A")
	})
}

func TestHandler_sendAnswerToCLI(t *testing.T) {
	convey.Convey("sendAnswerToCLI should call adapter.SendAnswerToSession", t, func() {
		var capturedSessionID, capturedAnswer string

		defer mockey.Mock((*claude.Adapter).SendAnswerToSession).To(func(a *claude.Adapter, sessionID, answer string) error {
			capturedSessionID = sessionID
			capturedAnswer = answer
			return nil
		}).Build().UnPatch()

		cfg := &config.Config{}
		feishuCli := &feishu.Client{}
		store, _ := NewStore("")
		adapter := &claude.Adapter{}
		pending := feishu.NewPendingAction()

		handler := NewHandler(cfg, feishuCli, store, adapter, pending)

		sess := &Session{
			ID:           "test-session",
			CLISessionID: "cli-session-123",
		}

		handler.sendAnswerToCLI(sess, "选项A")

		convey.So(capturedSessionID, convey.ShouldEqual, "cli-session-123")
		convey.So(capturedAnswer, convey.ShouldEqual, "选项A")
	})
}
