package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/wmgx/agentctl/internal/claude"
	"github.com/wmgx/agentctl/internal/config"
	"github.com/wmgx/agentctl/internal/cron"
	"github.com/wmgx/agentctl/internal/feishu"
	"github.com/wmgx/agentctl/internal/intent"
	"github.com/wmgx/agentctl/internal/router"
	"github.com/wmgx/agentctl/internal/session"
)

// acquireLock creates an exclusive file lock to ensure only one server instance runs.
// Returns the lock file handle; caller must close it on exit.
func acquireLock(dataDir string) *os.File {
	lockPath := filepath.Join(dataDir, "server.lock")
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		log.Fatalf("Cannot open lock file %s: %v", lockPath, err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		log.Fatalf("Another instance is already running (lock held by %s). Exiting.", lockPath)
	}
	// Write current PID into the lock file for diagnostics.
	f.Truncate(0)
	fmt.Fprintf(f, "%d\n", os.Getpid())
	return f
}

func main() {
	dataDir := config.DefaultDataDir()

	// 确保各目录存在
	os.MkdirAll(filepath.Join(dataDir, "data"), 0755)
	os.MkdirAll(filepath.Join(dataDir, "logs"), 0755)
	os.MkdirAll(filepath.Join(dataDir, "work_space"), 0755)

	// 单例限制：同一台机器只允许运行一个实例
	lockFile := acquireLock(dataDir)
	defer lockFile.Close()

	// 检测配置，首次运行时交互式引导
	configPath := filepath.Join(dataDir, "config.json")
	created, err := config.EnsureConfig(configPath)
	if err != nil {
		log.Fatalf("Setup config: %v", err)
	}
	if created {
		fmt.Println()
	}

	// 加载配置
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

	// 确保 prompts 目录和默认文件存在（用户可自行编辑）
	promptsDir := filepath.Join(dataDir, "prompts")
	if err := intent.EnsureDefaultPrompts(promptsDir); err != nil {
		log.Printf("Warning: failed to init prompts dir: %v", err)
	}

	feishuCli := feishu.NewClient(cfg.Feishu.AppID, cfg.Feishu.AppSecret, cfg.Feishu.BotName)
	cliAdapter := claude.NewAdapter(cfg.ClaudeCLIPath, cfg.Anthropic.BaseURL, cfg.Anthropic.AuthToken, dataDir)
	defer cliAdapter.Stop()
	promptFile := filepath.Join(promptsDir, "classifier.md")
	classifier := intent.NewClassifier(cliAdapter, cfg.Anthropic.Model, cfg.ChainUpgradeThreshold, promptFile)
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
	// appCtx is the application-level context for long-running goroutines.
	// We cannot use the feishu event handler's ctx because it gets cancelled
	// as soon as the handler returns.
	appCtx, appCancel := context.WithCancel(context.Background())

	eventListener.OnCardAction(func(_ context.Context, action feishu.CardAction) string {
		requestID := action.Value["request_id"]
		if requestID == "" {
			log.Printf("[main] card action missing request_id: %+v", action)
			return ""
		}
		pendingAction.Resolve(requestID, feishu.ActionResult{
			Action: action.Action,
			Value:  action.Value,
		})
		return ""
	})

	eventListener.OnMessage(func(_ context.Context, msg feishu.IncomingMessage) {
		log.Printf("[main] dispatch: chat_type=%s, chat_id=%s, text=%q", msg.ChatType, msg.ChatID, msg.Text)
		if msg.ChatType == "p2p" {
			go func() {
				reactionID, _ := feishuCli.AddReaction(appCtx, msg.MessageID, "OnIt")
				rt.HandleRouterMessage(appCtx, msg)
				if reactionID != "" {
					feishuCli.RemoveReaction(appCtx, msg.MessageID, reactionID)
				}
			}()
		} else if sess := sessionStore.GetByChatID(msg.ChatID); sess != nil {
			go func() {
				reactionID, _ := feishuCli.AddReaction(appCtx, msg.MessageID, "OnIt")
				sessHandler.HandleMessage(appCtx, msg)
				if reactionID != "" {
					feishuCli.RemoveReaction(appCtx, msg.MessageID, reactionID)
				}
			}()
		} else {
			log.Printf("[main] unknown chat, ignoring: %s", msg.ChatID)
		}
	})

	eventListener.OnChatDisband(func(_ context.Context, chatID string) {
		if sess := sessionStore.GetByChatID(chatID); sess != nil {
			sess.Status = session.StatusClosed
			sessionStore.Save()
			log.Printf("[main] session closed due to chat disbanded: chat_id=%s session=%s", chatID, sess.ID)
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
		appCancel()
		pendingAction.ResolveAll(feishu.ActionResult{Action: "deny"})
		sessionStore.Save()
		cronStore.Save()
		cancel()
	}()

	fmt.Println("=== Agent for IM ===")
	fmt.Printf("Feishu App: %s\n", cfg.Feishu.AppID)
	fmt.Printf("Anthropic API: %s\n", cfg.Anthropic.BaseURL)
	fmt.Printf("Active sessions: %d\n", len(sessionStore.ListActive()))
	fmt.Printf("Cron jobs: %d\n", len(cronStore.ListEnabled()))
	fmt.Println("Starting Feishu WebSocket connection...")

	if err := eventListener.Start(ctx); err != nil {
		log.Fatalf("Event listener: %v", err)
	}
}
