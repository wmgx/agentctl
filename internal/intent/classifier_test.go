package intent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCleanupJSONWithExtraText(t *testing.T) {
	testCases := []struct {
		name     string
		raw      string
		expected ClassifyResult
	}{
		{
			name: "纯净的 JSON",
			raw:  `{"intent":"session","topic":"创建新的会话","tags":["新会话"],"reason":"测试"}`,
			expected: ClassifyResult{
				Intent: IntentSession,
				Topic:  "创建新的会话",
				Tags:   []string{"新会话"},
				Reason: "测试",
			},
		},
		{
			name: "JSON 后带喵～",
			raw:  `{"intent":"session","topic":"创建新的会话","tags":["新会话"],"reason":"测试"}喵～`,
			expected: ClassifyResult{
				Intent: IntentSession,
				Topic:  "创建新的会话",
				Tags:   []string{"新会话"},
				Reason: "测试",
			},
		},
		{
			name: "JSON 后带喵～)",
			raw:  `{"intent":"session","topic":"创建新的会话","tags":["新会话"],"reason":"测试"}喵～)`,
			expected: ClassifyResult{
				Intent: IntentSession,
				Topic:  "创建新的会话",
				Tags:   []string{"新会话"},
				Reason: "测试",
			},
		},
		{
			name: "JSON 前后都有额外文本",
			raw:  `这是前缀 {"intent":"direct","topic":"测试","tags":[],"reason":"直接回复"} 这是后缀`,
			expected: ClassifyResult{
				Intent: IntentDirect,
				Topic:  "测试",
				Tags:   []string{},
				Reason: "直接回复",
			},
		},
		{
			name: "Markdown 代码块格式",
			raw: "```json\n" +
				`{"intent":"system","topic":"列出会话","tags":[],"reason":"系统操作","system_action":"list_sessions"}` + "\n" +
				"```",
			expected: ClassifyResult{
				Intent:       IntentSystem,
				Topic:        "列出会话",
				Tags:         []string{},
				Reason:       "系统操作",
				SystemAction: ActionListSessions,
			},
		},
		{
			name: "Markdown 代码块 + 喵～",
			raw: "```json\n" +
				`{"intent":"session","topic":"测试会话","tags":["测试"],"reason":"需要多轮"}` + "\n" +
				"```\n喵～",
			expected: ClassifyResult{
				Intent: IntentSession,
				Topic:  "测试会话",
				Tags:   []string{"测试"},
				Reason: "需要多轮",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// 模拟 classifier.go 中的清理逻辑
			cleaned := strings.TrimSpace(tc.raw)

			// 去除 markdown 代码块标记
			if strings.HasPrefix(cleaned, "```") {
				if i := strings.Index(cleaned, "\n"); i != -1 {
					cleaned = cleaned[i+1:]
				}
				if j := strings.LastIndex(cleaned, "```"); j != -1 {
					cleaned = strings.TrimSpace(cleaned[:j])
				}
			}

			// 提取纯 JSON：从第一个 { 到最后一个 }
			startIdx := strings.Index(cleaned, "{")
			endIdx := strings.LastIndex(cleaned, "}")
			if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
				cleaned = cleaned[startIdx : endIdx+1]
			}

			// 解析 JSON
			var result ClassifyResult
			err := json.Unmarshal([]byte(cleaned), &result)

			if err != nil {
				t.Fatalf("JSON 解析失败: %v", err)
			}

			if result.Intent != tc.expected.Intent {
				t.Errorf("Intent 不匹配: got %v, want %v", result.Intent, tc.expected.Intent)
			}
			if result.Topic != tc.expected.Topic {
				t.Errorf("Topic 不匹配: got %v, want %v", result.Topic, tc.expected.Topic)
			}
			if result.Reason != tc.expected.Reason {
				t.Errorf("Reason 不匹配: got %v, want %v", result.Reason, tc.expected.Reason)
			}
			if result.SystemAction != tc.expected.SystemAction {
				t.Errorf("SystemAction 不匹配: got %v, want %v", result.SystemAction, tc.expected.SystemAction)
			}

			// 比较 Tags
			if len(result.Tags) != len(tc.expected.Tags) {
				t.Errorf("Tags 长度不匹配: got %d, want %d", len(result.Tags), len(tc.expected.Tags))
			} else {
				for i, tag := range result.Tags {
					if tag != tc.expected.Tags[i] {
						t.Errorf("Tags[%d] 不匹配: got %v, want %v", i, tag, tc.expected.Tags[i])
					}
				}
			}
		})
	}
}

