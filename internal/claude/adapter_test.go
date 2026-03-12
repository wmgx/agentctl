package claude

import (
	"encoding/json"
	"testing"

	"github.com/bytedance/mockey"
	"github.com/smartystreets/goconvey/convey"
)

func TestAdapter_SendAnswerToSession(t *testing.T) {
	convey.Convey("SendAnswerToSession should delegate to tmux SendKeys", t, func() {
		var capturedSessionID, capturedText string
		defer mockey.Mock((*TmuxRunner).SendKeys).To(func(tr *TmuxRunner, sessionID, text string) error {
			capturedSessionID = sessionID
			capturedText = text
			return nil
		}).Build().UnPatch()

		adapter := &Adapter{
			tmux: &TmuxRunner{},
		}

		err := adapter.SendAnswerToSession("test-session", "选项A")

		convey.So(err, convey.ShouldBeNil)
		convey.So(capturedSessionID, convey.ShouldEqual, "test-session")

		// 验证发送的是正确的 JSON 格式
		var response map[string]string
		json.Unmarshal([]byte(capturedText), &response)
		convey.So(response["answer"], convey.ShouldEqual, "选项A")
	})
}
