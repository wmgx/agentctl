# 飞书 + Claude Code 远程操控系统 实施计划

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**目标:** 构建 Go 后台服务，通过飞书群聊远程操控 Claude Code CLI，支持拉群建 session、流式卡片输出、语义化交互、定时任务。

**架构:** 单进程 Go 服务，飞书 WebSocket 长连接接收消息，fork claude CLI 子进程执行，JSON 文件持久化。主 Bot 做三路路由（即时指令/session 对话/系统管理），每个群对应独立 session。

**技术栈:** Go, larksuite/oapi-sdk-go/v3 (飞书SDK), robfig/cron/v3, Anthropic API (意图分类), claude CLI (stream-json)

---

## Phase 1: 项目骨架与基础设施

### Task 1: 初始化项目结构与依赖

**Files:**
- Create: `cmd/server/main.go`
- Create: `internal/config/config.go`
- Modify: `go.mod`
- Create: `config.example.json`

**Step 1: 初始化 go module 并安装依赖**

Run:
```bash
cd /Users/bytedance/go/agentctl
go get github.com/larksuite/oapi-sdk-go/v3@latest
go get github.com/larksuite/oapi-sdk-go/v3/ws@latest
go get github.com/larksuite/oapi-sdk-go/v3/service/im/v1@latest
go get github.com/larksuite/oapi-sdk-go/v3/event/dispatcher@latest
go get github.com/robfig/cron/v3@latest
go get github.com/google/uuid@latest
```

**Step 2: 创建 config 数据结构与加载逻辑**

Create `internal/config/config.go`:

```go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type FeishuConfig struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	BotName   string `json:"bot_name"`
}

type AnthropicConfig struct {
	APIKey string `json:"api_key"`
	Model  string `json:"model"` // 默认 claude-haiku-4-5-20250929
}

type Config struct {
	Feishu         FeishuConfig      `json:"feishu"`
	Anthropic      AnthropicConfig   `json:"anthropic"`
	DefaultCwd     string            `json:"default_cwd"`
	Repos          map[string]string `json:"repos"`
	IdleTimeoutMin int               `json:"idle_timeout_min"`
	DangerousTools []string          `json:"dangerous_tools"`
	RouterChatID   string            `json:"router_chat_id"`
	ClaudeCLIPath  string            `json:"claude_cli_path"` // 默认 "claude"
}

func DefaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agent-for-im")
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if cfg.IdleTimeoutMin == 0 {
		cfg.IdleTimeoutMin = 30
	}
	if cfg.ClaudeCLIPath == "" {
		cfg.ClaudeCLIPath = "claude"
	}
	if cfg.Anthropic.Model == "" {
		cfg.Anthropic.Model = "claude-haiku-4-5-20250929"
	}
	return &cfg, nil
}
```

**Step 3: 创建示例配置文件**

Create `config.example.json`:

```json
{
  "feishu": {
    "app_id": "cli_xxxx",
    "app_secret": "xxxx",
    "bot_name": "ClaudeBot"
  },
  "anthropic": {
    "api_key": "sk-ant-xxxx",
    "model": "claude-haiku-4-5-20250929"
  },
  "default_cwd": "/Users/bytedance/go",
  "repos": {
    "order": "/Users/bytedance/go/order-service",
    "contract": "/Users/bytedance/go/contract-service"
  },
  "idle_timeout_min": 30,
  "dangerous_tools": ["rm ", "git push", "git reset"],
  "router_chat_id": "",
  "claude_cli_path": "claude"
}
```

**Step 4: 创建最小入口 main.go**

Create `cmd/server/main.go`:

```go
package main

import (
	"fmt"
	"log"
	"os"
	"path/filepath"

	"agentctl/internal/config"
)

func main() {
	dataDir := config.DefaultDataDir()
	configPath := filepath.Join(dataDir, "config.json")

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	fmt.Printf("Agent for IM started. Feishu App: %s\n", cfg.Feishu.AppID)
	fmt.Printf("Default CWD: %s\n", cfg.DefaultCwd)
	fmt.Printf("Repos: %v\n", cfg.Repos)
}
```

**Step 5: 验证编译通过**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功，无错误

**Step 6: 提交**

```bash
git init
git add go.mod go.sum cmd/ internal/config/ config.example.json
git commit -m "feat: init project skeleton with config loading"
```

---

### Task 2: JSON 文件存储层

**Files:**
- Create: `internal/session/model.go`
- Create: `internal/session/store.go`
- Create: `internal/cron/model.go`

**Step 1: 创建 Session 数据模型**

Create `internal/session/model.go`:

```go
package session

import "time"

type Status string

const (
	StatusActive    Status = "active"
	StatusSuspended Status = "suspended"
	StatusClosed    Status = "closed"
)

type Session struct {
	ID           string    `json:"id"`
	ChatID       string    `json:"chat_id"`
	Name         string    `json:"name"`
	Tags         []string  `json:"tags"`
	WorkingDir   string    `json:"working_dir"`
	CLISessionID string    `json:"cli_session_id"`
	Status       Status    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}
```

**Step 2: 创建 Store（JSON 文件读写，原子写入）**

Create `internal/session/store.go`:

```go
package session

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu       sync.RWMutex
	sessions map[string]*Session // key = Session.ID
	filePath string
}

func NewStore(dataDir string) (*Store, error) {
	fp := filepath.Join(dataDir, "sessions.json")
	s := &Store{
		sessions: make(map[string]*Session),
		filePath: fp,
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	var sessions []*Session
	if err := json.Unmarshal(data, &sessions); err != nil {
		return fmt.Errorf("parse sessions: %w", err)
	}
	for _, sess := range sessions {
		s.sessions[sess.ID] = sess
	}
	return nil
}

func (s *Store) Save() error {
	s.mu.RLock()
	sessions := make([]*Session, 0, len(s.sessions))
	for _, sess := range s.sessions {
		sessions = append(sessions, sess)
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(sessions, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

func (s *Store) Put(sess *Session) {
	s.mu.Lock()
	s.sessions[sess.ID] = sess
	s.mu.Unlock()
}

func (s *Store) GetByID(id string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessions[id]
}

func (s *Store) GetByChatID(chatID string) *Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sess := range s.sessions {
		if sess.ChatID == chatID {
			return sess
		}
	}
	return nil
}

func (s *Store) ListActive() []*Session {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Session
	for _, sess := range s.sessions {
		if sess.Status == StatusActive || sess.Status == StatusSuspended {
			result = append(result, sess)
		}
	}
	return result
}
```

