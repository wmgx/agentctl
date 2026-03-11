package intent

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/wmgx/agentctl/internal/claude"
	"github.com/wmgx/agentctl/internal/session"
)

// ClassifyTrace 记录意图分类的性能追踪信息
type ClassifyTrace struct {
	StartTime         time.Time
	SkillLoadDuration time.Duration
	PromptDuration    time.Duration
	APIDuration       time.Duration
	ParseDuration     time.Duration
	TotalDuration     time.Duration
	PromptSize        int // system prompt 大小（字符数）
	UserMsgSize       int // user prompt 大小（字符数）
	SkillCount        int // 加载的 skill 数量
	CacheHit          bool
}

type Classifier struct {
	adapter         *claude.Adapter
	model           string
	threshold       int // 超过此轮数视为 session，来自 config.ChainUpgradeThreshold
	promptFile      string
	skillsCache     string    // 缓存的 skill 列表字符串
	skillsCacheTime time.Time // 缓存更新时间
	skillsCacheTTL  time.Duration
}

func NewClassifier(adapter *claude.Adapter, model string, threshold int, promptFile string) *Classifier {
	if threshold <= 0 {
		threshold = 4
	}
	return &Classifier{
		adapter:        adapter,
		model:          model,
		threshold:      threshold,
		promptFile:     promptFile,
		skillsCacheTTL: 1 * time.Minute,
	}
}

