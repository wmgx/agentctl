package router

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/wmgx/agentctl/internal/claude"
	"github.com/wmgx/agentctl/internal/config"
	"github.com/wmgx/agentctl/internal/feishu"
	"github.com/wmgx/agentctl/internal/intent"
	"github.com/wmgx/agentctl/internal/session"
)

const streamThrottle = time.Second

// filterCodeBlocks 调用 feishu.FilterCodeBlocks 过滤代码块
func filterCodeBlocks(text string, compact bool) string {
	return feishu.FilterCodeBlocks(text, compact)
}


// questionMark 是 Claude 输出的问题卡片标记格式
// 格式：<!--QUESTION:{"title":"...","options":["A","B"],"has_custom":true}-->
var questionMarkRe = regexp.MustCompile(`<!--QUESTION:(\{.*?\})-->`)

type questionData struct {
	Title     string   `json:"title"`
	Options   []string `json:"options"`
	HasCustom bool     `json:"has_custom"`
}

// extractQuestion 从文本中提取 QUESTION 标记，返回解析后的数据。
// 如果文本中没有标记，返回 nil。
func extractQuestion(text string) *questionData {
	m := questionMarkRe.FindStringSubmatch(text)
	if m == nil {
		return nil
	}
	var q questionData
	if err := json.Unmarshal([]byte(m[1]), &q); err != nil {
		log.Printf("[question] parse error: %v, raw: %s", err, m[1])
		return nil
	}
	return &q
}

// removeQuestionMark 从文本中移除 QUESTION 标记，返回干净的文本。
func removeQuestionMark(text string) string {
	return strings.TrimSpace(questionMarkRe.ReplaceAllString(text, ""))
}

// questionSystemHint 附加到系统提示，告知 Claude 何时输出问题标记。
const questionSystemHint = `当你需要向用户提问以获取选择时（例如让用户选择方案），在回复末尾附加以下格式的标记（不要有其他内容在标记之后）：
<!--QUESTION:{"title":"问题标题","options":["选项A","选项B","选项C"],"has_custom":true}-->
has_custom 为 true 时用户可以自定义输入回答，为 false 时只能从 options 中选择。
除非需要用户做选择，否则不要输出此标记。`

type Router struct {
	cfg          *config.Config
	feishuCli    *feishu.Client
	classifier   *intent.Classifier
	store        *session.Store
	adapter      *claude.Adapter
	pending      *feishu.PendingAction
	chainTracker *feishu.ReplyChainTracker
}

func New(cfg *config.Config, feishuCli *feishu.Client, classifier *intent.Classifier,
	store *session.Store, adapter *claude.Adapter, pending *feishu.PendingAction) *Router {
	return &Router{
		cfg:          cfg,
		feishuCli:    feishuCli,
		classifier:   classifier,
		store:        store,
		adapter:      adapter,
		pending:      pending,
		chainTracker: feishu.NewReplyChainTracker(1000),
	}
}

func (r *Router) HandleRouterMessage(ctx context.Context, msg feishu.IncomingMessage) {
	log.Printf("[router] received message: chat_type=%s, text=%s", msg.ChatType, msg.Text)

	if msg.Text == "" {
		log.Printf("[router] empty text, ignoring")
		return
	}

	// P2P 引用链深度检测：达到阈值时记录深度，在本条消息回复后再发卡片
	upgradeDepth := 0
	if msg.ChatType == "p2p" {
		upgradeDepth = r.checkChainDepth(ctx, msg)
	}

	activeSessions := r.store.ListActive()
	result, err := r.classifier.Classify(ctx, msg.Text, activeSessions)
	isDirect := false
	if err != nil {
		log.Printf("[router] classify error: %v", err)
		r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("⚠️ 意图分类失败: %v", err))
		return
	} else {
		log.Printf("[router] classified: intent=%s, topic=%s, action=%s", result.Intent, result.Topic, result.SystemAction)
		switch result.Intent {
		case intent.IntentDirect:
			isDirect = true
			r.handleDirect(ctx, msg)
		case intent.IntentSession:
			r.handleSession(ctx, msg, result)
		case intent.IntentSystem:
			r.handleSystem(ctx, msg, result)
		default:
			isDirect = true
			log.Printf("[router] unknown intent: %s, treating as direct", result.Intent)
			r.handleDirect(ctx, msg)
		}
	}

	// 升级群聊卡片仅在直接回复后发送；session/system 有自己的建群流程，不需要
	if upgradeDepth > 0 && isDirect {
		go r.sendChainUpgradeCard(ctx, msg, upgradeDepth)
	}
}