**Step 3: 创建 CronJob 数据模型**

Create `internal/cron/model.go`:

```go
package cron

type CronJob struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Cron       string   `json:"cron"`
	Prompt     string   `json:"prompt"`
	WorkingDir string   `json:"working_dir,omitempty"`
	TargetChat string   `json:"target_chat,omitempty"`
	Tags       []string `json:"tags,omitempty"`
	Enabled    bool     `json:"enabled"`
}
```

**Step 4: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功

**Step 5: 提交**

```bash
git add internal/session/ internal/cron/model.go
git commit -m "feat: add session store and data models"
```

---

## Phase 2: Claude CLI 适配层

### Task 3: stream-json 解析器

**Files:**
- Create: `internal/claude/stream.go`

**Step 1: 实现 NDJSON 事件解析器**

Create `internal/claude/stream.go`:

```go
package claude

import "encoding/json"

// Claude CLI stream-json 输出的消息类型
type MessageType string

const (
	MsgSystem    MessageType = "system"
	MsgAssistant MessageType = "assistant"
	MsgUser      MessageType = "user"
	MsgResult    MessageType = "result"
)

// StreamMessage 是 stream-json 的每行 JSON
type StreamMessage struct {
	Type      MessageType     `json:"type"`
	SessionID string          `json:"session_id,omitempty"`
	Message   json.RawMessage `json:"message,omitempty"`

	// result 类型专用
	Subtype    string  `json:"subtype,omitempty"`
	Result     string  `json:"result,omitempty"`
	CostUSD    float64 `json:"total_cost_usd,omitempty"`
	DurationMs int64   `json:"duration_ms,omitempty"`
	Usage      *Usage  `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_input_tokens,omitempty"`
	CacheCreate  int `json:"cache_creation_input_tokens,omitempty"`
}

// AssistantMessage 是 assistant 类型的 message 字段
type AssistantMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
	Type  string `json:"type"`            // "text" | "tool_use"
	Text  string `json:"text,omitempty"`  // type=text 时
	ID    string `json:"id,omitempty"`    // type=tool_use 时
	Name  string `json:"name,omitempty"`  // 工具名
	Input json.RawMessage `json:"input,omitempty"` // 工具输入
}

// UserMessage 是 user 类型的 message 字段（tool_result）
type UserMessage struct {
	Role    string             `json:"role"`
	Content []ToolResultBlock  `json:"content"`
}

type ToolResultBlock struct {
	Type      string `json:"type"` // "tool_result"
	ToolUseID string `json:"tool_use_id"`
	Content   string `json:"content"`
}

func ParseStreamLine(line []byte) (*StreamMessage, error) {
	var msg StreamMessage
	if err := json.Unmarshal(line, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func ParseAssistantMessage(raw json.RawMessage) (*AssistantMessage, error) {
	var msg AssistantMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}

func ParseUserMessage(raw json.RawMessage) (*UserMessage, error) {
	var msg UserMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
```

**Step 2: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功

**Step 3: 提交**

```bash
git add internal/claude/stream.go
git commit -m "feat: add stream-json parser for claude CLI output"
```

---

### Task 4: CLI 子进程适配器

**Files:**
- Create: `internal/claude/adapter.go`

**Step 1: 实现 CLI 子进程管理器**

Create `internal/claude/adapter.go`:

```go
package claude

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
)

// Event 是传给上层的统一事件
type Event struct {
	Type       string // "text" | "tool_use" | "tool_result" | "result" | "session_init" | "error"
	Text       string
	ToolName   string
	ToolInput  string
	ToolID     string
	SessionID  string
	CostUSD    float64
	Usage      *Usage
	DurationMs int64
}

// EventHandler 是事件回调函数
type EventHandler func(event Event)

type Adapter struct {
	CLIPath string
}

func NewAdapter(cliPath string) *Adapter {
	return &Adapter{CLIPath: cliPath}
}

// Run 启动 claude CLI 子进程并流式解析输出
func (a *Adapter) Run(ctx context.Context, opts RunOptions, handler EventHandler) error {
	args := []string{
		"-p", opts.Prompt,
		"--output-format", "stream-json",
		"--verbose",
	}
	if opts.Cwd != "" {
		args = append(args, "--cwd", opts.Cwd)
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	}
	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(opts.AllowedTools, ","))
	}
	if opts.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.AppendSystemPrompt)
	}

	cmd := exec.CommandContext(ctx, a.CLIPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start claude: %w", err)
	}

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		msg, err := ParseStreamLine(line)
		if err != nil {
			handler(Event{Type: "error", Text: fmt.Sprintf("parse error: %v", err)})
			continue
		}
		a.dispatch(msg, handler)
	}

	return cmd.Wait()
}

func (a *Adapter) dispatch(msg *StreamMessage, handler EventHandler) {
	switch msg.Type {
	case MsgSystem:
		if msg.SessionID != "" {
			handler(Event{Type: "session_init", SessionID: msg.SessionID})
		}

	case MsgAssistant:
		am, err := ParseAssistantMessage(msg.Message)
		if err != nil {
			return
		}
		for _, block := range am.Content {
			switch block.Type {
			case "text":
				handler(Event{Type: "text", Text: block.Text})
			case "tool_use":
				inputStr, _ := json.Marshal(block.Input)
				handler(Event{
					Type:      "tool_use",
					ToolName:  block.Name,
					ToolInput: string(inputStr),
					ToolID:    block.ID,
				})
			}
		}

	case MsgUser:
		um, err := ParseUserMessage(msg.Message)
		if err != nil {
			return
		}
		for _, block := range um.Content {
			handler(Event{
				Type:   "tool_result",
				ToolID: block.ToolUseID,
				Text:   block.Content,
			})
		}

	case MsgResult:
		handler(Event{
			Type:       "result",
			Text:       msg.Result,
			SessionID:  msg.SessionID,
			CostUSD:    msg.CostUSD,
			Usage:      msg.Usage,
			DurationMs: msg.DurationMs,
		})
	}
}

type RunOptions struct {
	Prompt             string
	Cwd                string
	ResumeSessionID    string
	AllowedTools       []string
	AppendSystemPrompt string
}
```

**Step 2: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功

**Step 3: 提交**

```bash
git add internal/claude/adapter.go
git commit -m "feat: add claude CLI subprocess adapter with stream parsing"
```

---

## Phase 3: 飞书集成层

### Task 5: 飞书客户端封装

**Files:**
- Create: `internal/feishu/client.go`

**Step 1: 封装飞书 API（创建群、拉人、发消息、更新卡片）**

Create `internal/feishu/client.go`:

```go
package feishu