// defaultSystemPromptTpl 内置 prompt 模板，{{threshold}} 会被替换为实际阈值
const defaultSystemPromptTpl = `你是意图分类器。根据用户消息和现有sessions列表,返回JSON。

【严格要求】
- 只输出纯 JSON，不要有任何额外文字、标点或格式符号
- 不要添加任何前缀或后缀（如"喵～"等）
- 不要使用 markdown 代码块

【可用的一键操作 skill】
系统提供以下专用 skill，可快速完成特定任务（1-2轮即可）：
{{skills}}

【特别注意】
如果用户请求可通过上述 skill 一键完成，即使看起来需要多轮确认信息（如申请权限、测试接口等），也应分类为 "direct"。

判断逻辑——先估算"预期交互轮数"，再决定意图：

预期交互轮数估算：
- 1轮可解决：翻译、查词、算数、解释代码片段、问概念、简单问答
- 2-{{threshold_minus1}}轮可解决：
  * 写小函数/脚本、修bug、代码review、短文生成、代码生成（小而明确的需求）
  * **可通过上述 skill 一键完成的任务**（如申请权限、测试接口、操作配置等）
- {{threshold}}+轮才能完成：实现功能模块、搭建项目、复杂调试、持续迭代且无对应 skill、用户描述模糊需要多次确认

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

// getQuickActionSkills 获取"一键操作类" skill 列表。
// 带 1 分钟缓存，懒加载（首次调用时才读取文件系统）。
// 返回值：(skill 列表, skill 数量, 加载耗时, 是否命中缓存)
func (c *Classifier) getQuickActionSkills() (string, int, time.Duration, bool) {
	start := time.Now()

	// 检查缓存是否有效
	if c.skillsCache != "" && time.Since(c.skillsCacheTime) < c.skillsCacheTTL {
		count := strings.Count(c.skillsCache, "\n") + 1
		if c.skillsCache == "" {
			count = 0
		}
		return c.skillsCache, count, time.Since(start), true
	}

	// 缓存过期或为空，重新读取
	home, err := os.UserHomeDir()
	if err != nil {
		return "", 0, time.Since(start), false // 读取失败，返回空字符串
	}

	skillsDir := filepath.Join(home, ".claude", "skills")
	entries, err := os.ReadDir(skillsDir)
	if err != nil {
		return "", 0, time.Since(start), false // 目录不存在或读取失败
	}

	var quickSkills []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		skillName := entry.Name()
		skillFile := filepath.Join(skillsDir, skillName, "SKILL.md")

		data, err := os.ReadFile(skillFile)
		if err != nil {
			continue
		}

		content := string(data)
		// 提取 description 字段（格式：description: xxx）
		desc := extractDescription(content)
		if isQuickActionSkill(desc) {
			quickSkills = append(quickSkills, fmt.Sprintf("- %s: %s", skillName, desc))
		}
	}

	// 缓存结果
	c.skillsCache = strings.Join(quickSkills, "\n")
	c.skillsCacheTime = time.Now()
	duration := time.Since(start)
	return c.skillsCache, len(quickSkills), duration, false
}

// extractDescription 从 SKILL.md 中提取 description 字段
func extractDescription(content string) string {
	lines := strings.Split(content, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if desc, found := strings.CutPrefix(line, "description:"); found {
			return strings.TrimSpace(desc)
		}
	}
	return ""
}

// isQuickActionSkill 判断是否为"一键操作类" skill
func isQuickActionSkill(desc string) bool {
	if desc == "" {
		return false
	}
	lower := strings.ToLower(desc)

	// 关键词匹配：申请、测试、操作、升级、运行、列出、部署等
	keywords := []string{
		"申请", "权限", "测试", "操作", "升级", "运行", "列出", "部署",
		"run", "test", "operate", "upgrade", "list", "deploy", "apply",
		"tcc", "bytecloud", "bits-ut", "api", "rpc", "http",
	}

	for _, kw := range keywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// buildSystemPrompt 返回最终 system prompt。
// 优先读取外部文件（promptFile），文件不存在则用内置模板。
// 模板中 {{threshold}} 替换为实际阈值，{{threshold_minus1}} 替换为 threshold-1，
// {{skills}} 替换为动态获取的 skill 列表。
// 返回值：(prompt 内容, skill 数量, skill 加载耗时, 是否命中缓存)
func (c *Classifier) buildSystemPrompt() (string, int, time.Duration, bool) {
	tpl := defaultSystemPromptTpl
	if c.promptFile != "" {
		if data, err := os.ReadFile(c.promptFile); err == nil {
			tpl = string(data)
		}
	}

	// 替换阈值
	t := fmt.Sprintf("%d", c.threshold)
	tm1 := fmt.Sprintf("%d", c.threshold-1)
	tpl = strings.ReplaceAll(tpl, "{{threshold}}", t)
	tpl = strings.ReplaceAll(tpl, "{{threshold_minus1}}", tm1)

	// 替换 skill 列表
	skills, count, duration, cacheHit := c.getQuickActionSkills()
	tpl = strings.ReplaceAll(tpl, "{{skills}}", skills)

	return tpl, count, duration, cacheHit
}

func (c *Classifier) Classify(ctx context.Context, userMsg string, activeSessions []*session.Session) (*ClassifyResult, error) {
	trace := &ClassifyTrace{StartTime: time.Now()}

	// 1. 构建 session 摘要
	sessionsSummary := "当前无活跃会话"
	if len(activeSessions) > 0 {
		sessionsSummary = "当前活跃会话:\n"
		for _, s := range activeSessions {
			sessionsSummary += fmt.Sprintf("- [%s] tags:%v status:%s\n", s.Name, s.Tags, s.Status)
		}
	}

	// 2. 构建 system prompt（包含 skill 列表加载）
	promptStart := time.Now()
	sysPrompt, skillCount, skillLoadDuration, cacheHit := c.buildSystemPrompt()
	trace.SkillLoadDuration = skillLoadDuration
	trace.PromptDuration = time.Since(promptStart)
	trace.PromptSize = len(sysPrompt)
	trace.SkillCount = skillCount
	trace.CacheHit = cacheHit

	userPrompt := fmt.Sprintf("用户消息: %s\n\n%s", userMsg, sessionsSummary)
	trace.UserMsgSize = len(userPrompt)

	classifyCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	// 3. 调用 Claude API
	apiStart := time.Now()
	raw, err := c.adapter.RunOnceWithOptions(classifyCtx, userPrompt, claude.RunOnceOptions{
		Model:        c.model,
		NoTools:      true,
		SystemPrompt: sysPrompt,
	})
	trace.APIDuration = time.Since(apiStart)

	if err != nil {
		log.Printf("[TRACE] Classify failed after %v: %v", time.Since(trace.StartTime), err)
		return nil, fmt.Errorf("classify: %w", err)
	}

	// 4. 解析 JSON
	parseStart := time.Now()
	cleaned := strings.TrimSpace(raw)
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
	// 防止 LLM 在 JSON 前后添加额外文本（如全局配置的"喵～"）
	startIdx := strings.Index(cleaned, "{")
	endIdx := strings.LastIndex(cleaned, "}")
	if startIdx != -1 && endIdx != -1 && endIdx > startIdx {
		cleaned = cleaned[startIdx : endIdx+1]
	}

	var result ClassifyResult
	if err := json.Unmarshal([]byte(cleaned), &result); err != nil {
		trace.ParseDuration = time.Since(parseStart)
		trace.TotalDuration = time.Since(trace.StartTime)
		log.Printf("[TRACE] JSON parse failed after %v: %v", trace.TotalDuration, err)
		return nil, fmt.Errorf("parse intent: %w (raw: %s)", err, raw)
	}
	trace.ParseDuration = time.Since(parseStart)
	trace.TotalDuration = time.Since(trace.StartTime)

	// 5. 输出 trace 信息
	logClassifyTrace(trace, &result)

	return &result, nil
}

// logClassifyTrace 输出意图分类的性能追踪信息
func logClassifyTrace(trace *ClassifyTrace, result *ClassifyResult) {
	cacheStatus := "MISS"
	if trace.CacheHit {
		cacheStatus = "HIT"
	}

	log.Printf(`
[INTENT TRACE] =====================================
  Total:         %v
  ├─ Skill Load: %v (cache: %s, count: %d)
  ├─ Prompt:     %v (size: %d chars)
  ├─ API Call:   %v ⚠️  <-- 主要耗时
  └─ Parse:      %v

  User Msg Size: %d chars
  Result:        intent=%s, topic=%s
=========================================`,
		trace.TotalDuration,
		trace.SkillLoadDuration, cacheStatus, trace.SkillCount,
		trace.PromptDuration, trace.PromptSize,
		trace.APIDuration,
		trace.ParseDuration,
		trace.UserMsgSize,
		result.Intent, result.Topic,
	)
}
