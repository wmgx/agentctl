package feishu

import (
	"testing"
)

func TestFilterCodeBlocks(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		compact  bool
		expected string
	}{
		{
			name:     "不过滤模式-保留代码块",
			input:    "开头文本\n```go\ncode here\n```\n结尾文本",
			compact:  false,
			expected: "开头文本\n```go\ncode here\n```\n结尾文本",
		},
		{
			name:     "过滤模式-移除单个代码块",
			input:    "开头文本\n```go\ncode here\n```\n结尾文本",
			compact:  true,
			expected: "开头文本\n\n[代码块 #1 已省略]\n\n结尾文本",
		},
		{
			name:     "过滤模式-移除多个代码块",
			input:    "第一段\n```\ncode1\n```\n第二段\n```\ncode2\n```\n第三段",
			compact:  true,
			expected: "第一段\n\n[代码块 #1 已省略]\n\n第二段\n\n[代码块 #2 已省略]\n\n第三段",
		},
		{
			name:     "过滤模式-无代码块",
			input:    "纯文本内容\n没有代码块",
			compact:  true,
			expected: "纯文本内容\n没有代码块",
		},
		{
			name:     "过滤模式-空输入",
			input:    "",
			compact:  true,
			expected: "",
		},
		{
			name:     "过滤模式-代码块包含多行",
			input:    "前文\n```python\ndef hello():\n    print('world')\n```\n后文",
			compact:  true,
			expected: "前文\n\n[代码块 #1 已省略]\n\n后文",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := FilterCodeBlocks(tt.input, tt.compact)
			if result != tt.expected {
				t.Errorf("FilterCodeBlocks() = %q, want %q", result, tt.expected)
			}
		})
	}
}