import (
	"context"
	"encoding/json"
	"fmt"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
)

type Client struct {
	api    *lark.Client
	botName string
}

func NewClient(appID, appSecret, botName string) *Client {
	api := lark.NewClient(appID, appSecret,
		lark.WithLogLevel(larkcore.LogLevelInfo),
	)
	return &Client{api: api, botName: botName}
}

// CreateGroup 创建群聊并返回 chat_id
func (c *Client) CreateGroup(ctx context.Context, name string) (string, error) {
	req := larkim.NewCreateChatReqBuilder().
		Body(larkim.NewCreateChatReqBodyBuilder().
			Name(name).
			ChatMode("group").
			ChatType("private").
			Build()).
		Build()

	resp, err := c.api.Im.Chat.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("create chat: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("create chat failed: %s", resp.Msg)
	}
	return *resp.Data.ChatId, nil
}

// AddMember 拉人入群
func (c *Client) AddMember(ctx context.Context, chatID, userOpenID string) error {
	req := larkim.NewCreateChatMembersReqBuilder().
		ChatId(chatID).
		MemberIdType("open_id").
		Body(larkim.NewCreateChatMembersReqBodyBuilder().
			IdList([]string{userOpenID}).
			Build()).
		Build()

	resp, err := c.api.Im.ChatMembers.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("add member failed: %s", resp.Msg)
	}
	return nil
}

// SendText 发送文本消息
func (c *Client) SendText(ctx context.Context, chatID, text string) (string, error) {
	content, _ := json.Marshal(map[string]string{"text": text})
	return c.sendMessage(ctx, chatID, "text", string(content))
}

// SendCard 发送交互卡片
func (c *Client) SendCard(ctx context.Context, chatID string, card interface{}) (string, error) {
	content, err := json.Marshal(card)
	if err != nil {
		return "", fmt.Errorf("marshal card: %w", err)
	}
	return c.sendMessage(ctx, chatID, "interactive", string(content))
}

// UpdateCard 更新卡片内容（用于流式输出）
func (c *Client) UpdateCard(ctx context.Context, messageID string, card interface{}) error {
	content, err := json.Marshal(card)
	if err != nil {
		return fmt.Errorf("marshal card: %w", err)
	}

	req := larkim.NewPatchMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewPatchMessageReqBodyBuilder().
			Content(string(content)).
			Build()).
		Build()

	resp, err := c.api.Im.Message.Patch(ctx, req)
	if err != nil {
		return fmt.Errorf("update card: %w", err)
	}
	if !resp.Success() {
		return fmt.Errorf("update card failed: %s", resp.Msg)
	}
	return nil
}

func (c *Client) sendMessage(ctx context.Context, chatID, msgType, content string) (string, error) {
	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType("chat_id").
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(chatID).
			MsgType(msgType).
			Content(content).
			Build()).
		Build()

	resp, err := c.api.Im.Message.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("send message: %w", err)
	}
	if !resp.Success() {
		return "", fmt.Errorf("send message failed: %s", resp.Msg)
	}
	return *resp.Data.MessageId, nil
}

func (c *Client) LarkAPI() *lark.Client {
	return c.api
}
```

**Step 2: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功

**Step 3: 提交**

```bash
git add internal/feishu/client.go
git commit -m "feat: add feishu client wrapper for chat, message, and card APIs"
```

---

### Task 6: 飞书卡片模板

**Files:**
- Create: `internal/feishu/card.go`

**Step 1: 实现卡片构建器（流式输出、审批、目录选择）**

Create `internal/feishu/card.go`:

```go
package feishu

import "fmt"

