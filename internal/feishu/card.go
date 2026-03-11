package feishu

import (
	"fmt"
	"sort"
)

func StreamingCard(content string, isComplete bool, tokenInfo string) map[string]interface{} {
	return StreamingCardWithElapsed(content, isComplete, tokenInfo, 0)
}

func StreamingCardWithElapsed(content string, isComplete bool, tokenInfo string, elapsedSec int) map[string]interface{} {
	headerColor := "blue"
	headerTitle := "Claude 回复中..."
	if isComplete {
		headerColor = "green"
		if elapsedSec > 0 {
			headerTitle = fmt.Sprintf("Claude 回复完成（耗时 %ds）", elapsedSec)
		} else {
			headerTitle = "Claude 回复完成"
		}
	} else if elapsedSec > 0 {
		headerTitle = fmt.Sprintf("Claude 回复中...（已用 %ds）", elapsedSec)
	}

	elements := []interface{}{
		map[string]interface{}{
			"tag":     "markdown",
			"content": content,
		},
	}

	if tokenInfo != "" {
		elements = append(elements,
			map[string]interface{}{"tag": "hr"},
			map[string]interface{}{
				"tag": "note",
				"elements": []interface{}{
					map[string]string{
						"tag":     "plain_text",
						"content": tokenInfo,
					},
				},
			},
		)
	}

	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": headerTitle},
			"template": headerColor,
		},
		"elements": elements,
	}
}

func ApprovalCard(toolName, toolInput, requestID string) map[string]interface{} {
	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": "⚠️ 需要确认操作"},
			"template": "orange",
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": fmt.Sprintf("Claude 想执行 **%s**:\n```\n%s\n```", toolName, toolInput),
			},
			map[string]interface{}{
				"tag": "action",
				"actions": []interface{}{
					map[string]interface{}{
						"tag":  "button",
						"text": map[string]string{"tag": "plain_text", "content": "批准"},
						"type": "primary",
						"value": map[string]string{
							"action":     "approve",
							"request_id": requestID,
						},
					},
					map[string]interface{}{
						"tag":  "button",
						"text": map[string]string{"tag": "plain_text", "content": "拒绝"},
						"type": "danger",
						"value": map[string]string{
							"action":     "deny",
							"request_id": requestID,
						},
					},
				},
			},
		},
	}
}

// SessionConfirmCard 展示意图分析结果，让用户确认是否建立群聊会话，同时选择工作目录
func SessionConfirmCard(topic, reason string, repos map[string]string, defaultCwd, requestID string) map[string]interface{} {
	body := fmt.Sprintf("**主题**：%s\n\n**分析**：%s", topic, reason)

	var elements []interface{}

	// 主题/分析文本
	elements = append(elements, map[string]interface{}{
		"tag":     "markdown",
		"content": body,
	})
	elements = append(elements, map[string]interface{}{"tag": "hr"})

	// 预设目录快捷按钮（有配置才显示）
	if len(repos) > 0 {
		elements = append(elements, map[string]interface{}{
			"tag":     "markdown",
			"content": "**快速选择预设目录：**",
		})
		repoKeys := make([]string, 0, len(repos))
		for name := range repos {
			repoKeys = append(repoKeys, name)
		}
		sort.Strings(repoKeys)
		var quickActions []interface{}
		for _, name := range repoKeys {
			path := repos[name]
			quickActions = append(quickActions, map[string]interface{}{
				"tag":  "button",
				"text": map[string]string{"tag": "plain_text", "content": name},
				"type": "default",
				"value": map[string]string{
					"action":     "confirm_session_with_cwd",
					"cwd":        path,
					"request_id": requestID,
				},
			})
		}
		elements = append(elements, map[string]interface{}{
			"tag":     "action",
			"actions": quickActions,
		})
		elements = append(elements, map[string]interface{}{"tag": "hr"})
	}

	// form：手动输入路径 + 建立群聊按钮（form_submit）
	placeholder := defaultCwd
	if placeholder == "" {
		placeholder = "请输入工作目录绝对路径（留空使用默认）"
	}
	elements = append(elements, map[string]interface{}{
		"tag":  "form",
		"name": "session_form",
		"elements": []interface{}{
			map[string]interface{}{
				"tag":        "input",
				"name":       "custom_cwd",
				"max_length": 500,
				"placeholder": map[string]string{
					"tag":     "plain_text",
					"content": placeholder,
				},
			},
			map[string]interface{}{
				"tag":         "button",
				"name":        "submit_session",
				"action_type": "form_submit",
				"text":        map[string]string{"tag": "plain_text", "content": "✅ 建立群聊会话"},
				"type":        "primary",
				"value": map[string]string{
					"action":     "confirm_session_with_cwd",
					"request_id": requestID,
				},
			},
		},
	})

	// 直接回复按钮（不在 form 内，避免被 form_submit 影响）
	elements = append(elements, map[string]interface{}{
		"tag": "action",
		"actions": []interface{}{
			map[string]interface{}{
				"tag":  "button",
				"text": map[string]string{"tag": "plain_text", "content": "💬 直接回复就好"},
				"type": "default",
				"value": map[string]string{
					"action":     "deny_session",
					"request_id": requestID,
				},
			},
		},
	})

	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": "🤔 需要建立独立会话吗？"},
			"template": "blue",
		},
		"elements": elements,
	}
}

