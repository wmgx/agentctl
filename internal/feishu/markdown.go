package feishu

import (
	"regexp"
	"strings"
)

var codeBlockStartRegex = regexp.MustCompile("^```(\\w*)$")
var codeBlockEndRegex = regexp.MustCompile("^```$")
var headingRegex = regexp.MustCompile("^(#{2,6})\\s+(.+)$")

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

	var elements []interface{}
	var currentText strings.Builder
	var codeBlockLines []string
	var codeLanguage string
	inCodeBlock := false

	lines := strings.Split(content, "\n")

	for i, line := range lines {
		// 检测代码块开始
		if matches := codeBlockStartRegex.FindStringSubmatch(line); matches != nil && !inCodeBlock {
			// 保存当前文本段
			if currentText.Len() > 0 {
				// 移除末尾换行符
				text := strings.TrimSuffix(currentText.String(), "\n")
				elements = append(elements, map[string]interface{}{
					"tag":     "markdown",
					"content": text,
				})
				currentText.Reset()
			}

			inCodeBlock = true
			codeLanguage = matches[1]
			codeBlockLines = []string{}
			continue
		}

		// 检测代码块结束
		if codeBlockEndRegex.MatchString(line) && inCodeBlock {
			// 生成 collapsible_panel
			panel := createCollapsiblePanel(codeLanguage, codeBlockLines)
			elements = append(elements, panel)

			inCodeBlock = false
			codeLanguage = ""
			codeBlockLines = nil
			continue
		}

		// 处理代码块内容
		if inCodeBlock {
			codeBlockLines = append(codeBlockLines, line)
		} else {
			// 标题转换（非代码块内）
			if matches := headingRegex.FindStringSubmatch(line); matches != nil {
				headingText := matches[2]
				// 简化实现：所有标题统一使用 🔧 emoji
				// 原设计支持多种 emoji（read📖, write✍️等），此处简化以快速交付
				// 后续可根据用户反馈优化 emoji 映射规则
				convertedLine := "**🔧 " + headingText + "**"
				currentText.WriteString(convertedLine)
				currentText.WriteString("\n")
				continue
			}

			// 添加行内容
			currentText.WriteString(line)
			// 只有不是最后一行才添加换行符
			if i < len(lines)-1 {
				currentText.WriteString("\n")
			}
		}
	}

	// 处理未闭合的代码块（作为普通文本）
	if inCodeBlock {
		// 如果有未闭合的代码块，需要合并前面已保存的文本段（如果有的话）
		var previousText string
		if len(elements) > 0 {
			if lastElem, ok := elements[len(elements)-1].(map[string]interface{}); ok {
				if lastElem["tag"] == "markdown" {
					// 移除最后一个 markdown 元素，准备合并
					previousText = lastElem["content"].(string) + "\n"
					elements = elements[:len(elements)-1]
				}
			}
		}

		// 合并前面的文本、代码块标记和代码内容
		currentText.Reset()
		if previousText != "" {
			currentText.WriteString(previousText)
		}
		currentText.WriteString("```")
		currentText.WriteString(codeLanguage)
		currentText.WriteString("\n")
		currentText.WriteString(strings.Join(codeBlockLines, "\n"))
	}

	// 添加剩余文本
	if currentText.Len() > 0 {
		content := currentText.String()
		// 移除末尾多余换行
		content = strings.TrimSuffix(content, "\n")
		// 移除空白文本（仅包含空格、换行等）
		if strings.TrimSpace(content) != "" {
			elements = append(elements, map[string]interface{}{
				"tag":     "markdown",
				"content": content,
			})
		}
	}

	// 如果最终 elements 为空且没有内容，返回空数组
	if len(elements) == 0 {
		return []interface{}{}
	}

	return elements
}

func createCollapsiblePanel(language string, lines []string) map[string]interface{} {
	// 获取工具名和emoji
	toolName := getToolName(language)
	emoji := getToolEmoji(language)

	// 重建代码块内容（手动添加 ``` 和换行，确保格式正确）
	codeContent := "```" + language + "\n" + strings.Join(lines, "\n") + "\n```"

	return map[string]interface{}{
		"tag": "collapsible_panel",
		"header": map[string]interface{}{
			"title": map[string]interface{}{
				"tag":     "plain_text",
				"content": emoji + " " + toolName + "（点击展开）",
			},
		},
		"expanded": false,
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": codeContent,
			},
		},
	}
}

func getToolName(language string) string {
	nameMap := map[string]string{
		"bash":   "Bash 输出",
		"python": "Python 代码",
		"go":     "Go 代码",
		"json":   "JSON 数据",
		"yaml":   "YAML 配置",
		"sql":    "SQL 查询",
	}

	if name, ok := nameMap[language]; ok {
		return name
	}
	return "代码输出"
}

func getToolEmoji(language string) string {
	emojiMap := map[string]string{
		"bash":   "🔧",
		"python": "🐍",
		"go":     "🐹",
		"json":   "📋",
		"yaml":   "⚙️",
		"sql":    "🗄️",
	}

	if emoji, ok := emojiMap[language]; ok {
		return emoji
	}
	return "📄"
}
