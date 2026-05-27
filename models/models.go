package models

type Prompt struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type StreamingOutput struct {
	Content string `json:"content,omitempty"`
	Error   error  `json:"error"`
}

type AIModel struct {
	Model          string `json:"model"`
	ContextLength  int    `json:"context_length,omitempty"`
	FlashAttention bool   `json:"flash_attention,omitempty"`
	EchoLoadConfig bool   `json:"echo_load_config,omitempty"`
}