// CwdSelectionCard 生成工作目录选择卡片（飞书互动卡片 v2）。
// 有预设 repos 时展示快速选择按钮；底部始终提供文本输入框供手动输入路径。
func CwdSelectionCard(repos map[string]string, defaultCwd, requestID string) map[string]interface{} {
	var elements []interface{}

	// 预设目录快捷按钮（有配置才显示）
	if len(repos) > 0 {
		var actions []interface{}
		for name, path := range repos {
			actions = append(actions, map[string]interface{}{
				"tag":  "button",
				"text": map[string]string{"tag": "plain_text", "content": name},
				"type": "default",
				"value": map[string]string{
					"action":     "select_cwd",
					"cwd":        path,
					"request_id": requestID,
				},
			})
		}
		elements = append(elements,
			map[string]interface{}{
				"tag":     "markdown",
				"content": "**快速选择预设目录：**",
			},
			map[string]interface{}{
				"tag":     "action",
				"actions": actions,
			},
			map[string]interface{}{"tag": "hr"},
		)
	}

	// 文本输入框（始终显示）
	placeholder := defaultCwd
	if placeholder == "" {
		placeholder = "请输入工作目录绝对路径"
	}
	elements = append(elements,
		map[string]interface{}{
			"tag":  "form",
			"name": "cwd_form",
			"elements": []interface{}{
				map[string]interface{}{
					"tag":        "input",
					"name":       "custom_cwd",
					"max_length": 500,
					"placeholder": map[string]string{
						"tag":     "plain_text",
						"content": placeholder,
					},
				},
				map[string]interface{}{
					"tag":         "button",
					"name":        "submit_cwd",
					"action_type": "form_submit",
					"text":        map[string]string{"tag": "plain_text", "content": "✅ 确认路径"},
					"type":        "primary",
					"value": map[string]string{
						"action":     "select_cwd",
						"request_id": requestID,
					},
				},
			},
		},
	)

	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": "选择工作目录"},
			"template": "blue",
		},
		"elements": elements,
	}
}

// ChainUpgradeCard 生成引用链升级群聊的确认卡片
func ChainUpgradeCard(depth int, requestID string) map[string]interface{} {
	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": "💬 对话较长，是否升级为群聊？"},
			"template": "wathet",
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": fmt.Sprintf("当前对话已延伸 **%d 轮**引用。\n升级为群聊后，历史对话将被转发并注入到新会话上下文中，Claude 可直接继续。", depth),
			},
			map[string]interface{}{
				"tag": "action",
				"actions": []interface{}{
					map[string]interface{}{
						"tag":  "button",
						"text": map[string]string{"tag": "plain_text", "content": "🚀 升级为群聊"},
						"type": "primary",
						"value": map[string]string{
							"action":     "upgrade_group",
							"request_id": requestID,
							"depth":      fmt.Sprintf("%d", depth),
						},
					},
					map[string]interface{}{
						"tag":  "button",
						"text": map[string]string{"tag": "plain_text", "content": "继续私聊"},
						"type": "default",
						"value": map[string]string{
							"action":     "dismiss_upgrade",
							"request_id": requestID,
							"depth":      fmt.Sprintf("%d", depth),
						},
					},
				},
			},
		},
	}
}

// CwdSelectionCardDone 生成工作目录选择卡片的完成状态（禁用交互）
// status: "processing" | "selected" | "timeout"
func CwdSelectionCardDone(status string) map[string]interface{} {
	var headerTitle, headerColor, bodyText string
	switch status {
	case "processing":
		headerTitle = "⏳ 正在处理..."
		headerColor = "yellow"
		bodyText = "正在创建会话，请稍候..."
	case "selected":
		headerTitle = "✅ 已选择工作目录"
		headerColor = "green"
		bodyText = "工作目录已确认，会话创建中..."
	default: // timeout
		headerTitle = "⌛ 选择已超时"
		headerColor = "grey"
		bodyText = "选择超时，请重新发送消息。"
	}

	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": headerTitle},
			"template": headerColor,
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": bodyText,
			},
		},
	}
}

