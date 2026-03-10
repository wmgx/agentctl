package feishu

import "fmt"

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

// SessionConfirmCard 展示意图分析结果，让用户确认是否建立群聊会话
func SessionConfirmCard(topic, reason, requestID string) map[string]interface{} {
	body := fmt.Sprintf("**主题**：%s\n\n**分析**：%s", topic, reason)
	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": "🤔 需要建立独立会话吗？"},
			"template": "blue",
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": body,
			},
			map[string]interface{}{
				"tag": "action",
				"actions": []interface{}{
					map[string]interface{}{
						"tag":  "button",
						"text": map[string]string{"tag": "plain_text", "content": "✅ 建立群聊会话"},
						"type": "primary",
						"value": map[string]string{
							"action":     "confirm_session",
							"request_id": requestID,
						},
					},
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
			},
		},
	}
}

func CwdSelectionCard(repos map[string]string, defaultCwd, requestID string) map[string]interface{} {
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
	if defaultCwd != "" {
		actions = append(actions, map[string]interface{}{
			"tag":  "button",
			"text": map[string]string{"tag": "plain_text", "content": "默认目录"},
			"type": "default",
			"value": map[string]string{
				"action":     "select_cwd",
				"cwd":        defaultCwd,
				"request_id": requestID,
			},
		})
	}
	actions = append(actions, map[string]interface{}{
		"tag":  "button",
		"text": map[string]string{"tag": "plain_text", "content": "输入自定义路径"},
		"type": "default",
		"value": map[string]string{
			"action":     "custom_cwd",
			"request_id": requestID,
		},
	})

	return map[string]interface{}{
		"header": map[string]interface{}{
			"title":    map[string]string{"tag": "plain_text", "content": "选择工作目录"},
			"template": "blue",
		},
		"elements": []interface{}{
			map[string]interface{}{
				"tag":     "markdown",
				"content": "请选择代码仓库：",
			},
			map[string]interface{}{
				"tag":     "action",
				"actions": actions,
			},
		},
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