// checkChainDepth 检测 P2P 引用链深度。
// 返回达到阈值时的深度（> 0），否则返回 0。
// 注意：仅在深度恰好等于阈值时触发，避免重复发卡片。
func (r *Router) checkChainDepth(ctx context.Context, msg feishu.IncomingMessage) int {
	// 无引用：重置链
	if msg.ParentMessageID == "" {
		r.chainTracker.Track(msg.SenderID, msg.MessageID, "")
		return 0
	}

	// 用户已选择不升级，直接放行
	if r.chainTracker.IsDismissed(msg.SenderID) {
		return 0
	}

	depth := r.chainTracker.Track(msg.SenderID, msg.MessageID, msg.ParentMessageID)

	// depth==1 表示 parentMsgID 不在内存中，向上追溯历史
	if depth == 1 {
		ancestors := r.buildChainFromAPI(ctx, msg.ParentMessageID)
		if len(ancestors) > 0 {
			r.chainTracker.PrependChain(msg.SenderID, ancestors)
			depth = len(ancestors) + 1
		}
	}

	log.Printf("[chain] sender=%s depth=%d", msg.SenderID, depth)

	// 仅在首次达到阈值时触发（>= 避免因链包含 bot 回复 ID 导致深度跳过阈值）
	// 同时检查上一次 Track 前的深度未达到阈值，防止每条消息都弹卡片
	if depth >= r.cfg.ChainUpgradeThreshold {
		return depth
	}
	return 0
}

// sendChainUpgradeCard 发送升级群聊确认卡片，并处理用户选择。
// 在本条消息回复完成后由 goroutine 调用。
func (r *Router) sendChainUpgradeCard(ctx context.Context, msg feishu.IncomingMessage, depth int) {
	requestID := uuid.New().String()
	card := feishu.ChainUpgradeCard(depth, requestID)
	cardMsgID, err := r.feishuCli.SendCard(ctx, msg.ChatID, card)
	if err != nil {
		log.Printf("[chain] send upgrade card error: %v", err)
		return
	}

	ch := r.pending.Wait(requestID)
	select {
	case action := <-ch:
		switch action.Action {
		case "upgrade_group":
			// 立即禁用按钮，告知正在处理
			if err := r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.ChainUpgradeCardDone("upgrading", depth)); err != nil {
				log.Printf("[chain] update card to upgrading error: %v", err)
			}
			cwd := r.selectCwdForUpgrade(ctx, msg)
			r.handleChainUpgradeWithCwd(ctx, msg, cwd)
			// 升级完成后更新卡片状态
			if err := r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.ChainUpgradeCardDone("upgraded", depth)); err != nil {
				log.Printf("[chain] update card to upgraded error: %v", err)
			}
		case "dismiss_upgrade":
			r.chainTracker.Dismiss(msg.SenderID)
			// 卡片已在 OnCardAction 回调里同步禁用，无需再次 UpdateCard
		default:
			log.Printf("[chain] unknown action: %s", action.Action)
		}
	case <-time.After(10 * time.Minute):
		log.Printf("[chain] upgrade prompt timeout for sender %s", msg.SenderID)
		r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.ChainUpgradeCardDone("timeout", depth))
	}
}

// buildChainFromAPI 从给定消息 ID 向上追溯引用链，返回祖先消息 ID 列表（从旧到新）
// 最多追溯 10 层，防止无限循环
func (r *Router) buildChainFromAPI(ctx context.Context, startMsgID string) []string {
	const maxDepth = 10
	var chain []string
	currentID := startMsgID

	for i := 0; i < maxDepth; i++ {
		info, err := r.feishuCli.GetMessage(ctx, currentID)
		if err != nil {
			log.Printf("[chain] GetMessage %s error: %v", currentID, err)
			break
		}
		log.Printf("[chain] API traverse: id=%s sender_type=%s parent=%q text=%.30q", info.MessageID, info.SenderType, info.ParentID, info.Text)
		// 前置：旧消息在前
		chain = append([]string{info.MessageID}, chain...)
		if info.ParentID == "" {
			break
		}
		currentID = info.ParentID
	}
	return chain
}

