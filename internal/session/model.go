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
	Model        string    `json:"model,omitempty"` // Claude 模型，空则使用 CLI 默认
	Status       Status    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	LastActiveAt time.Time `json:"last_active_at"`
}
