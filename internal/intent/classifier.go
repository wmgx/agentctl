package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wmgx/agentctl/internal/claude"
	"github.com/wmgx/agentctl/internal/session"
)

type Classifier struct {
	adapter    *claude.Adapter
	model      string
	threshold  int // 超过此轮数视为 session，来自 config.ChainUpgradeThreshold
	promptFile string
}

func NewClassifier(adapter *claude.Adapter, model string, threshold int, promptFile string) *Classifier {
	if threshold <= 0 {
		threshold = 4
	}
	return &Classifier{adapter: adapter, model: model, threshold: threshold, promptFile: promptFile}
}

// defaultSystemPromptTpl 内置 prompt 模板，{{threshold}} 会被替换为实际阈值
const defaultSystemPromptTpl = `你是意图分类器。根据用户消息和现有sessions列表,返回JSON。
不要输出其他任何内容,只输出纯JSON。

判断逻辑——先估算"预期交互轮数"，再决定意图：

预期交互轮数估算：
- 1轮可解决：翻译、查词、算数、解释代码片段、问概念、简单问答
- 2-{{threshold_minus1}}轮可解决：写一个小函数/脚本、修复明确的bug、代码review、短文生成、代码生成（小而明确的需求）
- {{threshold}}+轮才能完成：实现完整功能模块、从零搭建项目、持续调试复杂问题、需要读写本地文件/执行命令、重构大段代码、用户描述模糊需要多次确认

意图类型：
- "direct": 预期 1-{{threshold_minus1}} 轮可解决。包括：问答、解释、翻译、写小函数、修单个bug、小段代码生成
- "session": 预期 {{threshold}}+ 轮才能完成，或任务需要持续迭代、需要访问本地文件/执行命令。用户明确说"建群/开项目"也算。
- "system": 管理系统自身。如:列出会话、关闭会话、管理标签、管理定时任务、系统状态

系统管理子类型（仅限以下值，禁止输出 create_session 或其他自造值）:
- list_sessions: 列出会话
- close_session: 关闭会话
- add_tag / remove_tag: 管理标签(params.tag_name)
- add_cron: 添加定时任务(提取 cron_name, cron_schedule_hint, cron_prompt)
- list_cron: 列出定时任务
- toggle_cron: 启停定时任务(params.cron_name)
- delete_cron: 删除定时任务(params.cron_name)
- status: 系统状态
注意：用户说"建群/新建会话/开始项目"属于 "session" 意图，不属于 "system"

返回格式:
{"intent":"direct|session|system","topic":"主题摘要","tags":["关键词"],"reason":"一句话说明分类理由，session 时需解释预计需要多少轮、为何需要持续会话","system_action":"子类型","params":{},"cron_schedule_hint":"","cron_prompt":"","cron_name":""}`

// EnsureDefaultPrompts 确保 promptsDir 目录及默认 prompt 文件存在。
// 若文件已存在则不覆盖，方便用户自定义。
func EnsureDefaultPrompts(promptsDir string) error {
	if err := os.MkdirAll(promptsDir, 0755); err != nil {
		return err
	}
	classifierFile := filepath.Join(promptsDir, "classifier.md")
	if _, err := os.Stat(classifierFile); os.IsNotExist(err) {
		if err := os.WriteFile(classifierFile, []byte(defaultSystemPromptTpl), 0644); err != nil {
			return err
		}
	}
	return nil
}

// buildSystemPrompt 返回最终 system prompt。
// 优先读取外部文件（promptFile），文件不存在则用内置模板。
// 模板中 {{threshold}} 替换为实际阈值，{{threshold_minus1}} 替换为 threshold-1。
func (c *Classifier) buildSystemPrompt() string {
	tpl := defaultSystemPromptTpl
	if c.promptFile != "" {
		if data, err := os.ReadFile(c.promptFile); err == nil {
			tpl = string(data)
		}
	}
	t := fmt.Sprintf("%d", c.threshold)
	tm1 := fmt.Sprintf("%d", c.threshold-1)
	tpl = strings.ReplaceAll(tpl, "{{threshold}}", t)
	tpl = strings.ReplaceAll(tpl, "{{threshold_minus1}}", tm1)
	return tpl
}

func (c *Classifier) Classify(ctx context.Context, userMsg string, activeSessions []*session.Session) (*ClassifyResult, error) {
	sessionsSummary := "当前无活跃会话"
	if len(activeSessions) > 0 {
		sessionsSummary = "当前活跃会话:\n"
		for _, s := range activeSessions {
			sessionsSummary += fmt.Sprintf("- [%s] tags:%v status:%s\n", s.Name, s.Tags, s.Status)
		}
	}

	sysPrompt := c.buildSystemPrompt()
	prompt := fmt.Sprintf("%s\n\n---\n用户消息: %s\n\n%s", sysPrompt, userMsg, sessionsSummary)

	classifyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	raw, err := c.adapter.RunOnce(classifyCtx, prompt, "", true)
	if err != nil {
		return nil, fmt.Errorf("classify: %w", err)
	}

	cleaned := strings.TrimSpace(raw)
	if strings.HasPrefix(cleaned, "```") {
		if i := strings.Index(cleaned, "\n"); i != -1 {
			cleaned = cleaned[i+1:]
		}
		if j := strings.LastIndex(cleaned, "```"); j != -1 {
			cleaned = strings.TrimSpace(cleaned[:j])
		}
	}

	var result ClassifyResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		return nil, fmt.Errorf("parse intent: %w (raw: %s)", err, raw)
	}
	return &result, nil
}