// selectCwdForUpgrade 在 chain upgrade 前弹出工作目录选择卡片。
// 选择超时时返回默认目录。
func (r *Router) selectCwdForUpgrade(ctx context.Context, msg feishu.IncomingMessage) string {
	requestID := uuid.New().String()
	card := feishu.CwdSelectionCard(r.cfg.Repos, r.cfg.DefaultCwd, requestID)
	if _, err := r.feishuCli.SendCard(ctx, msg.ChatID, card); err != nil {
		log.Printf("[chain] send cwd card error: %v", err)
		return r.cfg.DefaultCwd
	}
	ch := r.pending.Wait(requestID)
	select {
	case action := <-ch:
		if cwd := extractCwd(action, r.cfg.DefaultCwd); cwd != "" {
			r.maybeAddRepo(cwd)
			return cwd
		}
	case <-time.After(2 * time.Minute):
		log.Printf("[chain] cwd selection timeout, using default")
	}
	return r.cfg.DefaultCwd
}

// handleChainUpgradeWithCwd 执行升级流程：建群、合并转发历史、注入 Claude 上下文
func (r *Router) handleChainUpgradeWithCwd(ctx context.Context, msg feishu.IncomingMessage, cwd string) {
	chainMsgIDs := r.chainTracker.GetChain(msg.SenderID)
	if len(chainMsgIDs) == 0 {
		log.Printf("[chain] upgrade: no chain for sender %s", msg.SenderID)
		return
	}

	// 1. 获取链上所有消息内容，构造历史上下文
	historyLines := r.fetchChainLines(ctx, msg.SenderID, "")

	// 2. 建群，群名取第一条用户消息的摘要
	groupName := fmt.Sprintf("[Claude] 私聊升级 %s", time.Now().Format("01-02 15:04"))
	for _, line := range historyLines {
		if strings.HasPrefix(line, "[用户]") {
			summary := strings.TrimPrefix(line, "[用户]: ")
			runes := []rune(summary)
			if len(runes) > 20 {
				runes = runes[:20]
				summary = string(runes) + "..."
			}
			groupName = fmt.Sprintf("[Claude] %s", summary)
			break
		}
	}

	chatID, err := r.feishuCli.CreateGroup(ctx, groupName)
	if err != nil {
		r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("建群失败: %v", err))
		return
	}

	// 3. 拉入用户并转移群主
	if err := r.feishuCli.AddMember(ctx, chatID, msg.SenderID); err != nil {
		log.Printf("[chain] add member error: %v", err)
	}
	if err := r.feishuCli.TransferOwner(ctx, chatID, msg.SenderID); err != nil {
		log.Printf("[chain] transfer owner error: %v", err)
	}

	// 4. 合并转发历史消息
	if err := r.feishuCli.MergeForwardMessages(ctx, chainMsgIDs, chatID); err != nil {
		log.Printf("[chain] merge forward error: %v", err)
		// 非致命，继续建立 session
	}

	// 5. 构造历史上下文注入消息
	var contextPrompt string
	if len(historyLines) > 0 {
		contextPrompt = "以下是我们之前在私聊中的对话历史，请了解背景后继续：\n\n" +
			strings.Join(historyLines, "\n") +
			"\n\n---\n请基于以上历史继续对话。"
	} else {
		contextPrompt = "这是一个从私聊升级的群聊会话，请继续之前的对话。"
	}

	// 6. 用历史上下文初始化 Claude session，获取 CLISessionID
	var cliSessionID string
	initResult, err := r.adapter.RunOnceWithSession(ctx, contextPrompt, cwd)
	if err != nil {
		log.Printf("[chain] history injection error: %v", err)
	} else {
		cliSessionID = initResult.SessionID
		log.Printf("[chain] history injected, sessionID=%s", cliSessionID)
	}

	// 7. 创建 Session
	sess := &session.Session{
		ID:           uuid.New().String(),
		ChatID:       chatID,
		Name:         groupName,
		WorkingDir:   cwd,
		CLISessionID: cliSessionID,
		Model:        r.cfg.SessionModel,
		Status:       session.StatusActive,
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
	r.store.Put(sess)
	r.store.Save()

	r.feishuCli.SendText(ctx, chatID, "✅ 历史对话已注入，Claude 已了解背景，请继续聊吧！")
	r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("已升级为群聊，请到新群继续对话 👆"))

	// 8. 清空链
	r.chainTracker.Reset(msg.SenderID)
}