// StreamingCard 构建流式输出卡片
func StreamingCard(content string, isComplete bool, tokenInfo string) map[string]interface{} {
	headerColor := "blue"
	headerTitle := "Claude 回复中..."
	if isComplete {
		headerColor = "green"
		headerTitle = "Claude 回复完成"
	}

	elements := []interface{}{
		map[string]interface{}{
			"tag":     "markdown",
			"content": content,
		},
	}

	if tokenInfo != "" {
		elements = append(elements,
			map[string]string{"tag": "hr"},
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

// ApprovalCard 构建危险工具审批卡片
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

// CwdSelectionCard 构建工作目录选择卡片
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

// ConfirmCard 构建通用确认卡片
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
```

**Step 2: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功

**Step 3: 提交**

```bash
git add internal/feishu/card.go
git commit -m "feat: add feishu card templates for streaming, approval, and selection"
```

---

### Task 7: 飞书 WebSocket 事件监听

**Files:**
- Create: `internal/feishu/event.go`
- Create: `internal/feishu/callback.go`

**Step 1: 实现 WebSocket 事件监听与消息分发**

Create `internal/feishu/event.go`:

```go
package feishu

import (
	"context"
	"fmt"
	"log"

	larkevent "github.com/larksuite/oapi-sdk-go/v3/event"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// IncomingMessage 是从飞书接收到的消息
type IncomingMessage struct {
	ChatID    string
	MessageID string
	SenderID  string // open_id
	Text      string
	MsgType   string
	ChatType  string // "p2p" | "group"
}

// MessageHandler 是消息处理回调
type MessageHandler func(ctx context.Context, msg IncomingMessage)

// CardActionHandler 是卡片按钮回调
type CardActionHandler func(ctx context.Context, action CardAction) string

type CardAction struct {
	OpenID    string
	MessageID string
	Action    string
	Value     map[string]string
}

// EventListener 管理飞书 WebSocket 事件监听
type EventListener struct {
	appID          string
	appSecret      string
	onMessage      MessageHandler
	onCardAction   CardActionHandler
}

func NewEventListener(appID, appSecret string) *EventListener {
	return &EventListener{
		appID:     appID,
		appSecret: appSecret,
	}
}

func (el *EventListener) OnMessage(handler MessageHandler) {
	el.onMessage = handler
}

func (el *EventListener) OnCardAction(handler CardActionHandler) {
	el.onCardAction = handler
}

func (el *EventListener) Start(ctx context.Context) error {
	eventDispatcher := dispatcher.NewEventDispatcher("", "")

	// 注册消息接收事件
	eventDispatcher.OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
		if el.onMessage == nil {
			return nil
		}
		msg := event.Event.Message
		sender := event.Event.Sender

		incoming := IncomingMessage{
			ChatID:    *msg.ChatId,
			MessageID: *msg.MessageId,
			SenderID:  *sender.SenderId.OpenId,
			MsgType:   *msg.MessageType,
			ChatType:  *msg.ChatType,
		}

		// 解析文本内容
		if *msg.MessageType == "text" {
			incoming.Text = extractText(*msg.Content)
		}

		el.onMessage(ctx, incoming)
		return nil
	})

	// 注册卡片回调（通过自定义事件）
	if el.onCardAction != nil {
		eventDispatcher.OnCustomizedEvent("card.action.trigger", func(ctx context.Context, event *larkevent.EventReq) error {
			// 卡片回调需要通过 HTTP 回调处理，WebSocket 模式下需要额外配置
			log.Println("Card action received via customized event")
			return nil
		})
	}

	// 启动 WebSocket 客户端
	wsClient := larkws.NewClient(el.appID, el.appSecret,
		larkws.WithEventHandler(eventDispatcher),
		larkws.WithLogLevel(larkws.LogLevelInfo),
	)

	fmt.Println("Starting Feishu WebSocket connection...")
	return wsClient.Start(ctx)
}

func extractText(content string) string {
	// content 格式: {"text":"实际内容"}
	var c struct {
		Text string `json:"text"`
	}
	if err := jsonUnmarshal([]byte(content), &c); err != nil {
		return content
	}
	return c.Text
}
```

注意：`extractText` 中需要 json 包，在文件头部 import 中加入 `"encoding/json"` 并将 `jsonUnmarshal` 改为 `json.Unmarshal`。

**Step 2: 实现卡片回调处理器**

Create `internal/feishu/callback.go`:

```go
package feishu

import (
	"encoding/json"
	"sync"
)

// PendingAction 管理待处理的卡片交互
type PendingAction struct {
	mu      sync.Mutex
	pending map[string]chan ActionResult // key = request_id
}

type ActionResult struct {
	Action string
	Value  map[string]string
}

func NewPendingAction() *PendingAction {
	return &PendingAction{
		pending: make(map[string]chan ActionResult),
	}
}

// Wait 阻塞等待用户点击卡片按钮
func (pa *PendingAction) Wait(requestID string) chan ActionResult {
	ch := make(chan ActionResult, 1)
	pa.mu.Lock()
	pa.pending[requestID] = ch
	pa.mu.Unlock()
	return ch
}

// Resolve 用户点击后回调
func (pa *PendingAction) Resolve(requestID string, result ActionResult) bool {
	pa.mu.Lock()
	ch, ok := pa.pending[requestID]
	if ok {
		delete(pa.pending, requestID)
	}
	pa.mu.Unlock()

	if !ok {
		return false
	}
	ch <- result
	close(ch)
	return true
}

// ResolveAll 关闭时拒绝所有待处理
func (pa *PendingAction) ResolveAll(result ActionResult) {
	pa.mu.Lock()
	for id, ch := range pa.pending {
		ch <- result
		close(ch)
		delete(pa.pending, id)
	}
	pa.mu.Unlock()
}

// ParseCardCallback 解析飞书卡片回调 body
func ParseCardCallback(body []byte) (*CardAction, error) {
	var cb struct {
		OpenID string `json:"open_id"`
		Action struct {
			Value json.RawMessage `json:"value"`
		} `json:"action"`
	}
	if err := json.Unmarshal(body, &cb); err != nil {
		return nil, err
	}

	var value map[string]string
	if err := json.Unmarshal(cb.Action.Value, &value); err != nil {
		return nil, err
	}

	return &CardAction{
		OpenID: cb.OpenID,
		Action: value["action"],
		Value:  value,
	}, nil
}
```

**Step 3: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功（可能需要修复 import）

**Step 4: 提交**

```bash
git add internal/feishu/event.go internal/feishu/callback.go
git commit -m "feat: add feishu websocket event listener and card callback handler"
```

---

## Phase 4: 意图分类与路由

### Task 8: 意图分类器

**Files:**
- Create: `internal/intent/model.go`
- Create: `internal/intent/classifier.go`

**Step 1: 定义意图数据模型**

Create `internal/intent/model.go`:

```go
package intent

// IntentType 意图类型
type IntentType string

const (
	IntentDirect  IntentType = "direct"  // 即时指令，主Bot直接处理
	IntentSession IntentType = "session" // 需要 session 的对话
	IntentSystem  IntentType = "system"  // 系统管理
)

// SystemAction 系统管理子类型
type SystemAction string

const (
	ActionListSessions SystemAction = "list_sessions"
	ActionCloseSession SystemAction = "close_session"
	ActionAddTag       SystemAction = "add_tag"
	ActionRemoveTag    SystemAction = "remove_tag"
	ActionAddCron      SystemAction = "add_cron"
	ActionListCron     SystemAction = "list_cron"
	ActionToggleCron   SystemAction = "toggle_cron"
	ActionDeleteCron   SystemAction = "delete_cron"
	ActionStatus       SystemAction = "status"
)

// ClassifyResult 意图分类结果
type ClassifyResult struct {
	Intent       IntentType   `json:"intent"`
	Topic        string       `json:"topic,omitempty"`
	Tags         []string     `json:"tags,omitempty"`
	SystemAction SystemAction `json:"system_action,omitempty"`
	Params       map[string]string `json:"params,omitempty"`

	// add_cron 专用
	CronScheduleHint string `json:"cron_schedule_hint,omitempty"`
	CronPrompt       string `json:"cron_prompt,omitempty"`
	CronName         string `json:"cron_name,omitempty"`
}
```

**Step 2: 实现 Anthropic API 调用**

Create `internal/intent/classifier.go`:

```go
package intent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"agentctl/internal/session"
)

type Classifier struct {
	apiKey string
	model  string
}

func NewClassifier(apiKey, model string) *Classifier {
	return &Classifier{apiKey: apiKey, model: model}
}

const systemPrompt = `你是意图分类器。根据用户消息和现有sessions列表，返回JSON。
不要输出其他任何内容，只输出纯JSON。

意图类型：
- "direct": 即时指令，一问一答，不需要代码库上下文。如：翻译、查询、计算、申请权限
- "session": 需要操作代码的多轮对话。如：代码审查、写功能、修bug、重构
- "system": 管理系统自身。如：列出会话、关闭会话、管理标签、管理定时任务、系统状态

系统管理子类型：
- list_sessions: 列出会话
- close_session: 关闭会话
- add_tag / remove_tag: 管理标签（params.tag_name）
- add_cron: 添加定时任务（提取 cron_name, cron_schedule_hint, cron_prompt）
- list_cron: 列出定时任务
- toggle_cron: 启停定时任务（params.cron_name）
- delete_cron: 删除定时任务（params.cron_name）
- status: 系统状态

返回格式：
{"intent":"direct|session|system","topic":"主题摘要","tags":["关键词"],"system_action":"子类型","params":{},"cron_schedule_hint":"","cron_prompt":"","cron_name":""}`

func (c *Classifier) Classify(userMsg string, activeSessions []*session.Session) (*ClassifyResult, error) {
	sessionsSummary := "当前无活跃会话"
	if len(activeSessions) > 0 {
		var summaries []string
		for _, s := range activeSessions {
			summaries = append(summaries, fmt.Sprintf("- [%s] tags:%v status:%s", s.Name, s.Tags, s.Status))
		}
		sessionsSummary = "当前活跃会话:\n"
		for _, s := range summaries {
			sessionsSummary += s + "\n"
		}
	}

	userContent := fmt.Sprintf("用户消息: %s\n\n%s", userMsg, sessionsSummary)

	reqBody := map[string]interface{}{
		"model":      c.model,
		"max_tokens": 500,
		"messages": []map[string]string{
			{"role": "user", "content": userContent},
		},
		"system": systemPrompt,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", c.apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anthropic api: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("anthropic api %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("parse response: %w", err)
	}
	if len(apiResp.Content) == 0 {
		return nil, fmt.Errorf("empty response")
	}

	var result ClassifyResult
	if err := json.Unmarshal([]byte(apiResp.Content[0].Text), &result); err != nil {
		return nil, fmt.Errorf("parse intent: %w (raw: %s)", err, apiResp.Content[0].Text)
	}
	return &result, nil
}
```

**Step 3: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功

**Step 4: 提交**

```bash
git add internal/intent/
git commit -m "feat: add intent classifier using Anthropic API (haiku)"
```

---

### Task 9: 主 Bot 路由器

**Files:**
- Create: `internal/router/router.go`

**Step 1: 实现三路路由逻辑**

Create `internal/router/router.go`:

```go
package router

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"agentctl/internal/claude"
	"agentctl/internal/config"
	"agentctl/internal/feishu"
	"agentctl/internal/intent"
	"agentctl/internal/session"
)