// ChainUpgradeCardDone 生成操作完成后的禁用卡片（按钮置灰，防止重复点击）
// status: "upgrading" | "upgraded" | "dismissed" | "timeout"
func ChainUpgradeCardDone(status string, depth int) map[string]interface{} {
	var headerTitle, headerColor, bodyText string
	switch status {
	case "upgrading":
		headerTitle = "⏳ 正在创建群聊..."
		headerColor = "yellow"
		bodyText = "正在创建群聊并注入历史上下文，请稍候..."
	case "upgraded":
		headerTitle = "✅ 已升级为群聊"
		headerColor = "green"
		bodyText = fmt.Sprintf("已成功创建新群聊，**%d 轮**历史对话已转发，请前往新群继续。", depth)
	case "dismissed":
		headerTitle = "已选择继续私聊"
		headerColor = "grey"
		bodyText = "好的，继续在私聊中对话。"
	default: // timeout
		headerTitle = "⌛ 提示已超时"
		headerColor = "grey"
		bodyText = "升级提示已超时，如需升级请重新触发。"
	}

	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": headerTitle},
			"template": headerColor,
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": bodyText,
			},
			map[string]interface{}{
				"tag": "action",
				"actions": []interface{}{
					map[string]interface{}{
						"tag":      "button",
						"text":     map[string]string{"tag": "plain_text", "content": "🚀 升级为群聊"},
						"type":     "primary",
						"disabled": true,
					},
					map[string]interface{}{
						"tag":      "button",
						"text":     map[string]string{"tag": "plain_text", "content": "继续私聊"},
						"type":     "default",
						"disabled": true,
					},
				},
			},
		},
	}
}

// SessionConfirmCardDone 生成建群确认卡片的完成状态（禁用交互）。
// groupName 为空时仅显示"会话已创建"；非空时额外展示群名，供建群完成后更新用。
func SessionConfirmCardDone(confirmed bool, groupName string) map[string]interface{} {
	var headerTitle, headerColor, bodyText string
	if confirmed {
		headerTitle = "✅ 已创建群聊会话"
		headerColor = "green"
		if groupName != "" {
			bodyText = fmt.Sprintf("已创建群聊 **%s**，请到新群继续对话。", groupName)
		} else {
			bodyText = "会话已创建，请到新群继续对话。"
		}
	} else {
		headerTitle = "已选择直接回复"
		headerColor = "grey"
		bodyText = "好的，直接在私聊中回复。"
	}
	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": headerTitle},
			"template": headerColor,
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": bodyText,
			},
		},
	}
}

// QuestionCard 生成交互式问题选择卡片。
// options 中的每一项作为独立按钮；has_custom=true 时底部附加文本输入框。
func QuestionCard(title string, options []string, hasCustom bool, requestID string) map[string]interface{} {
	var elements []interface{}

	// 选项按钮行（每行最多3个）
	var actions []interface{}
	for _, opt := range options {
		actions = append(actions, map[string]interface{}{
			"tag":  "button",
			"text": map[string]string{"tag": "plain_text", "content": opt},
			"type": "default",
			"value": map[string]string{
				"action":     "choose_option",
				"chosen":     opt,
				"request_id": requestID,
			},
		})
	}
	if len(actions) > 0 {
		elements = append(elements, map[string]interface{}{
			"tag":     "action",
			"actions": actions,
		})
	}

	// 自定义输入框
	if hasCustom {
		elements = append(elements, map[string]interface{}{"tag": "hr"})
		elements = append(elements, map[string]interface{}{
			"tag":  "form",
			"name": "question_form",
			"elements": []interface{}{
				map[string]interface{}{
					"tag":  "input",
					"name": "custom_answer",
					"placeholder": map[string]string{
						"tag":     "plain_text",
						"content": "或输入自定义回答...",
					},
				},
				map[string]interface{}{
					"tag":         "button",
					"name":        "submit_answer",
					"action_type": "form_submit",
					"text":        map[string]string{"tag": "plain_text", "content": "✅ 发送"},
					"type":        "primary",
					"value": map[string]string{
						"action":     "choose_option",
						"request_id": requestID,
					},
				},
			},
		})
	}

	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": title},
			"template": "blue",
		},
		"elements": elements,
	}
}

// QuestionCardDone 生成问题卡片的已回答状态（禁用所有交互）
func QuestionCardDone(chosen string) map[string]interface{} {
	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": "✅ 已回答"},
			"template": "green",
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": fmt.Sprintf("**你的选择：** %s", chosen),
			},
		},
	}
}