// streamResponse 执行流式回复，支持问题卡片交互、中断按钮、多轮对话循环
// 参数：
//   chatID: 目标群 ID
//   initialPrompt: 初始 prompt
//   replyToMsgID: 回复目标消息 ID（为空则发送新消息到 chatID）
//   resumeSessionID: 复用的 CLI session ID（为空则创建新 session）
// 返回：最终的 CLI session ID（用于 Session 绑定）
func (r *Router) streamResponse(ctx context.Context, chatID, initialPrompt, replyToMsgID, resumeSessionID string) string {
	prompt := initialPrompt

	// 根据是否有 replyToMsgID 决定使用 ReplyCard 还是 SendCard
	initCard := feishu.StreamingCard("正在思考...", false, "")
	var cardMsgID string
	var err error
	if replyToMsgID != "" {
		// P2P 场景：回复原消息
		cardMsgID, err = r.feishuCli.ReplyCard(ctx, replyToMsgID, initCard)
	} else {
		// Session 场景：发送新消息
		cardMsgID, err = r.feishuCli.SendCard(ctx, chatID, initCard)
	}
	if err != nil {
		log.Printf("[router] streamResponse: send card error: %v", err)
		return ""
	}

	// 支持问题卡片交互的对话循环
	// 每轮：调用 Claude → 检测是否有问题标记 → 有则发卡片等待用户回答 → 继续循环
	startTime := time.Now()

dialogLoop:
	for {
		// 每轮独立的 abort 控制
		abortID := uuid.New().String()
		runCtx, runCancel := context.WithCancel(ctx)

		abortCh := r.pending.Wait(abortID)
		var userAborted atomic.Bool
		go func(id string, cancel context.CancelFunc) {
			select {
			case action := <-abortCh:
				if action.Action == "stop_stream" {
					userAborted.Store(true)
					cancel()
				}
			case <-runCtx.Done():
				r.pending.Resolve(id, feishu.ActionResult{Action: "cleanup"})
			}
		}(abortID, runCancel)

		// 更新卡片为带停止按钮的进行中状态
		elapsed := int(time.Since(startTime).Seconds())
		r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithAbort("正在思考...", "", elapsed, abortID))

		var (
			textBuf    strings.Builder
			tokenInfo  string
			lastUpdate time.Time
			// cardMu 序列化所有 UpdateCard 调用，防止流式更新和最终卡片乱序到达飞书
			cardMu      sync.Mutex
			cardFinished bool
		)

		r.adapter.Run(runCtx, claude.RunOptions{
			Prompt:             prompt,
			ResumeSessionID:    resumeSessionID,
			AppendSystemPrompt: questionSystemHint,
		}, func(event claude.Event) {
			switch event.Type {
			case "session_init":
				if resumeSessionID == "" {
					resumeSessionID = event.SessionID
				}
			case "text":
				textBuf.WriteString(event.Text)
				elapsed := int(time.Since(startTime).Seconds())
				if !userAborted.Load() && time.Since(lastUpdate) > streamThrottle {
					displayText := filterCodeBlocks(textBuf.String(), r.cfg.CompactStream)
					cardMu.Lock()
					if !cardFinished {
						r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithAbort(displayText, "", elapsed, abortID))
						lastUpdate = time.Now()
					}
					cardMu.Unlock()
				}
			case "result":
				if event.Usage != nil {
					tokenInfo = fmt.Sprintf("✅ Input: %d | Output: %d | Cost: $%.4f",
						event.Usage.InputTokens, event.Usage.OutputTokens, event.CostUSD)
				}
				if resumeSessionID == "" && event.SessionID != "" {
					resumeSessionID = event.SessionID
				}
			}
		})

		runCancel() // 确保 goroutine 退出

		elapsed = int(time.Since(startTime).Seconds())
		rawText := textBuf.String()

		// 用户中断：显示中断卡片，退出循环
		if userAborted.Load() {
			cleanText := removeQuestionMark(rawText)
			if cleanText == "" {
				cleanText = "（已中断）"
			}
			// 持锁发送最终卡片，确保它在所有流式更新之后到达飞书
			cardMu.Lock()
			cardFinished = true
			r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardAborted(cleanText, tokenInfo, elapsed))
			cardMu.Unlock()
			break dialogLoop
		}

		question := extractQuestion(rawText)
		cleanText := removeQuestionMark(rawText)
		if cleanText == "" {
			cleanText = "（无输出）"
		}

		// 持锁发送最终卡片
		cardMu.Lock()
		cardFinished = true
		if question == nil {
			// 普通回复，更新卡片并结束
			r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithElapsed(cleanText, true, tokenInfo, elapsed))
			cardMu.Unlock()
			break dialogLoop
		}
		cardMu.Unlock()

		// 有问题标记：更新卡片显示回复文本（已完成），再单独发问题选择卡片
		r.feishuCli.UpdateCard(ctx, cardMsgID, feishu.StreamingCardWithElapsed(cleanText, true, tokenInfo, elapsed))

		requestID := uuid.New().String()
		questionCard := feishu.QuestionCard(question.Title, question.Options, question.HasCustom, requestID)
		if _, err := r.feishuCli.SendCard(ctx, chatID, questionCard); err != nil {
			log.Printf("[router] send question card error: %v", err)
			break dialogLoop
		}
		log.Printf("[router] question card sent, waiting for answer: %s", question.Title)

		ch := r.pending.Wait(requestID)
		select {
		case action := <-ch:
			chosen := action.Value["chosen"]
			if chosen == "" {
				chosen = strings.TrimSpace(action.FormValue["custom_answer"])
			}
			if chosen == "" {
				break dialogLoop
			}
			log.Printf("[router] user answered: %s", chosen)
			prompt = chosen
		case <-time.After(10 * time.Minute):
			log.Printf("[router] question card timeout")
			break dialogLoop
		}

		// 为新一轮回复新建一个卡片
		newCard := feishu.StreamingCard("正在思考...", false, "")
		cardMsgID, _ = r.feishuCli.SendCard(ctx, chatID, newCard)
	}

	return resumeSessionID
}

