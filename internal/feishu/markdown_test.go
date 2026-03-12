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

		convey.Convey("转换 Markdown 标题为加粗文本", func() {
			content := "## 这是标题\n正文内容"
			result := FormatMarkdownForCard(content, true)

			convey.So(len(result), convey.ShouldEqual, 1)
			elem := result[0].(map[string]interface{})
			convey.So(elem["tag"], convey.ShouldEqual, "markdown")
			convey.So(elem["content"], convey.ShouldContainSubstring, "**🔧 这是标题**")
			convey.So(elem["content"], convey.ShouldContainSubstring, "正文内容")
		})

		convey.Convey("同时处理标题和代码块", func() {
			content := "## 执行结果\n```bash\necho test\n```\n完成"
			result := FormatMarkdownForCard(content, true)

			convey.So(len(result), convey.ShouldEqual, 3)

			// 第一段：标题
			elem0 := result[0].(map[string]interface{})
			convey.So(elem0["content"], convey.ShouldContainSubstring, "**🔧 执行结果**")

			// 第二段：代码块
			elem1 := result[1].(map[string]interface{})
			convey.So(elem1["tag"], convey.ShouldEqual, "collapsible_panel")

			// 第三段：后文
			elem2 := result[2].(map[string]interface{})
			convey.So(elem2["content"], convey.ShouldContainSubstring, "完成")
		})
	})
}
