package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

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

type EventHandler func(event Event)

type Adapter struct {
	CLIPath   string
	BaseURL   string
	AuthToken string
	tmux      *TmuxRunner
}

func NewAdapter(cliPath, baseURL, authToken, dataDir string) *Adapter {
	return &Adapter{
		CLIPath:   cliPath,
		BaseURL:   baseURL,
		AuthToken: authToken,
		tmux:      NewTmuxRunner(dataDir),
	}
}

// Stop cleans up all tmux sessions managed by this adapter.
func (a *Adapter) Stop() {
	a.tmux.Stop()
}

// SendAnswerToSession 向指定 Claude CLI session 发送用户答案
// 答案会被包装为 JSON 格式: {"answer": "用户选择的答案"}
func (a *Adapter) SendAnswerToSession(sessionID, answer string) error {
	// 使用 json.Marshal 安全构造 JSON(自动转义特殊字符)
	responseObj := map[string]string{"answer": answer}
	answerJSON, err := json.Marshal(responseObj)
	if err != nil {
		return fmt.Errorf("failed to marshal answer JSON: %w", err)
	}

	// 委托给 TmuxRunner.SendKeys
	if err := a.tmux.SendKeys(sessionID, string(answerJSON)); err != nil {
		return fmt.Errorf("failed to send answer to session: %w", err)
	}

	return nil
}

func (a *Adapter) envMap() map[string]string {
	env := make(map[string]string)
	if a.BaseURL != "" {
		env["ANTHROPIC_BASE_URL"] = a.BaseURL
	}
	if a.AuthToken != "" {
		env["ANTHROPIC_AUTH_TOKEN"] = a.AuthToken
	}
	return env
}

// RunOnceOptions 是 RunOnce 的可选参数
type RunOnceOptions struct {
	Model        string
	NoTools      bool
	SystemPrompt string // 如果非空，使用 --system-prompt 覆盖全局配置
}

// RunOnce executes a prompt via claude CLI and returns the text output.
// noTools=true disables all tool use (useful for pure text/JSON responses).
func (a *Adapter) RunOnce(ctx context.Context, prompt, model string, noTools bool) (string, error) {
	return a.RunOnceWithOptions(ctx, prompt, RunOnceOptions{
		Model:   model,
		NoTools: noTools,
	})
}

// RunOnceWithOptions 执行一次性 prompt，支持更多配置选项
func (a *Adapter) RunOnceWithOptions(ctx context.Context, prompt string, opts RunOnceOptions) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	args := []string{
		"--output-format", "text",
		"--max-turns", "5",
		"--dangerously-skip-permissions",
		"--no-session-persistence",
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if opts.NoTools {
		args = append(args, "--tools", "")
	}
	// 如果提供了 SystemPrompt，使用 --system-prompt 覆盖全局 CLAUDE.md
	if opts.SystemPrompt != "" {
		args = append(args, "--system-prompt", opts.SystemPrompt)
	}

	result, err := a.tmux.ExecCollect(ctx, a.envMap(), a.CLIPath, prompt, args, "")
	if err != nil {
		return "", fmt.Errorf("claude cli: %w", err)
	}
	return strings.TrimSpace(result), nil
}

// RunOnceResult 包含 RunOnceWithSession 的输出文本和 CLISessionID
type RunOnceResult struct {
	Text      string
	SessionID string
}

// RunOnceWithSession 执行一次性 prompt，返回文本输出和 CLISessionID。
// 用于历史上下文注入：需要拿到 session ID 以便后续群聊消息用 --resume 继续。
func (a *Adapter) RunOnceWithSession(ctx context.Context, prompt, cwd string) (*RunOnceResult, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
		"--max-turns", "1",
		"--tools", "",
	}

	var result RunOnceResult
	err := a.tmux.ExecStream(ctx, a.envMap(), a.CLIPath, prompt, args, cwd, func(line string) {
		if len(line) == 0 {
			return
		}
		msg, err := ParseStreamLine([]byte(line))
		if err != nil {
			return
		}
		switch msg.Type {
		case MsgSystem:
			if msg.SessionID != "" {
				result.SessionID = msg.SessionID
			}
		case MsgResult:
			result.Text = msg.Result
			if result.SessionID == "" && msg.SessionID != "" {
				result.SessionID = msg.SessionID
			}
		}
	})
	if err != nil {
		return nil, fmt.Errorf("claude cli: %w", err)
	}
	return &result, nil
}

// Run executes a streaming conversation via claude CLI in a tmux session.
func (a *Adapter) Run(ctx context.Context, opts RunOptions, handler EventHandler) error {
	args := []string{
		"--output-format", "stream-json",
		"--verbose",
		"--dangerously-skip-permissions",
	}
	if opts.ResumeSessionID != "" {
		args = append(args, "--resume", opts.ResumeSessionID)
	}
	if opts.Model != "" {
		args = append(args, "--model", opts.Model)
	}
	if len(opts.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(opts.AllowedTools, ","))
	}
	if opts.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", opts.AppendSystemPrompt)
	}

	return a.tmux.ExecStream(ctx, a.envMap(), a.CLIPath, opts.Prompt, args, opts.Cwd, func(line string) {
		if len(line) == 0 {
			return
		}
		msg, err := ParseStreamLine([]byte(line))
		if err != nil {
			handler(Event{Type: "error", Text: fmt.Sprintf("parse error: %v", err)})
			return
		}
		a.dispatch(msg, handler)
	})
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
	Model              string
	AllowedTools       []string
	AppendSystemPrompt string
}