type Router struct {
	cfg        *config.Config
	feishuCli  *feishu.Client
	classifier *intent.Classifier
	store      *session.Store
	adapter    *claude.Adapter
	pending    *feishu.PendingAction
}

func New(cfg *config.Config, feishuCli *feishu.Client, classifier *intent.Classifier,
	store *session.Store, adapter *claude.Adapter, pending *feishu.PendingAction) *Router {
	return &Router{
		cfg:        cfg,
		feishuCli:  feishuCli,
		classifier: classifier,
		store:      store,
		adapter:    adapter,
		pending:    pending,
	}
}

// HandleRouterMessage 处理主Bot群的消息
func (r *Router) HandleRouterMessage(ctx context.Context, msg feishu.IncomingMessage) {
	// 意图分类
	activeSessions := r.store.ListActive()
	result, err := r.classifier.Classify(msg.Text, activeSessions)
	if err != nil {
		log.Printf("classify error: %v", err)
		r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("意图识别失败: %v", err))
		return
	}

	switch result.Intent {
	case intent.IntentDirect:
		r.handleDirect(ctx, msg, result)
	case intent.IntentSession:
		r.handleSession(ctx, msg, result)
	case intent.IntentSystem:
		r.handleSystem(ctx, msg, result)
	}
}

func (r *Router) handleDirect(ctx context.Context, msg feishu.IncomingMessage, result *intent.ClassifyResult) {
	// 即时指令：fork 一次 claude CLI，不建 session
	var fullText strings.Builder
	r.adapter.Run(ctx, claude.RunOptions{
		Prompt:       msg.Text,
		Cwd:          r.cfg.DefaultCwd,
		AllowedTools: []string{"Read", "Bash", "WebSearch", "WebFetch"},
	}, func(event claude.Event) {
		if event.Type == "text" {
			fullText.WriteString(event.Text)
		}
	})

	text := fullText.String()
	if text == "" {
		text = "（无输出）"
	}
	r.feishuCli.SendText(ctx, msg.ChatID, text)
}

func (r *Router) handleSession(ctx context.Context, msg feishu.IncomingMessage, result *intent.ClassifyResult) {
	// 匹配已有 sessions
	candidates := r.matchSessions(result.Tags, result.Topic)

	if len(candidates) > 0 {
		// 列出候选
		var sb strings.Builder
		sb.WriteString("找到相关会话：\n")
		for i, s := range candidates {
			ago := time.Since(s.LastActiveAt).Truncate(time.Minute)
			sb.WriteString(fmt.Sprintf("%d. [%s] %s前活跃\n", i+1, s.Name, ago))
		}
		sb.WriteString("\n回复序号复用，或回复「新建」创建新会话")
		r.feishuCli.SendText(ctx, msg.ChatID, sb.String())
		// TODO: 等待用户回复选择，暂时简化为直接创建
		return
	}

	// 无候选 → 创建新 session
	r.startSessionCreation(ctx, msg, result)
}

func (r *Router) startSessionCreation(ctx context.Context, msg feishu.IncomingMessage, result *intent.ClassifyResult) {
	requestID := uuid.New().String()

	// 发送工作目录选择卡片
	card := feishu.CwdSelectionCard(r.cfg.Repos, r.cfg.DefaultCwd, requestID)
	r.feishuCli.SendCard(ctx, msg.ChatID, card)

	// 等待用户选择
	ch := r.pending.Wait(requestID)
	go func() {
		select {
		case action := <-ch:
			cwd := action.Value["cwd"]
			if cwd == "" {
				cwd = r.cfg.DefaultCwd
			}
			r.createSession(ctx, msg, result, cwd)
		case <-time.After(5 * time.Minute):
			r.feishuCli.SendText(ctx, msg.ChatID, "选择超时，请重新发送")
		}
	}()
}

