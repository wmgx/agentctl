package cron

import (
	"context"
	"log"
	"strings"

	"github.com/wmgx/agentctl/internal/claude"
	"github.com/wmgx/agentctl/internal/config"
	"github.com/wmgx/agentctl/internal/feishu"
	"github.com/wmgx/agentctl/internal/logclean"

	cronlib "github.com/robfig/cron/v3"
)

type Scheduler struct {
	cron      *cronlib.Cron
	store     *Store
	cfg       *config.Config
	adapter   *claude.Adapter
	feishuCli *feishu.Client
	entryMap  map[string]cronlib.EntryID
	logDir    string
}

func NewScheduler(store *Store, cfg *config.Config, adapter *claude.Adapter, feishuCli *feishu.Client, logDir string) *Scheduler {
	return &Scheduler{
		cron:      cronlib.New(),
		store:     store,
		cfg:       cfg,
		adapter:   adapter,
		feishuCli: feishuCli,
		entryMap:  make(map[string]cronlib.EntryID),
		logDir:    logDir,
	}
}

func (s *Scheduler) Start() {
	for _, job := range s.store.ListEnabled() {
		s.addJob(job)
	}
	// 内置日志清理：每天凌晨 3 点执行
	s.cron.AddFunc("0 3 * * *", func() {
		logclean.Run(s.logDir, s.cfg.LogRetentionDays)
	})
	// 启动时立即执行一次，清理历史遗留的过期日志
	go logclean.Run(s.logDir, s.cfg.LogRetentionDays)

	s.cron.Start()
	log.Printf("Cron scheduler started with %d jobs (log retention: %dd)", len(s.entryMap), s.cfg.LogRetentionDays)
}

func (s *Scheduler) Stop() {
	s.cron.Stop()
}

func (s *Scheduler) Reload() {
	for id, entryID := range s.entryMap {
		s.cron.Remove(entryID)
		delete(s.entryMap, id)
	}
	for _, job := range s.store.ListEnabled() {
		s.addJob(job)
	}
}

func (s *Scheduler) addJob(job *CronJob) {
	jobCopy := *job
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

	if targetChat == "" {
		log.Printf("Cron job %s finished but no target_chat configured, result discarded", job.Name)
		return
	}

	text := "📅 **" + job.Name + "**\n\n" + result
	if _, err := s.feishuCli.SendText(ctx, targetChat, text); err != nil {
		log.Printf("Failed to send cron result: %v", err)
	}
}
