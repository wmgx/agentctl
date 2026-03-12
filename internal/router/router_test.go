package router

import (
	"context"
	"fmt"
	"testing"

	"github.com/bytedance/mockey"
	"github.com/smartystreets/goconvey/convey"

	"github.com/wmgx/agentctl/internal/claude"
	"github.com/wmgx/agentctl/internal/config"
	"github.com/wmgx/agentctl/internal/feishu"
	"github.com/wmgx/agentctl/internal/intent"
	"github.com/wmgx/agentctl/internal/session"
)

func TestRouter_streamResponse(t *testing.T) {
	convey.Convey("TestRouter_streamResponse", t, func() {
		ctx := context.Background()

		// Setup Router with mocks
		cfg := &config.Config{}
		adapter := claude.NewAdapter("", "", "", "")
		feishuCli := &feishu.Client{}
		classifier := &intent.Classifier{}
		sessionStore, _ := session.NewStore("")
		pending := &feishu.PendingAction{}

		router := &Router{
			cfg:        cfg,
			adapter:    adapter,
			feishuCli:  feishuCli,
			classifier: classifier,
			store:      sessionStore,
			pending:    pending,
		}

		convey.Convey("Scenario 1: P2P - replyToMsgID 非空，调用 ReplyCard", func() {
			chatID := "chat_123"
			prompt := "帮我实现一个功能"
			replyToMsgID := "msg_456"
			resumeSessionID := ""

			// Mock ReplyCard
			mockReplyCard := mockey.Mock((*feishu.Client).ReplyCard).To(
				func(ctx context.Context, msgID string, card interface{}) (string, error) {
					convey.So(msgID, convey.ShouldEqual, replyToMsgID)
					return "card_msg_789", nil
				},
			).Build()
			defer mockReplyCard.UnPatch()

			// Mock Claude adapter.Run
			mockRun := mockey.Mock((*claude.Adapter).Run).To(
				func(ctx context.Context, opts claude.RunOptions, handler claude.EventHandler) error {
					handler(claude.Event{Type: "session_init", SessionID: "cli_session_123"})
					handler(claude.Event{Type: "text", Text: "完成"})
					handler(claude.Event{Type: "result", SessionID: "cli_session_123"})
					return nil
				},
			).Build()
			defer mockRun.UnPatch()

			// Mock UpdateCard (to avoid real API calls during streaming)
			mockUpdateCard := mockey.Mock((*feishu.Client).UpdateCard).Return(nil).Build()
			defer mockUpdateCard.UnPatch()

			// Execute
			result := router.streamResponse(ctx, chatID, prompt, replyToMsgID, resumeSessionID)

			// Verify ReplyCard was called (not SendCard)
			convey.So(mockReplyCard.Times(), convey.ShouldEqual, 1)

			// Verify result is not empty (session ID returned)
			convey.So(result, convey.ShouldNotBeEmpty)
		})

		convey.Convey("Scenario 2: Session - replyToMsgID 为空，调用 SendCard", func() {
			chatID := "chat_123"
			prompt := "帮我实现一个功能"
			replyToMsgID := "" // Empty -> Session scenario
			resumeSessionID := ""

			// Mock SendCard
			mockSendCard := mockey.Mock((*feishu.Client).SendCard).To(
				func(ctx context.Context, chatID string, card interface{}) (string, error) {
					return "card_msg_789", nil
				},
			).Build()
			defer mockSendCard.UnPatch()

			// Mock Claude adapter.Run
			mockRun := mockey.Mock((*claude.Adapter).Run).To(
				func(ctx context.Context, opts claude.RunOptions, handler claude.EventHandler) error {
					handler(claude.Event{Type: "session_init", SessionID: "cli_session_123"})
					handler(claude.Event{Type: "text", Text: "完成"})
					handler(claude.Event{Type: "result", SessionID: "cli_session_123"})
					return nil
				},
			).Build()
			defer mockRun.UnPatch()

			// Mock UpdateCard
			mockUpdateCard := mockey.Mock((*feishu.Client).UpdateCard).Return(nil).Build()
			defer mockUpdateCard.UnPatch()

			// Execute
			result := router.streamResponse(ctx, chatID, prompt, replyToMsgID, resumeSessionID)

			// Verify SendCard was called (not ReplyCard)
			convey.So(mockSendCard.Times(), convey.ShouldEqual, 1)

			// Verify result is not empty
			convey.So(result, convey.ShouldNotBeEmpty)
		})

		convey.Convey("Scenario 3: 错误处理 - 卡片发送失败返回空字符串", func() {
			chatID := "chat_123"
			prompt := "帮我实现一个功能"
			replyToMsgID := "msg_456"
			resumeSessionID := ""

			// Mock ReplyCard to return error
			mockReplyCard := mockey.Mock((*feishu.Client).ReplyCard).To(
				func(ctx context.Context, msgID string, card interface{}) (string, error) {
					return "", fmt.Errorf("API error")
				},
			).Build()
			defer mockReplyCard.UnPatch()

			// Execute
			result := router.streamResponse(ctx, chatID, prompt, replyToMsgID, resumeSessionID)

			// Verify: should return empty string on error
			convey.So(result, convey.ShouldBeEmpty)
		})

		convey.Convey("Scenario 4: CLI Session 复用 - 传入 resumeSessionID 能正确返回", func() {
			chatID := "chat_123"
			prompt := "继续上次的任务"
			replyToMsgID := ""
			resumeSessionID := "existing_cli_session_123"

			// Mock SendCard
			mockSendCard := mockey.Mock((*feishu.Client).SendCard).Return("card_msg_789", nil).Build()
			defer mockSendCard.UnPatch()

			// Mock Claude adapter.Run with session reuse
			mockRun := mockey.Mock((*claude.Adapter).Run).To(
				func(ctx context.Context, opts claude.RunOptions, handler claude.EventHandler) error {
					// Verify resumeSessionID is passed in opts
					convey.So(opts.ResumeSessionID, convey.ShouldEqual, resumeSessionID)
					handler(claude.Event{Type: "text", Text: "已恢复会话"})
					handler(claude.Event{Type: "result", SessionID: resumeSessionID})
					return nil
				},
			).Build()
			defer mockRun.UnPatch()

			// Mock UpdateCard
			mockUpdateCard := mockey.Mock((*feishu.Client).UpdateCard).Return(nil).Build()
			defer mockUpdateCard.UnPatch()

			// Execute
			result := router.streamResponse(ctx, chatID, prompt, replyToMsgID, resumeSessionID)

			// Verify: should return the same session ID
			convey.So(result, convey.ShouldEqual, resumeSessionID)
		})
	})
}