func (r *Router) createSession(ctx context.Context, msg feishu.IncomingMessage, result *intent.ClassifyResult, cwd string) {
	name := result.Topic
	if name == "" {
		name = "新会话"
	}
	groupName := fmt.Sprintf("[Claude] %s", name)

	// 创建飞书群
	chatID, err := r.feishuCli.CreateGroup(ctx, groupName)
	if err != nil {
		r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("创建群失败: %v", err))
		return
	}

	// 拉用户入群
	if err := r.feishuCli.AddMember(ctx, chatID, msg.SenderID); err != nil {
		log.Printf("add member error: %v", err)
	}

	// 创建 session 记录
	sess := &session.Session{
		ID:           uuid.New().String(),
		ChatID:       chatID,
		Name:         name,
		Tags:         result.Tags,
		WorkingDir:   cwd,
		Status:       session.StatusActive,
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
	r.store.Put(sess)
	r.store.Save()

	// 在新群发送欢迎消息
	r.feishuCli.SendText(ctx, chatID, fmt.Sprintf("会话已创建\n主题: %s\n工作目录: %s\n\n正在处理你的请求...", name, cwd))

	// 将原始问题转发到新群，触发首次对话
	r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("已创建新会话 [%s]，请到新群继续", name))
}

func (r *Router) handleSystem(ctx context.Context, msg feishu.IncomingMessage, result *intent.ClassifyResult) {
	switch result.SystemAction {
	case intent.ActionListSessions:
		sessions := r.store.ListActive()
		if len(sessions) == 0 {
			r.feishuCli.SendText(ctx, msg.ChatID, "当前没有活跃会话")
			return
		}
		var sb strings.Builder
		sb.WriteString("活跃会话：\n")
		for _, s := range sessions {
			ago := time.Since(s.LastActiveAt).Truncate(time.Minute)
			sb.WriteString(fmt.Sprintf("• [%s] tags:%v %s前活跃 (%s)\n", s.Name, s.Tags, ago, s.Status))
		}
		r.feishuCli.SendText(ctx, msg.ChatID, sb.String())

	case intent.ActionStatus:
		sessions := r.store.ListActive()
		r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("系统状态：\n活跃会话: %d\n默认目录: %s", len(sessions), r.cfg.DefaultCwd))

	default:
		r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("系统操作 %s 暂未实现", result.SystemAction))
	}
}

func (r *Router) matchSessions(tags []string, topic string) []*session.Session {
	active := r.store.ListActive()
	if len(tags) == 0 {
		return nil
	}

	type scored struct {
		sess  *session.Session
		score int
	}
	var results []scored

	for _, s := range active {
		score := 0
		for _, tag := range tags {
			for _, st := range s.Tags {
				if strings.EqualFold(tag, st) {
					score++
				}
			}
		}
		if score > 0 {
			results = append(results, scored{s, score})
		}
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		return results[i].sess.LastActiveAt.After(results[j].sess.LastActiveAt)
	})

	var out []*session.Session
	for _, r := range results {
		out = append(out, r.sess)
		if len(out) >= 5 {
			break
		}
	}
	return out
}
```

**Step 2: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功

**Step 3: 提交**

```bash
git add internal/router/
git commit -m "feat: add main bot three-way router (direct/session/system)"
```

---

## Phase 5: Session 群对话引擎

### Task 10: Session 消息处理（流式卡片输出）

**Files:**
- Create: `internal/session/handler.go`

**Step 1: 实现 session 群的消息处理逻辑（流式卡片 + 危险工具审批）**

Create `internal/session/handler.go`:

```go
package session

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"agentctl/internal/claude"
	"agentctl/internal/config"
	"agentctl/internal/feishu"
)

type Handler struct {
	cfg       *config.Config
	feishuCli *feishu.Client
	store     *Store
	adapter   *claude.Adapter
	pending   *feishu.PendingAction

	// 防止同一 session 并发执行
	locks sync.Map // key=sessionID
}

func NewHandler(cfg *config.Config, feishuCli *feishu.Client, store *Store,
	adapter *claude.Adapter, pending *feishu.PendingAction) *Handler {
	return &Handler{
		cfg:       cfg,
		feishuCli: feishuCli,
		store:     store,
		adapter:   adapter,
		pending:   pending,
	}
}

func (h *Handler) HandleMessage(ctx context.Context, msg feishu.IncomingMessage) {
	sess := h.store.GetByChatID(msg.ChatID)
	if sess == nil {
		return // 不是 session 群
	}

	if sess.Status == StatusClosed {
		h.feishuCli.SendText(ctx, msg.ChatID, "此会话已关闭，请到主群创建新会话")
		return
	}

	// 会话锁
	if _, loaded := h.locks.LoadOrStore(sess.ID, true); loaded {
		h.feishuCli.SendText(ctx, msg.ChatID, "上一个请求正在处理中，请稍候...")
		return
	}
	defer h.locks.Delete(sess.ID)

	// 更新状态
	sess.Status = StatusActive
	sess.LastActiveAt = time.Now()
	h.store.Put(sess)

	// 发送初始卡片
	card := feishu.StreamingCard("正在思考...", false, "")
	cardMsgID, err := h.feishuCli.SendCard(ctx, msg.ChatID, card)
	if err != nil {
		log.Printf("send card error: %v", err)
		return
	}

	// 流式处理
	var (
		textBuf    strings.Builder
		lastUpdate time.Time
		throttle   = time.Second // 每秒最多更新一次
	)

	h.adapter.Run(ctx, claude.RunOptions{
		Prompt:          msg.Text,
		Cwd:             sess.WorkingDir,
		ResumeSessionID: sess.CLISessionID,
		AllowedTools:    []string{"Read", "Write", "Edit", "Bash", "Glob", "Grep", "WebFetch", "WebSearch"},
	}, func(event claude.Event) {
		switch event.Type {
		case "session_init":
			sess.CLISessionID = event.SessionID
			h.store.Put(sess)
			h.store.Save()

		case "text":
			textBuf.WriteString(event.Text)
			if time.Since(lastUpdate) > throttle {
				card := feishu.StreamingCard(textBuf.String(), false, "")
				h.feishuCli.UpdateCard(ctx, cardMsgID, card)
				lastUpdate = time.Now()
			}

		case "tool_use":
			if h.isDangerous(event.ToolName, event.ToolInput) {
				h.handleDangerousTool(ctx, msg.ChatID, event)
			}
			textBuf.WriteString(fmt.Sprintf("\n\n🔧 **%s** 执行中...\n", event.ToolName))

		case "tool_result":
			// 摘要工具结果（截断过长内容）
			resultText := event.Text
			if len(resultText) > 500 {
				resultText = resultText[:500] + "...(已截断)"
			}
			textBuf.WriteString(fmt.Sprintf("```\n%s\n```\n", resultText))

		case "result":
			tokenInfo := ""
			if event.Usage != nil {
				tokenInfo = fmt.Sprintf("✅ Input: %d | Output: %d | Cost: $%.4f",
					event.Usage.InputTokens, event.Usage.OutputTokens, event.CostUSD)
			}
			finalText := textBuf.String()
			if finalText == "" {
				finalText = event.Text
			}
			card := feishu.StreamingCard(finalText, true, tokenInfo)
			h.feishuCli.UpdateCard(ctx, cardMsgID, card)
		}
	})

	h.store.Save()
}

