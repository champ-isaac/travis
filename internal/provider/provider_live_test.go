package provider

// Build steps 1 and 2 end with a real call. These tests make it. They run only when LIVE_BASE_URL is
// set, so CI and a plain go test skip them:
//
//	LIVE_BASE_URL=http://127.0.0.1:8080 go test ./internal/provider -run Live -v -count=1
//
// They send two stateless requests with the exact model id from /v1/models, and change nothing on the server.

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/champ-isaac/travis/internal/openai"
	"github.com/champ-isaac/travis/models"
)

func liveProvider(t *testing.T) (*Local, string) {
	t.Helper()
	base := os.Getenv("LIVE_BASE_URL")
	if base == "" {
		t.Skip("LIVE_BASE_URL is not set, so no real server is called")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	page, err := openai.New(openai.WithBaseURL(base)).ListModels(ctx)
	if err != nil || len(page.Data) == 0 {
		t.Fatalf("ListModels on %s: %v (%d models)", base, err, len(page.Data))
	}
	l, err := NewLocal(base, "")
	if err != nil {
		t.Fatal(err)
	}
	return l, page.Data[0].ID // the exact id: any other name makes the server drop its weights
}

func TestLive_PlainReplyHasNoControlTokens(t *testing.T) {
	l, model := liveProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := l.Complete(ctx, Request{
		Model:    model,
		Messages: []models.Prompt{{Role: models.User, Content: "Say hello in one sentence."}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("text %q, thinking %d chars, finish %s, usage %+v", resp.Text, len(resp.Thinking), resp.FinishReason, resp.Usage)
	if strings.TrimSpace(resp.Text) == "" || strings.Contains(resp.Text+resp.Thinking, "<|") {
		t.Errorf("text = %q, thinking has <| : %v; want clean non-empty text", resp.Text, strings.Contains(resp.Thinking, "<|"))
	}
	if resp.FinishReason != "stop" || resp.Usage == nil || resp.Usage.TotalTokens == 0 {
		t.Errorf("finish %q, usage %+v, want stop and a token count", resp.FinishReason, resp.Usage)
	}
}

func TestLive_CalculatorToolCall(t *testing.T) {
	l, model := liveProvider(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := l.Complete(ctx, Request{
		Model:    model,
		Messages: []models.Prompt{{Role: models.User, Content: "What is 1234 x 5678? Use the calculator tool."}},
		Tools:    []string{"basic_calculator"},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("text %q, tool calls %+v", resp.Text, resp.ToolCalls)
	if len(resp.ToolCalls) == 0 {
		t.Fatalf("no tool call in the reply: text %q", resp.Text)
	}
	call := resp.ToolCalls[0]
	var args struct {
		Operator     string  `json:"operator"`
		FirstNumber  float64 `json:"first_number"`
		SecondNumber float64 `json:"second_number"`
	}
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		t.Fatalf("arguments %q: %v", call.Arguments, err)
	}
	if call.ID == "" || call.Name != "calculator" || args.Operator != "multiply" || args.FirstNumber != 1234 || args.SecondNumber != 5678 {
		t.Errorf("call = %+v, want a calculator call with an ID and multiply 1234 5678", call)
	}
	if strings.Contains(resp.Text, "<|") {
		t.Errorf("text %q still has control tokens", resp.Text)
	}
}