func (r *Router) handleDirect(ctx context.Context, msg feishu.IncomingMessage) {
	log.Printf("[router] handleDirect: %s", msg.Text)

	prompt := msg.Text
	// P2P 引用场景：把链上历史消息拼入 prompt，让 Claude 知道上下文
	if msg.ChatType == "p2p" && msg.ParentMessageID != "" {
		if lines := r.fetchChainLines(ctx, msg.SenderID, msg.MessageID); len(lines) > 0 {
			prompt = "以下是之前的对话历史：\n\n" +
				strings.Join(lines, "\n") +
				"\n\n当前消息：" + msg.Text
		}
	}

	// 调用 streamResponse 处理流式回复
	r.streamResponse(ctx, msg.ChatID, prompt, msg.MessageID, "")
}

// fetchChainLines 获取链上所有历史消息（不含当前消息），格式化为 [角色]: 文本 的列表
func (r *Router) fetchChainLines(ctx context.Context, senderID, currentMsgID string) []string {
	chainMsgIDs := r.chainTracker.GetChain(senderID)
	var lines []string
	for _, msgID := range chainMsgIDs {
		if msgID == currentMsgID {
			continue // 当前消息已在 prompt 里，跳过
		}
		info, err := r.feishuCli.GetMessage(ctx, msgID)
		if err != nil || info.Text == "" {
			continue
		}
		role := "用户"
		if info.SenderType == "app" {
			role = "Claude"
		}
		lines = append(lines, fmt.Sprintf("[%s]: %s", role, info.Text))
	}
	return lines
}

func (r *Router) handleSession(ctx context.Context, msg feishu.IncomingMessage, result *intent.ClassifyResult) {
	topic := result.Topic
	if topic == "" {
		topic = "未识别主题"
	}
	reason := result.Reason
	if reason == "" {
		reason = "该任务预计需要多轮交互，建议建立独立会话以保持上下文。"
	}

	confirmID := uuid.New().String()
	log.Printf("[router] handleSession: sending confirm card, confirm_id=%s", confirmID)
	card := feishu.SessionConfirmCard(topic, reason, r.cfg.Repos, r.cfg.DefaultCwd, confirmID)
	cardMsgID, err := r.feishuCli.ReplyCard(ctx, msg.MessageID, card)
	if err != nil {
		log.Printf("[router] handleSession: ReplyCard error: %v", err)
		r.feishuCli.ReplyText(ctx, msg.MessageID, fmt.Sprintf("⚠️ 发送卡片失败: %v", err))
		return
	}

	ch := r.pending.Wait(confirmID)
	go func() {
		select {
		case action := <-ch:
			log.Printf("[router] handleSession: got action=%s for confirm_id=%s", action.Action, confirmID)
			switch action.Action {
			case "confirm_session_with_cwd":
				cwd := extractCwd(action, r.cfg.DefaultCwd)
				r.maybeAddRepo(cwd)
				r.createSession(ctx, msg, result, cwd, cardMsgID)
			case "deny_session":
				r.handleDirect(ctx, msg)
			default:
				log.Printf("[router] handleSession: unknown action=%s", action.Action)
			}
		case <-time.After(5 * time.Minute):
			log.Printf("[router] handleSession: confirm timeout for msg %s", msg.MessageID)
		}
	}()
}

