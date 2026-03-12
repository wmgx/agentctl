package config

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type FeishuConfig struct {
	AppID     string `json:"app_id"`
	AppSecret string `json:"app_secret"`
	BotName   string `json:"bot_name"`
}

type AnthropicConfig struct {
	APIKey    string `json:"api_key"`
	Model     string `json:"model"`                // 默认 claude-haiku-4-5-20250929
	BaseURL   string `json:"base_url,omitempty"`   // 不配置则使用官方 https://api.anthropic.com
	AuthToken string `json:"auth_token,omitempty"` // 代理鉴权 token，不配置则用 api_key
}

type Config struct {
	Feishu                FeishuConfig      `json:"feishu"`
	Anthropic             AnthropicConfig   `json:"anthropic"`
	DefaultCwd            string            `json:"default_cwd"`
	Repos                 map[string]string `json:"repos"`
	IdleTimeoutMin        int               `json:"idle_timeout_min"`
	DangerousTools        []string          `json:"dangerous_tools"`
	ClaudeCLIPath         string            `json:"claude_cli_path"`
	SessionModel          string            `json:"session_model,omitempty"`           // 新 session 默认使用的模型，默认 claude-sonnet-4-5
	BotOpenID             string            `json:"bot_open_id,omitempty"`             // Bot 自身的 open_id，用于区分历史消息发送者
	ChainUpgradeThreshold int               `json:"chain_upgrade_threshold,omitempty"` // P2P 引用链触发升级群聊的轮数，默认 4
	LogRetentionDays      int               `json:"log_retention_days,omitempty"`      // 日志保留天数，超过后自动删除，默认 7
	CompactStream         bool              `json:"compact_stream,omitempty"`          // 流式输出时隐藏代码块，只显示过程和结果，默认 false

	path string // 配置文件路径，运行时赋值，不序列化
}

func DefaultDataDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentctl")
}

