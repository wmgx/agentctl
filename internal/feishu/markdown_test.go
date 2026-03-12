package feishu

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestFormatMarkdownForCard(t *testing.T) {
	convey.Convey("FormatMarkdownForCard", t, func() {
		convey.Convey("普通文本保持不变", func() {
			content := "这是普通文本\n第二行"
			result := FormatMarkdownForCard(content, true)

			convey.So(len(result), convey.ShouldEqual, 1)
			element := result[0].(map[string]interface{})
			convey.So(element["tag"], convey.ShouldEqual, "markdown")
			convey.So(element["content"], convey.ShouldEqual, content)
		})
	})
}