// maybeAddRepo 如果 cwd 是手动输入的新路径（不在配置 Repos 里且目录存在），则自动加入配置。
func (r *Router) maybeAddRepo(cwd string) {
	if cwd == "" {
		return
	}
	info, err := os.Stat(cwd)
	if err != nil || !info.IsDir() {
		log.Printf("[router] maybeAddRepo: path not a valid dir, skip: %s", cwd)
		return
	}
	added, err := r.cfg.AddRepo(cwd)
	if err != nil {
		log.Printf("[router] maybeAddRepo: save config error: %v", err)
		return
	}
	if added {
		log.Printf("[router] maybeAddRepo: added %s to repos", cwd)
	}
}

// extractCwd 从卡片回调中提取工作目录：
// 优先读 Value["cwd"]（预设按钮），其次读 FormValue["custom_cwd"]（手动输入），最后返回默认值。
func extractCwd(action feishu.ActionResult, defaultCwd string) string {
	if cwd := action.Value["cwd"]; cwd != "" {
		return cwd
	}
	if cwd := strings.TrimSpace(action.FormValue["custom_cwd"]); cwd != "" {
		return cwd
	}
	return defaultCwd
}

func (r *Router) createSession(ctx context.Context, msg feishu.IncomingMessage, result *intent.ClassifyResult, cwd, confirmCardMsgID string) {
	name := result.Topic
	if name == "" {
		name = "新会话"
	}
	groupName := fmt.Sprintf("[Claude] %s", name)

	log.Printf("[session] createSession start: topic=%s cwd=%s", name, cwd)
	log.Printf("[session] calling CreateGroup: name=%s", groupName)
	chatID, err := r.feishuCli.CreateGroup(ctx, groupName)
	if err != nil {
		log.Printf("[session] CreateGroup error: %v", err)
		r.feishuCli.SendText(ctx, msg.ChatID, fmt.Sprintf("创建群失败: %v", err))
		return
	}
	log.Printf("[session] CreateGroup success: chat_id=%s", chatID)

	if err := r.feishuCli.AddMember(ctx, chatID, msg.SenderID); err != nil {
		log.Printf("add member error: %v", err)
	}
	log.Printf("[session] AddMember done")
	if err := r.feishuCli.TransferOwner(ctx, chatID, msg.SenderID); err != nil {
		log.Printf("[session] transfer owner error: %v", err)
	}
	log.Printf("[session] TransferOwner done")

	sess := &session.Session{
		ID:           uuid.New().String(),
		ChatID:       chatID,
		Name:         name,
		Tags:         result.Tags,
		WorkingDir:   cwd,
		Model:        r.cfg.SessionModel,
		Status:       session.StatusActive,
		CreatedAt:    time.Now(),
		LastActiveAt: time.Now(),
	}
	r.store.Put(sess)
	r.store.Save()

	r.feishuCli.SendText(ctx, chatID, fmt.Sprintf("会话已创建\n主题: %s\n工作目录: %s\n\n正在处理你的请求...", name, cwd))

	// 建群完成后更新确认卡片，显示群名（替代单独发送的通知消息）
	if confirmCardMsgID != "" {
		if err := r.feishuCli.UpdateCard(ctx, confirmCardMsgID, feishu.SessionConfirmCardDone(true, groupName)); err != nil {
			log.Printf("[session] update confirm card with group name error: %v", err)
		}
	}
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
		// 分类器误判为 system（如 create_session）时，降级为直接回复
		log.Printf("[router] unknown system_action=%s, falling back to direct", result.SystemAction)
		r.handleDirect(ctx, msg)
	}
}
