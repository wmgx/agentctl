package claude

import "encoding/json"

type MessageType string

const (
	MsgSystem    MessageType = "system"
	MsgAssistant MessageType = "assistant"
	MsgUser      MessageType = "user"
	MsgResult    MessageType = "result"
)

type StreamMessage struct {
	Type       MessageType     `json:"type"`
	SessionID  string          `json:"session_id,omitempty"`
	Message    json.RawMessage `json:"message,omitempty"`
	Subtype    string          `json:"subtype,omitempty"`
	Result     string          `json:"result,omitempty"`
	CostUSD    float64         `json:"total_cost_usd,omitempty"`
	DurationMs int64           `json:"duration_ms,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
}

type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	CacheRead    int `json:"cache_read_input_tokens,omitempty"`
	CacheCreate  int `json:"cache_creation_input_tokens,omitempty"`
}

type AssistantMessage struct {
	Role    string         `json:"role"`
	Content []ContentBlock `json:"content"`
}

type ContentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	ID    string          `json:"id,omitempty"`
	Name  string          `json:"name,omitempty"`
	Input json.RawMessage `json:"input,omitempty"`
}

type UserMessage struct {
	Role    string            `json:"role"`
	Content []ToolResultBlock `json:"content"`
}

type ToolResultBlock struct {
	Type      string `json:"type"`
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
