package models

type Role string

const (
	User      Role = "user"
	System    Role = "system"
	Assistant Role = "assistant"
	Tool      Role = "tool"
	Developer Role = "developer"
)

type Prompt struct {
	Role    Role   `json:"role"`
	Content string `json:"content"`
}

type StreamingOutput struct {
	Id      string `json:"id"`
	Content string `json:"content,omitempty"`
	Error   error  `json:"error"`
}

type AIModel struct {
	Model          string `json:"model"`
	ContextLength  int    `json:"context_length,omitempty"`
	FlashAttention bool   `json:"flash_attention,omitempty"`
	EchoLoadConfig bool   `json:"echo_load_config,omitempty"`
}