func (h *Handler) isDangerous(toolName, toolInput string) bool {
	for _, pattern := range h.cfg.DangerousTools {
		if strings.Contains(strings.ToLower(toolName+toolInput), strings.ToLower(pattern)) {
			return true
		}
	}
	return false
}

func (h *Handler) handleDangerousTool(ctx context.Context, chatID string, event claude.Event) {
	requestID := event.ToolID
	card := feishu.ApprovalCard(event.ToolName, event.ToolInput, requestID)
	h.feishuCli.SendCard(ctx, chatID, card)

	// 阻塞等待审批
	ch := h.pending.Wait(requestID)
	select {
	case result := <-ch:
		if result.Action != "approve" {
			log.Printf("Tool %s denied by user", event.ToolName)
		}
	case <-time.After(5 * time.Minute):
		log.Printf("Tool approval timeout for %s", event.ToolName)
	}
}
```

**Step 2: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功

**Step 3: 提交**

```bash
git add internal/session/handler.go
git commit -m "feat: add session message handler with streaming card and tool approval"
```

---

## Phase 6: 定时任务

### Task 11: Cron 调度器

**Files:**
- Create: `internal/cron/scheduler.go`
- Create: `internal/cron/store.go`

**Step 1: 实现 cron 持久化 store**

Create `internal/cron/store.go`:

```go
package cron

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

type Store struct {
	mu       sync.RWMutex
	jobs     map[string]*CronJob
	filePath string
}

func NewStore(dataDir string) (*Store, error) {
	fp := filepath.Join(dataDir, "cron_jobs.json")
	s := &Store{
		jobs:     make(map[string]*CronJob),
		filePath: fp,
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.filePath)
	if err != nil {
		return err
	}
	var jobs []*CronJob
	if err := json.Unmarshal(data, &jobs); err != nil {
		return fmt.Errorf("parse cron jobs: %w", err)
	}
	for _, j := range jobs {
		s.jobs[j.ID] = j
	}
	return nil
}

func (s *Store) Save() error {
	s.mu.RLock()
	jobs := make([]*CronJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		jobs = append(jobs, j)
	}
	s.mu.RUnlock()

	data, err := json.MarshalIndent(jobs, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.filePath + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	return os.Rename(tmp, s.filePath)
}

func (s *Store) Put(job *CronJob) {
	s.mu.Lock()
	s.jobs[job.ID] = job
	s.mu.Unlock()
}

func (s *Store) Delete(id string) {
	s.mu.Lock()
	delete(s.jobs, id)
	s.mu.Unlock()
}

func (s *Store) Get(id string) *CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.jobs[id]
}

func (s *Store) ListEnabled() []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*CronJob
	for _, j := range s.jobs {
		if j.Enabled {
			result = append(result, j)
		}
	}
	return result
}

func (s *Store) ListAll() []*CronJob {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]*CronJob, 0, len(s.jobs))
	for _, j := range s.jobs {
		result = append(result, j)
	}
	return result
}
```

**Step 2: 实现调度器**

Create `internal/cron/scheduler.go`:

```go
package cron

import (
	"context"
	"log"
	"strings"

	"agentctl/internal/claude"
	"agentctl/internal/config"
	"agentctl/internal/feishu"

	cronlib "github.com/robfig/cron/v3"
)

type Scheduler struct {
	cron      *cronlib.Cron
	store     *Store
	cfg       *config.Config
	adapter   *claude.Adapter
	feishuCli *feishu.Client
	entryMap  map[string]cronlib.EntryID // job.ID → entry
}

func NewScheduler(store *Store, cfg *config.Config, adapter *claude.Adapter, feishuCli *feishu.Client) *Scheduler {
	return &Scheduler{
		cron:      cronlib.New(),
		store:     store,
		cfg:       cfg,
		adapter:   adapter,
		feishuCli: feishuCli,
		entryMap:  make(map[string]cronlib.EntryID),
	}
}

func (s *Scheduler) Start() {
	// 加载所有 enabled jobs
	for _, job := range s.store.ListEnabled() {
		s.addJob(job)
	}
	s.cron.Start()
	log.Printf("Cron scheduler started with %d jobs", len(s.entryMap))
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}

func (s *Scheduler) Reload() {
	// 清除所有 entries
	for id, entryID := range s.entryMap {
		s.cron.Remove(entryID)
		delete(s.entryMap, id)
	}
	for _, job := range s.store.ListEnabled() {
		s.addJob(job)
	}
}

func (s *Scheduler) addJob(job *CronJob) {
	jobCopy := *job // capture
	entryID, err := s.cron.AddFunc(job.Cron, func() {
		s.executeJob(&jobCopy)
	})
	if err != nil {
		log.Printf("Failed to add cron job %s: %v", job.Name, err)
		return
	}
	s.entryMap[job.ID] = entryID
}

func (s *Scheduler) executeJob(job *CronJob) {
	ctx := context.Background()
	log.Printf("Executing cron job: %s", job.Name)

	cwd := job.WorkingDir
	if cwd == "" {
		cwd = s.cfg.DefaultCwd
	}

	targetChat := job.TargetChat
	if targetChat == "" {
		targetChat = s.cfg.RouterChatID
	}

	var fullText strings.Builder
	s.adapter.Run(ctx, claude.RunOptions{
		Prompt:       job.Prompt,
		Cwd:          cwd,
		AllowedTools: []string{"Read", "Bash", "WebSearch", "WebFetch"},
	}, func(event claude.Event) {
		if event.Type == "text" {
			fullText.WriteString(event.Text)
		}
	})

	result := fullText.String()
	if result == "" {
		result = "（定时任务无输出）"
	}

	text := "📅 **" + job.Name + "**\n\n" + result
	if _, err := s.feishuCli.SendText(ctx, targetChat, text); err != nil {
		log.Printf("Failed to send cron result: %v", err)
	}
}
```

**Step 3: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./...`
Expected: 编译成功

