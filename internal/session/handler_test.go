package session

import (
	"context"
	"testing"
	"time"

	"github.com/bytedance/mockey"
	"github.com/smartystreets/goconvey/convey"
	"github.com/wmgx/agentctl/internal/claude"
	"github.com/wmgx/agentctl/internal/config"
	"github.com/wmgx/agentctl/internal/feishu"
)

func TestHandler_HandleMessage_InjectsSystemPrompt(t *testing.T) {
	convey.Convey("HandleMessage should inject AskUserQuestion system prompt", t, func() {
		defer mockey.Mock((*claude.Adapter).Run).To(func(a *claude.Adapter, ctx context.Context, opts claude.RunOptions, handler claude.EventHandler) error {
			// 验证系统提示词已注入
			convey.So(opts.AppendSystemPrompt, convey.ShouldContainSubstring, "AskUserQuestion")
			return nil
		}).Build().UnPatch()

		// Mock feishu client 的方法避免实际调用
		defer mockey.Mock((*feishu.Client).SendCard).Return("msg_id", nil).Build().UnPatch()
		defer mockey.Mock((*feishu.Client).UpdateCard).Return(nil).Build().UnPatch()
		defer mockey.Mock((*feishu.Client).SendText).Return("msg_id", nil).Build().UnPatch()

		cfg := &config.Config{}
		feishuCli := &feishu.Client{}
		store, _ := NewStore("")
		adapter := &claude.Adapter{}
		pending := feishu.NewPendingAction()

		handler := NewHandler(cfg, feishuCli, store, adapter, pending)

		// 创建测试会话
		sess := &Session{
			ID:         "test-session",
			ChatID:     "chat-123",
			Status:     StatusActive,
			WorkingDir: "/tmp",
			Model:      "sonnet",
			CreatedAt:  time.Now(),
		}
		store.Put(sess)

		msg := feishu.IncomingMessage{
			ChatID: "chat-123",
			Text:   "test message",
		}

		handler.HandleMessage(context.Background(), msg)
	})
}
