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

// Usage is the token count the server reports for one request.
type Usage struct {
	PromptTokens     int64 `json:"prompt_tokens"`
	CompletionTokens int64 `json:"completion_tokens"`
	TotalTokens      int64 `json:"total_tokens"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
}

// StreamingOutput is one item of a streamed reply: a piece of Content, then a last item
// that carries the response Id and the Usage, or an Error. Usage is nil when the server sent none.
type StreamingOutput struct {
	Id      string `json:"id"`
	Content string `json:"content,omitempty"`
	Usage   *Usage `json:"usage,omitempty"`
	Error   error  `json:"error"`
}

type AIModel struct {
	Model          string `json:"model"`
	ContextLength  int    `json:"context_length,omitempty"`
	FlashAttention bool   `json:"flash_attention,omitempty"`
	EchoLoadConfig bool   `json:"echo_load_config,omitempty"`
}
