package feishu

// FormatMarkdownForCard 将 Markdown 内容转换为飞书卡片元素数组
// compactMode 为 true 时启用代码块折叠和标题转换
func FormatMarkdownForCard(content string, compactMode bool) []interface{} {
	// 非 compact 模式直接返回原文本
	if !compactMode {
		return []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": content,
			},
		}
	}

	// 框架实现：compact 模式暂时也返回原文本
	// Task 2 将实现完整的状态机解析
	return []interface{}{
		map[string]interface{}{
			"tag":     "markdown",
			"content": content,
		},
	}
}
