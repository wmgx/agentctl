package cron

import (
	"context"
	"log"
	"strings"

	"github.com/wmgx/agentctl/internal/claude"
	"github.com/wmgx/agentctl/internal/config"
	"github.com/wmgx/agentctl/internal/feishu"

	cronlib "github.com/robfig/cron/v3"
)

type Scheduler struct {
	cron      *cronlib.Cron
	store     *Store
	cfg       *config.Config
	adapter   *claude.Adapter
	feishuCli *feishu.Client
	entryMap  map[string]cronlib.EntryID
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
