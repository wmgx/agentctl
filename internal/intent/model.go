package intent

type IntentType string

const (
	IntentDirect  IntentType = "direct"
	IntentSession IntentType = "session"
	IntentSystem  IntentType = "system"
)

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

type ClassifyResult struct {
	Intent           IntentType        `json:"intent"`
	Topic            string            `json:"topic,omitempty"`
	Tags             []string          `json:"tags,omitempty"`
	Reason           string            `json:"reason,omitempty"` // 分类理由，session 时说明为何需要建群
	SystemAction     SystemAction      `json:"system_action,omitempty"`
	Params           map[string]string `json:"params,omitempty"`
	CronScheduleHint string            `json:"cron_schedule_hint,omitempty"`
	CronPrompt       string            `json:"cron_prompt,omitempty"`
	CronName         string            `json:"cron_name,omitempty"`
}
