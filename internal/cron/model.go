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
