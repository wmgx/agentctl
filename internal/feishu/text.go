package feishu

import (
	"fmt"
	"strings"
)

// FilterCodeBlocks 过滤文本中的代码块，用简短提示替代。
// 当 compact=true 时，将 ```...``` 代码块替换为 [...代码已省略]
func FilterCodeBlocks(text string, compact bool) string {
	if !compact {
		return text
	}

	var result strings.Builder
	inCodeBlock := false
	codeBlockCount := 0
	lines := strings.Split(text, "\n")

	for _, line := range lines {
		// 检测代码块开始/结束标记
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			inCodeBlock = !inCodeBlock
			if inCodeBlock {
				// 代码块开始：输出替换文本
				codeBlockCount++
				fmt.Fprintf(&result, "\n[代码块 #%d 已省略]\n", codeBlockCount)
			} else {
				// 代码块结束：输出一个空行以保持间距
				result.WriteString("\n")
			}
			continue
		}

		// 代码块内部跳过
		if inCodeBlock {
			continue
		}

		result.WriteString(line)
		result.WriteString("\n")
	}

	return strings.TrimRight(result.String(), "\n")
}