func ConfirmCard(title, description, requestID string) map[string]interface{} {
	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": title},
			"template": "blue",
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": description,
			},
			map[string]interface{}{
				"tag": "action",
				"actions": []interface{}{
					map[string]interface{}{
						"tag":  "button",
						"text": map[string]string{"tag": "plain_text", "content": "确认"},
						"type": "primary",
						"value": map[string]string{
							"action":     "confirm",
							"request_id": requestID,
						},
					},
					map[string]interface{}{
						"tag":  "button",
						"text": map[string]string{"tag": "plain_text", "content": "取消"},
						"type": "danger",
						"value": map[string]string{
							"action":     "cancel",
							"request_id": requestID,
						},
					},
				},
			},
		},
	}
}

// StreamingCardWithAbort 生成流式回复卡片（进行中），底部附加停止按钮。
// abortID 是停止按钮的 request_id，用于 PendingAction 匹配。
func StreamingCardWithAbort(content, tokenInfo string, elapsedSec int, abortID string) map[string]interface{} {
	headerTitle := "Claude 回复中..."
	if elapsedSec > 0 {
		headerTitle = fmt.Sprintf("Claude 回复中...（已用 %ds）", elapsedSec)
	}

	elements := []interface{}{
		map[string]interface{}{
			"tag":     "markdown",
			"content": content,
		},
	}

	if tokenInfo != "" {
		elements = append(elements,
			map[string]interface{}{"tag": "hr"},
			map[string]interface{}{
				"tag": "note",
				"elements": []interface{}{
					map[string]string{"tag": "plain_text", "content": tokenInfo},
				},
			},
		)
	}

	// 停止按钮
	elements = append(elements, map[string]interface{}{
		"tag": "action",
		"actions": []interface{}{
			map[string]interface{}{
				"tag":  "button",
				"text": map[string]string{"tag": "plain_text", "content": "🛑 停止"},
				"type": "danger",
				"value": map[string]string{
					"action":     "stop_stream",
					"request_id": abortID,
				},
			},
		},
	})

	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": headerTitle},
			"template": "blue",
		},
		"elements": elements,
	}
}

// ApprovalCardDone 生成审批卡片的完成状态（禁用交互）
func ApprovalCardDone(approved bool) map[string]interface{} {
	var headerTitle, headerColor string
	if approved {
		headerTitle = "✅ 已批准"
		headerColor = "green"
	} else {
		headerTitle = "❌ 已拒绝"
		headerColor = "red"
	}
	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": headerTitle},
			"template": headerColor,
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag": "action",
				"actions": []interface{}{
					map[string]interface{}{
						"tag":      "button",
						"text":     map[string]string{"tag": "plain_text", "content": "批准"},
						"type":     "primary",
						"disabled": true,
					},
					map[string]interface{}{
						"tag":      "button",
						"text":     map[string]string{"tag": "plain_text", "content": "拒绝"},
						"type":     "danger",
						"disabled": true,
					},
				},
			},
		},
	}
}

// StreamingCardStopping 生成正在停止状态的卡片（停止按钮已禁用）。
// 用于用户点击"停止"后立即回显，防止重复点击。
// 最终卡片由 UpdateCard 通过 mutex 有序替换。
func StreamingCardStopping() map[string]interface{} {
	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": "🔄 正在停止..."},
			"template": "yellow",
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": "正在中断，请稍候...",
			},
			map[string]interface{}{
				"tag": "action",
				"actions": []interface{}{
					map[string]interface{}{
						"tag":      "button",
						"text":     map[string]string{"tag": "plain_text", "content": "🛑 停止"},
						"type":     "danger",
						"disabled": true,
					},
				},
			},
		},
	}
}

// StreamingCardAborted 生成流式回复卡片的已中断状态。
// 保留已输出内容，header 标记为"已中断"。
func StreamingCardAborted(content, tokenInfo string, elapsedSec int) map[string]interface{} {
	headerTitle := "⛔ 已中断"
	if elapsedSec > 0 {
		headerTitle = fmt.Sprintf("⛔ 已中断（用时 %ds）", elapsedSec)
	}

	elements := []interface{}{
		map[string]interface{}{
			"tag":     "markdown",
			"content": content,
		},
	}

	if tokenInfo != "" {
		elements = append(elements,
			map[string]interface{}{"tag": "hr"},
			map[string]interface{}{
				"tag": "note",
				"elements": []interface{}{
					map[string]string{"tag": "plain_text", "content": tokenInfo + "（已中断）"},
				},
			},
		)
	}

	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": headerTitle},
			"template": "orange",
		},
		"elements": elements,
	}
}