// EnsureConfig checks if config file exists. If not, runs interactive setup.
// Returns true if a new config was created.
func EnsureConfig(path string) (bool, error) {
	if _, err := os.Stat(path); err == nil {
		return false, nil
	}

	fmt.Println("=== 首次启动配置 ===")
	fmt.Println("未检测到配置文件，开始交互式配置。")

	reader := bufio.NewReader(os.Stdin)
	prompt := func(label, hint, defaultVal string) string {
		if hint != "" {
			fmt.Printf("%s (%s)", label, hint)
		} else {
			fmt.Printf("%s", label)
		}
		if defaultVal != "" {
			fmt.Printf(" [%s]", defaultVal)
		}
		fmt.Print(": ")
		line, _ := reader.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return defaultVal
		}
		return line
	}

	cfg := Config{
		IdleTimeoutMin: 30,
		ClaudeCLIPath:  "claude",
		Repos:          make(map[string]string),
		DangerousTools: []string{"rm ", "git push", "git reset"},
	}

	// 飞书配置
	fmt.Println("── 飞书应用 ──")
	cfg.Feishu.AppID = prompt("App ID", "飞书开放平台获取", "")
	cfg.Feishu.AppSecret = prompt("App Secret", "", "")
	cfg.Feishu.BotName = prompt("Bot 名称", "", "ClaudeBot")

	// Anthropic 配置
	fmt.Println("\n── Anthropic（意图分类用） ──")
	cfg.Anthropic.BaseURL = prompt("Base URL", "代理地址，直连官方留空", "")
	cfg.Anthropic.AuthToken = prompt("Auth Token", "代理鉴权 token，留空则用 API Key", "")
	cfg.Anthropic.APIKey = prompt("API Key", "sk-ant-...", "")
	cfg.Anthropic.Model = prompt("Model", "意图分类模型", "claude-haiku-4-5-20250929")

	// 工作目录
	fmt.Println("\n── 工作目录 ──")
	defaultCwd := filepath.Join(filepath.Dir(path), "work_space")
	cfg.DefaultCwd = prompt("默认工作目录", "", defaultCwd)

	// Claude CLI（自动检测）
	if cliPath, err := exec.LookPath("claude"); err == nil {
		fmt.Printf("── Claude CLI ──\n已自动检测到: %s\n", cliPath)
		cfg.ClaudeCLIPath = cliPath
	} else {
		fmt.Println("\n── Claude CLI ──")
		fmt.Println("未在 PATH 中找到 claude，请手动指定路径")
		cfg.ClaudeCLIPath = prompt("Claude CLI 路径", "", "claude")
	}

	// 项目仓库（可选）
	fmt.Println("\n── 项目仓库（可选，直接回车跳过） ──")
	for {
		name := prompt("仓库名称", "如 order，输入空跳过", "")
		if name == "" {
			break
		}
		repoPath := prompt("仓库路径", "", "")
		if repoPath != "" {
			cfg.Repos[name] = repoPath
		}
	}

	// 写入
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return false, fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return false, fmt.Errorf("create config dir: %w", err)
	}

	if err := os.WriteFile(path, data, 0600); err != nil {
		return false, fmt.Errorf("write config: %w", err)
	}

	fmt.Printf("\n配置已保存到 %s\n", path)

	// 检查必填项
	var missing []string
	if cfg.Feishu.AppID == "" {
		missing = append(missing, "feishu.app_id")
	}
	if cfg.Feishu.AppSecret == "" {
		missing = append(missing, "feishu.app_secret")
	}
	if len(missing) > 0 {
		fmt.Printf("⚠ 以下必填项为空，请编辑配置文件补充: %s\n", strings.Join(missing, ", "))
	}

	return true, nil
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
	cfg.path = path
	if cfg.IdleTimeoutMin == 0 {
		cfg.IdleTimeoutMin = 30
	}
	if cfg.ClaudeCLIPath == "" {
		cfg.ClaudeCLIPath = "claude"
	}
	if cfg.Anthropic.Model == "" {
		cfg.Anthropic.Model = "claude-haiku-4-5-20250929"
	}
	if cfg.Anthropic.BaseURL == "" {
		cfg.Anthropic.BaseURL = "https://api.anthropic.com"
	}
	if cfg.SessionModel == "" {
		cfg.SessionModel = "claude-sonnet-4-5"
	}
	if cfg.ChainUpgradeThreshold <= 0 {
		cfg.ChainUpgradeThreshold = 4
	}
	if cfg.LogRetentionDays <= 0 {
		cfg.LogRetentionDays = 7
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Save 将当前配置写回文件。
func (c *Config) Save() error {
	if c.path == "" {
		return fmt.Errorf("config path not set")
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	return os.WriteFile(c.path, data, 0600)
}

// AddRepo 将路径添加到 Repos（若未存在），用目录名作为 key，并持久化到文件。
// 返回 true 表示实际新增了条目。
func (c *Config) AddRepo(dirPath string) (bool, error) {
	if c.Repos == nil {
		c.Repos = make(map[string]string)
	}
	for _, v := range c.Repos {
		if v == dirPath {
			return false, nil // 已存在
		}
	}
	name := filepath.Base(dirPath)
	// 若名称已被占用，加数字后缀
	if _, exists := c.Repos[name]; exists {
		for i := 2; ; i++ {
			candidate := fmt.Sprintf("%s%d", name, i)
			if _, exists := c.Repos[candidate]; !exists {
				name = candidate
				break
			}
		}
	}
	c.Repos[name] = dirPath
	return true, c.Save()
}

func (c *Config) validate() error {
	var missing []string
	if c.Feishu.AppID == "" {
		missing = append(missing, "feishu.app_id")
	}
	if c.Feishu.AppSecret == "" {
		missing = append(missing, "feishu.app_secret")
	}
	if len(missing) > 0 {
		return fmt.Errorf("配置缺少必填项: %s，请编辑配置文件补充", strings.Join(missing, ", "))
	}
	return nil
}
