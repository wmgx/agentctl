package claude

import (
	"os/exec"
	"testing"

	"github.com/bytedance/mockey"
	"github.com/smartystreets/goconvey/convey"
)

func TestTmuxRunner_SendKeys(t *testing.T) {
	// 检查 tmux 是否可用，不可用则 skip
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available, skipping SendKeys tests")
		return
	}

	convey.Convey("SendKeys should send text to tmux session", t, func() {
		// Mock exec.Command 避免实际调用 tmux
		defer mockey.Mock((*exec.Cmd).Run).Return(nil).Build().UnPatch()

		runner := NewTmuxRunner("/tmp/test-tmux")

		// Mock session
		runner.sessions["test-session"] = &tmuxSession{
			name: "claude-test",
		}

		err := runner.SendKeys("test-session", `{"answer": "选项1"}`)
		convey.So(err, convey.ShouldBeNil)
	})

	convey.Convey("SendKeys should return error for unknown session", t, func() {
		runner := NewTmuxRunner("/tmp/test-tmux")

		err := runner.SendKeys("unknown-session", "test")
		convey.So(err, convey.ShouldNotBeNil)
		convey.So(err.Error(), convey.ShouldContainSubstring, "not found")
	})
}
