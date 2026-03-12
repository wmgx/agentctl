package feishu

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
)

func TestFormatMarkdownForCard(t *testing.T) {
	convey.Convey("FormatMarkdownForCard", t, func() {
		convey.Convey("compact 模式 - 普通文本保持不变", func() {
			content := "这是普通文本\n第二行"
			result := FormatMarkdownForCard(content, true)

			convey.So(len(result), convey.ShouldEqual, 1)
			element := result[0].(map[string]interface{})
			convey.So(element["tag"], convey.ShouldEqual, "markdown")
			convey.So(element["content"], convey.ShouldEqual, content)
		})

		convey.Convey("非 compact 模式保持原样", func() {
			content := "这是普通文本\n第二行"
			result := FormatMarkdownForCard(content, false)

			convey.So(len(result), convey.ShouldEqual, 1)
			element := result[0].(map[string]interface{})
			convey.So(element["tag"], convey.ShouldEqual, "markdown")
			convey.So(element["content"], convey.ShouldEqual, content)
		})

		convey.Convey("检测并提取代码块", func() {
			content := "前文\n```bash\necho hello\n```\n后文"
			result := FormatMarkdownForCard(content, true)

			convey.So(len(result), convey.ShouldEqual, 3)

			// 第一段：前文
			elem0 := result[0].(map[string]interface{})
			convey.So(elem0["tag"], convey.ShouldEqual, "markdown")
			convey.So(elem0["content"], convey.ShouldEqual, "前文")

			// 第二段：代码块（collapsible_panel）
			elem1 := result[1].(map[string]interface{})
			convey.So(elem1["tag"], convey.ShouldEqual, "collapsible_panel")
			convey.So(elem1["expanded"], convey.ShouldEqual, false)

			// 第三段：后文
			elem2 := result[2].(map[string]interface{})
			convey.So(elem2["tag"], convey.ShouldEqual, "markdown")
			convey.So(elem2["content"], convey.ShouldEqual, "后文")
		})
	})
}