func TestExtractDescription(t *testing.T) {
	testCases := []struct {
		name     string
		content  string
		expected string
	}{
		{
			name: "标准格式",
			content: `---
name: db-perm
description: 申请 ByteCloud 数据库权限
---`,
			expected: "申请 ByteCloud 数据库权限",
		},
		{
			name: "多余空格",
			content: `---
name: api-test
description:    测试 RPC/HTTP 接口
---`,
			expected: "测试 RPC/HTTP 接口",
		},
		{
			name: "无 description",
			content: `---
name: test
---`,
			expected: "",
		},
		{
			name:     "空内容",
			content:  "",
			expected: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := extractDescription(tc.content)
			if result != tc.expected {
				t.Errorf("extractDescription() = %q, want %q", result, tc.expected)
			}
		})
	}
}

func TestIsQuickActionSkill(t *testing.T) {
	testCases := []struct {
		name     string
		desc     string
		expected bool
	}{
		{"申请数据库权限", "申请 ByteCloud 数据库权限", true},
		{"测试接口", "测试 RPC/HTTP 接口", true},
		{"操作 TCC", "操作 TCC 配置", true},
		{"运行单测", "运行 Go 单元测试 bits-ut", true},
		{"升级服务", "升级 TCE 服务版本", true},
		{"英文 test", "Run API tests", true},
		{"英文 deploy", "Deploy to production", true},
		{"不匹配", "帮助用户写代码", false},
		{"空描述", "", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := isQuickActionSkill(tc.desc)
			if result != tc.expected {
				t.Errorf("isQuickActionSkill(%q) = %v, want %v", tc.desc, result, tc.expected)
			}
		})
	}
}

func TestCleanupEdgeCases(t *testing.T) {
	t.Run("没有大括号", func(t *testing.T) {
		raw := "没有JSON内容"
		cleaned := strings.TrimSpace(raw)

		startIdx := strings.Index(cleaned, "{")
		endIdx := strings.LastIndex(cleaned, "}")

		// 如果没有找到大括号，不应该修改 cleaned
		if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
			cleaned = cleaned[startIdx : endIdx+1]
		}

		// 应该解析失败
		var result ClassifyResult
		err := json.Unmarshal([]byte(cleaned), &result)
		if err == nil {
			t.Error("预期解析失败，但成功了")
		}
	})

	t.Run("大括号顺序错误", func(t *testing.T) {
		raw := "}这是错误的{"
		cleaned := strings.TrimSpace(raw)

		startIdx := strings.Index(cleaned, "{")
		endIdx := strings.LastIndex(cleaned, "}")

		// endIdx 不大于 startIdx，不应该修改
		if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
			cleaned = cleaned[startIdx : endIdx+1]
		}

		// 保持原样，解析失败
		var result ClassifyResult
		err := json.Unmarshal([]byte(cleaned), &result)
		if err == nil {
			t.Error("预期解析失败，但成功了")
		}
	})

	t.Run("嵌套对象", func(t *testing.T) {
		raw := `前缀 {"intent":"session","params":{"key":"value"},"topic":"嵌套测试"} 后缀`
		cleaned := strings.TrimSpace(raw)

		startIdx := strings.Index(cleaned, "{")
		endIdx := strings.LastIndex(cleaned, "}")
		if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
			cleaned = cleaned[startIdx : endIdx+1]
		}

		var result ClassifyResult
		err := json.Unmarshal([]byte(cleaned), &result)

		if err != nil {
			t.Fatalf("JSON 解析失败: %v", err)
		}
		if result.Intent != IntentSession {
			t.Errorf("Intent 不匹配: got %v, want %v", result.Intent, IntentSession)
		}
		if result.Topic != "嵌套测试" {
			t.Errorf("Topic 不匹配: got %v, want %v", result.Topic, "嵌套测试")
		}
		if result.Params["key"] != "value" {
			t.Errorf("Params['key'] 不匹配: got %v, want %v", result.Params["key"], "value")
		}
	})
}