**Step 4: 提交**

```bash
git add internal/cron/
git commit -m "feat: add cron scheduler with persistent job store"
```

---

## Phase 7: 组装与启动

### Task 12: 组装 main.go（完整启动流程）

**Files:**
- Modify: `cmd/server/main.go`

**Step 1: 重写 main.go 组装所有组件**

```go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"agentctl/internal/claude"
	"agentctl/internal/config"
	"agentctl/internal/cron"
	"agentctl/internal/feishu"
	"agentctl/internal/intent"
	"agentctl/internal/router"
	"agentctl/internal/session"
)

func main() {
	dataDir := config.DefaultDataDir()

	// 确保数据目录存在
	os.MkdirAll(filepath.Join(dataDir, "data"), 0755)
	os.MkdirAll(filepath.Join(dataDir, "logs"), 0755)

	// 加载配置
	configPath := filepath.Join(dataDir, "config.json")
	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Load config: %v", err)
	}

	// 初始化各组件
	sessionStore, err := session.NewStore(filepath.Join(dataDir, "data"))
	if err != nil {
		log.Fatalf("Init session store: %v", err)
	}

	cronStore, err := cron.NewStore(filepath.Join(dataDir, "data"))
	if err != nil {
		log.Fatalf("Init cron store: %v", err)
	}

	feishuCli := feishu.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret, cfg.Feishu.BotName)
	cliAdapter := claude.NewAdapter(cfg.ClaudeCLIPath)
	classifier := intent.NewClassifier(cfg.Anthropic.APIKey, cfg.Anthropic.Model)
	pendingAction := feishu.NewPendingAction()

	// 路由器
	rt := router.New(cfg, feishuCli, classifier, sessionStore, cliAdapter, pendingAction)

	// Session 处理器
	sessHandler := session.NewHandler(cfg, feishuCli, sessionStore, cliAdapter, pendingAction)

	// Cron 调度器
	cronScheduler := cron.NewScheduler(cronStore, cfg, cliAdapter, feishuCli)
	cronScheduler.Start()
	defer cronScheduler.Stop()

	// 飞书事件监听
	eventListener := feishu.NewEventListener(cfg.Feishu.AppID, cfg.Feishu.AppSecret)
	eventListener.OnMessage(func(ctx context.Context, msg feishu.IncomingMessage) {
		// 判断来自哪个群
		if msg.ChatID == cfg.RouterChatID {
			// 主 Bot 群 → 路由
			go rt.HandleRouterMessage(ctx, msg)
		} else if sess := sessionStore.GetByChatID(msg.ChatID); sess != nil {
			// Session 群 → 对话处理
			go sessHandler.HandleMessage(ctx, msg)
		} else {
			// 未知群 → 忽略或回复
			log.Printf("Message from unknown chat: %s", msg.ChatID)
		}
	})

	// 启动
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 优雅关闭
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		sig := <-sigCh
		fmt.Printf("\nReceived %s, shutting down...\n", sig)
		pendingAction.ResolveAll(feishu.ActionResult{Action: "deny"})
		sessionStore.Save()
		cronStore.Save()
		cancel()
	}()

	fmt.Println("=== Agent for IM ===")
	fmt.Printf("Feishu App: %s\n", cfg.Feishu.AppID)
	fmt.Printf("Router Chat: %s\n", cfg.RouterChatID)
	fmt.Printf("Active sessions: %d\n", len(sessionStore.ListActive()))
	fmt.Printf("Cron jobs: %d\n", len(cronStore.ListEnabled()))
	fmt.Println("Starting Feishu WebSocket connection...")

	if err := eventListener.Start(ctx); err != nil {
		log.Fatalf("Event listener: %v", err)
	}
}
```

**Step 2: 验证编译**

Run: `cd /Users/bytedance/go/agentctl && go build ./cmd/server/`
Expected: 编译成功

**Step 3: 提交**

```bash
git add cmd/server/main.go
git commit -m "feat: assemble all components in main.go with graceful shutdown"
```

---

## Phase 8: 集成测试与调试

### Task 13: 端到端手动测试

**Step 1: 创建飞书应用**
- 在飞书开放平台创建应用，获取 App ID 和 App Secret
- 开启 Bot 功能
- 添加权限: im:chat, im:message, im:message:send_as_bot, im:chat.member:write
- 事件订阅: im.message.receive_v1，选择长连接模式
- 发布应用（需管理员审批）

**Step 2: 配置并启动**
- 复制 config.example.json 到 ~/.agent-for-im/config.json
- 填入 App ID, App Secret, Anthropic API Key
- 创建一个主 Bot 群，将群的 chat_id 填入 router_chat_id
- 运行: `go run ./cmd/server/`
- 确认日志显示 "connected to wss://..."

**Step 3: 测试即时指令**
- 在主 Bot 群发送: "今天天气怎么样"
- Expected: Bot 直接文本回复

**Step 4: 测试创建 session**
- 在主 Bot 群发送: "帮我看看 order 模块的代码"
- Expected: 弹出工作目录选择卡片 → 点击后创建新群 → 新群开始对话

**Step 5: 测试流式输出**
- 在 Session 群发送: "解释一下 main.go 的代码"
- Expected: 卡片流式更新，完成后显示 token 统计

**Step 6: 修复发现的问题，提交**

```bash
git add -A
git commit -m "fix: integration test fixes"
```

---

## 总结

| Phase | 内容 | Tasks |
|-------|------|-------|
| 1 | 项目骨架与基础设施 | Task 1-2 |
| 2 | Claude CLI 适配层 | Task 3-4 |
| 3 | 飞书集成层 | Task 5-7 |
| 4 | 意图分类与路由 | Task 8-9 |
| 5 | Session 群对话引擎 | Task 10 |
| 6 | 定时任务 | Task 11 |
| 7 | 组装与启动 | Task 12 |
| 8 | 集成测试 | Task 13 |

每个 Task 完成后都有独立提交，可随时回滚。Phase 1-7 是代码实现，Phase 8 是手动集成测试。
