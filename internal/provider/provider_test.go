package provider

// These tests run Local against a scripted server (net/http/httptest). They need no .env and no model
// server. The replies come from the fixtures captured from mlx_lm.server (inkling/testdata), so the
// decoder is tested on what the server really sends.

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/champ-isaac/travis/internal/openai"
	"github.com/champ-isaac/travis/models"
)

var _ Provider = (*Local)(nil)

type seenRequest struct {
	method, path, auth string
	body               map[string]any
}

// serve starts a server that answers every request with one status and body, and returns a Local
// for it and a function that lists the requests seen. Retries are off, so a 500 fails at once.
func serve(t *testing.T, status int, body string) (*Local, func() []seenRequest) {
	t.Helper()
	var mu sync.Mutex
	var got []seenRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var b map[string]any
		_ = json.Unmarshal(raw, &b)
		mu.Lock()
		got = append(got, seenRequest{r.Method, r.URL.Path, r.Header.Get("Authorization"), b})
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	l, err := NewLocal(srv.URL, "", openai.WithMaxRetries(0))
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return l, func() []seenRequest {
		mu.Lock()
		defer mu.Unlock()
		return append([]seenRequest(nil), got...)
	}
}

// reply builds a non-streamed chat completion. message is the JSON of the assistant message.
func reply(message, finish, usage string) string {
	if usage != "" {
		usage = `,"usage":` + usage
	}
	return `{"id":"chatcmpl-1","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"` +
		finish + `","message":` + message + `}]` + usage + `}`
}

func assistant(t *testing.T, content string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"role": "assistant", "content": content})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const usageJSON = `{"prompt_tokens":11,"completion_tokens":22,"total_tokens":33,"completion_tokens_details":{"reasoning_tokens":5}}`

// fixture returns the exact content of a reply captured from the local server.
func fixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("inkling", "testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if !strings.Contains(fx.Content, "<|") {
		t.Fatalf("%s: fixture has no control tokens", name)
	}
	return fx.Content
}

func ask(model, text string) Request {
	return Request{Model: model, Messages: []models.Prompt{{Role: models.User, Content: text}}}
}

func TestNewLocal_ReturnsAnErrorForABadBaseURL(t *testing.T) {
	for _, base := range []string{"", "localhost:8080", "ftp://localhost:8080", "http://", "://x", "  "} {
		l, err := NewLocal(base, "")
		if err == nil {
			t.Errorf("NewLocal(%q) returned no error", base)
		}
		if l != nil {
			t.Errorf("NewLocal(%q) returned a provider together with an error", base)
		}
	}
}

func TestNewLocal_AcceptsAnEmptyKeyAndSendsNoAuthorization(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-env") // must not leak to the local server
	l, seen := serve(t, 200, reply(assistant(t, "hi"), "stop", usageJSON))
	if _, err := l.Complete(context.Background(), ask("m", "x")); err != nil {
		t.Fatal(err)
	}
	if got := seen()[0].auth; got != "" {
		t.Errorf("Authorization = %q with an empty key, want none", got)
	}
}

func TestNewLocal_SendsAKeyWhenGiven(t *testing.T) {
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply(assistant(t, "hi"), "stop", usageJSON))
	}))
	defer srv.Close()
	l, err := NewLocal(srv.URL, "k", openai.WithMaxRetries(0))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Complete(context.Background(), ask("m", "x")); err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(auth, "Bearer k") {
		t.Errorf("Authorization = %q, want Bearer k", auth)
	}
}

func TestComplete_SendsTheRequest(t *testing.T) {
	l, seen := serve(t, 200, reply(assistant(t, "pong"), "stop", usageJSON))
	req := Request{
		Model: "model-1",
		Messages: []models.Prompt{
			{Role: models.System, Content: "be brief"},
			{Role: models.User, Content: "hello"},
		},
		Tools: []string{"basic_calculator"},
	}
	if _, err := l.Complete(context.Background(), req); err != nil {
		t.Fatal(err)
	}
	reqs := seen()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.method != http.MethodPost || r.path != "/v1/chat/completions" {
		t.Errorf("request = %s %s, want POST /v1/chat/completions", r.method, r.path)
	}
	if r.body["model"] != "model-1" {
		t.Errorf("model = %v, want model-1", r.body["model"])
	}
	if stream, _ := r.body["stream"].(bool); stream {
		t.Error("stream = true, want a non-streamed request")
	}
	msgs, _ := r.body["messages"].([]any)
	if len(msgs) != 2 ||
		msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != "be brief" ||
		msgs[1].(map[string]any)["role"] != "user" || msgs[1].(map[string]any)["content"] != "hello" {
		t.Errorf("messages = %v, want system then user in order", msgs)
	}
	tools, _ := r.body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want the one calculator schema", r.body["tools"])
	}
	if name := tools[0].(map[string]any)["function"].(map[string]any)["name"]; name != "calculator" {
		t.Errorf("tool name = %v, want calculator", name)
	}
}

func TestComplete_SendsNoToolsWhenNoneAreAsked(t *testing.T) {
	l, seen := serve(t, 200, reply(assistant(t, "hi"), "stop", usageJSON))
	if _, err := l.Complete(context.Background(), ask("m", "x")); err != nil {
		t.Fatal(err)
	}
	if tools, _ := seen()[0].body["tools"].([]any); len(tools) != 0 {
		t.Errorf("tools = %v, want none", tools)
	}
}

func TestComplete_SendsTheTemperatureOfTheRequest(t *testing.T) {
	for _, temp := range []float64{0, 0.7, 1.5} {
		l, seen := serve(t, 200, reply(assistant(t, "hi"), "stop", usageJSON))
		req := ask("m", "x")
		req.Temperature = temp
		if _, err := l.Complete(context.Background(), req); err != nil {
			t.Fatal(err)
		}
		got, present := seen()[0].body["temperature"]
		if !present || got != temp {
			t.Errorf("Temperature %v: request temperature = %v (present %v), want %v sent explicitly", temp, got, present, temp)
		}
	}
}

func TestComplete_DecodesTheCapturedReplies(t *testing.T) {
	cases := []struct {
		fixture   string
		wantCalls int
		wantText  bool
	}{
		{"plain", 0, true},
		{"final_answer", 0, true},
		{"single_tool_call", 1, false},
		{"multiple_tool_calls", 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.fixture, func(t *testing.T) {
			l, _ := serve(t, 200, reply(assistant(t, fixture(t, tc.fixture)), "stop", usageJSON))
			resp, err := l.Complete(context.Background(), ask("m", "x"))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(resp.Text, "<|") || strings.Contains(resp.Thinking, "<|") {
				t.Errorf("control tokens left: text %q, thinking %q", resp.Text, resp.Thinking)
			}
			if resp.Thinking == "" {
				t.Error("Thinking is empty, want the model's thinking split out")
			}
			if (resp.Text != "") != tc.wantText {
				t.Errorf("Text = %q, want non-empty: %v", resp.Text, tc.wantText)
			}
			if len(resp.ToolCalls) != tc.wantCalls {
				t.Fatalf("tool calls = %d, want %d", len(resp.ToolCalls), tc.wantCalls)
			}
			seen := map[string]bool{}
			for _, call := range resp.ToolCalls {
				if !strings.HasPrefix(call.ID, "call_") || seen[call.ID] {
					t.Errorf("tool call ID %q is empty, malformed or repeated", call.ID)
				}
				seen[call.ID] = true
			}
		})
	}
}

func TestComplete_ReturnsTheCalculatorCallWithItsArguments(t *testing.T) {
	l, _ := serve(t, 200, reply(assistant(t, fixture(t, "single_tool_call")), "stop", usageJSON))
	resp, err := l.Complete(context.Background(), ask("m", "What is 1234 x 5678?"))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "calculator" {
		t.Fatalf("tool calls = %+v, want one calculator call", resp.ToolCalls)
	}
	var args struct {
		Operator     string  `json:"operator"`
		FirstNumber  float64 `json:"first_number"`
		SecondNumber float64 `json:"second_number"`
	}
	if err := json.Unmarshal([]byte(resp.ToolCalls[0].Arguments), &args); err != nil {
		t.Fatalf("arguments %q: %v", resp.ToolCalls[0].Arguments, err)
	}
	if args.Operator != "multiply" || args.FirstNumber != 1234 || args.SecondNumber != 5678 {
		t.Errorf("arguments = %+v, want multiply 1234 5678", args)
	}
}

func TestComplete_CarriesTheIDFinishReasonAndUsage(t *testing.T) {
	l, _ := serve(t, 200, reply(assistant(t, "<|message_model|><|content_text|>ok<|end_message|>"), "stop", usageJSON))
	resp, err := l.Complete(context.Background(), ask("m", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.ID != "chatcmpl-1" || resp.FinishReason != "stop" || resp.Text != "ok" {
		t.Errorf("response = %+v, want id chatcmpl-1, finish stop, text ok", resp)
	}
	want := models.Usage{PromptTokens: 11, CompletionTokens: 22, TotalTokens: 33, ReasoningTokens: 5}
	if resp.Usage == nil || *resp.Usage != want {
		t.Errorf("usage = %+v, want %+v", resp.Usage, want)
	}
}

func TestComplete_UsageIsNilWhenTheServerSendsNone(t *testing.T) {
	l, _ := serve(t, 200, reply(assistant(t, "plain words"), "stop", ""))
	resp, err := l.Complete(context.Background(), ask("m", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Usage != nil {
		t.Errorf("usage = %+v, want nil: zero tokens would look like a real count", resp.Usage)
	}
	if resp.Text != "plain words" {
		t.Errorf("text = %q, want content with no control tokens kept as plain text", resp.Text)
	}
}

func TestComplete_AReplyCutOffByTheTokenLimitIsDecodedAsFarAsItGoes(t *testing.T) {
	l, _ := serve(t, 200, reply(assistant(t, "<|message_model|><|content_text|>half an ans"), "length", usageJSON))
	resp, err := l.Complete(context.Background(), ask("m", "x"))
	if err != nil {
		t.Fatalf("unexpected error %v", err)
	}
	if resp.Text != "half an ans" || resp.FinishReason != "length" {
		t.Errorf("response = %+v, want the partial text and finish_reason length so the caller can see the cut", resp)
	}
}

func TestComplete_ADecodeErrorKeepsTheUsageAndWhatWasDecoded(t *testing.T) {
	content := "<|message_model|><|content_text|>partial<|end_message|><|message_model|>calculator<|content_invoke_tool_json|>{broken"
	l, _ := serve(t, 200, reply(assistant(t, content), "stop", usageJSON))
	resp, err := l.Complete(context.Background(), ask("m", "x"))
	if err == nil {
		t.Fatal("no error for a tool call with broken JSON")
	}
	if resp.Text != "partial" || resp.ID != "chatcmpl-1" || resp.Usage == nil || resp.Usage.TotalTokens != 33 {
		t.Errorf("response = %+v, want the text before the failure, the id and the usage the call cost", resp)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("tool calls = %+v, want none: the failing call is not in the response", resp.ToolCalls)
	}
}

func TestComplete_KeepsToolCallsTheServerSendsItself(t *testing.T) {
	msg := `{"role":"assistant","content":"","tool_calls":[{"id":"srv-1","type":"function","function":{"name":"calculator","arguments":"{\"operator\":\"add\",\"first_number\":1,\"second_number\":2}"}}]}`
	l, _ := serve(t, 200, reply(msg, "tool_calls", usageJSON))
	resp, err := l.Complete(context.Background(), ask("m", "x"))
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "srv-1" || resp.ToolCalls[0].Name != "calculator" ||
		!strings.Contains(resp.ToolCalls[0].Arguments, `"add"`) {
		t.Errorf("tool calls = %+v, want the server's own call with its id", resp.ToolCalls)
	}
}

func TestComplete_RejectsABadRequestWithoutCallingTheServer(t *testing.T) {
	cases := map[string]struct {
		req     Request
		wantErr string // the error must name the problem
	}{
		"no model":    {Request{Messages: []models.Prompt{{Role: models.User, Content: "x"}}}, "model"},
		"no messages": {Request{Model: "m"}, "message"},
		"bad role":    {Request{Model: "m", Messages: []models.Prompt{{Role: "function", Content: "x"}}}, "function"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			l, seen := serve(t, 200, reply(assistant(t, "hi"), "stop", usageJSON))
			if _, err := l.Complete(context.Background(), tc.req); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want one that mentions %q", err, tc.wantErr)
			}
			if n := len(seen()); n != 0 {
				t.Errorf("server saw %d requests, want 0", n)
			}
		})
	}
}

func TestComplete_ReturnsAnErrorWhenTheServerFails(t *testing.T) {
	cases := map[string]struct {
		status  int
		body    string
		wantErr string // the error must carry the cause
	}{
		"http 500":   {500, `{"error":{"message":"boom"}}`, "boom"},
		"bad json":   {200, `{"choices":[`, "parsing response"},
		"no choices": {200, `{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[]}`, "no choices"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			l, _ := serve(t, tc.status, tc.body)
			if _, err := l.Complete(context.Background(), ask("m", "x")); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want one that mentions %q", err, tc.wantErr)
			}
		})
	}
}

func TestComplete_StopsWhenTheContextIsCancelled(t *testing.T) {
	l, _ := serve(t, 200, reply(assistant(t, "hi"), "stop", usageJSON))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := l.Complete(ctx, ask("m", "x")); !errors.Is(err, context.Canceled) {
		t.Errorf("error = %v, want one that wraps context.Canceled", err)
	}
}

func TestComplete_ACutOffToolCallIsAnErrorEvenWhenTheFinishReasonIsLength(t *testing.T) {
	content := "<|message_model|><|content_text|>I will calculate.<|end_message|><|message_model|>calculator<|content_invoke_tool_json|>" +
		`{"name":"calculator","args":{"operator":"multi`
	l, _ := serve(t, 200, reply(assistant(t, content), "length", usageJSON))
	resp, err := l.Complete(context.Background(), ask("m", "x"))
	if err == nil || !strings.Contains(err.Error(), "tool call") {
		t.Fatalf("error = %v, want one about the tool call: a call cut off in its JSON cannot be run", err)
	}
	if resp.FinishReason != "length" || resp.Text != "I will calculate." || resp.Usage == nil || len(resp.ToolCalls) != 0 {
		t.Errorf("response = %+v, want finish length, the text before the cut, the usage and no tool call", resp)
	}
}

func TestComplete_ValidatesToolCallsTheServerSendsItself(t *testing.T) {
	fn := func(name, args string) string {
		a, _ := json.Marshal(args)
		return `{"id":"srv-1","type":"function","function":{"name":"` + name + `","arguments":` + string(a) + `}}`
	}
	cases := []struct {
		name     string
		call     string
		wantErr  string // empty: the call is accepted
		wantArgs string
	}{
		{"object arguments", fn("calculator", `{"operator":"add"}`), "", `{"operator":"add"}`},
		{"empty arguments become an empty object", fn("calculator", ``), "", `{}`},
		{"null arguments become an empty object", fn("calculator", `null`), "", `{}`},
		{"custom tool call", `{"id":"srv-1","type":"custom","custom":{"name":"calculator","input":"1+2"}}`, "custom", ""},
		{"arguments are an array", fn("calculator", `[1,2]`), "JSON object", ""},
		{"arguments are a string", fn("calculator", `"1+2"`), "JSON object", ""},
		{"arguments are broken JSON", fn("calculator", `{"operator":`), "JSON object", ""},
		{"no name", fn("", `{}`), "no name", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			msg := `{"role":"assistant","content":"","tool_calls":[` + tc.call + `]}`
			l, _ := serve(t, 200, reply(msg, "tool_calls", usageJSON))
			resp, err := l.Complete(context.Background(), ask("m", "x"))
			if tc.wantErr == "" {
				if err != nil || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Arguments != tc.wantArgs {
					t.Errorf("err %v, tool calls %+v, want one call with arguments %s", err, resp.ToolCalls, tc.wantArgs)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want one that mentions %q", err, tc.wantErr)
			}
			if len(resp.ToolCalls) != 0 || resp.ID != "chatcmpl-1" || resp.Usage == nil {
				t.Errorf("response = %+v, want the bad call left out and the id and usage kept", resp)
			}
		})
	}
}

func TestNewLocal_TrimsWhitespaceAndTrailingSlashesFromTheBaseURL(t *testing.T) {
	var mu sync.Mutex
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply(assistant(t, "hi"), "stop", usageJSON))
	}))
	defer srv.Close()

	bases := []string{srv.URL, srv.URL + "/", srv.URL + "///", " " + srv.URL + " ", "\t" + srv.URL + "/\n"}
	for _, base := range bases {
		l, err := NewLocal(base, "", openai.WithMaxRetries(0))
		if err != nil {
			t.Fatalf("NewLocal(%q): %v", base, err)
		}
		if _, err := l.Complete(context.Background(), ask("m", "x")); err != nil {
			t.Fatalf("Complete with base %q: %v", base, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) != len(bases) {
		t.Fatalf("server saw %d requests, want %d", len(paths), len(bases))
	}
	for i, path := range paths {
		if path != "/v1/chat/completions" {
			t.Errorf("base %q: request path = %q, want /v1/chat/completions", bases[i], path)
		}
	}
}

func TestNewLocal_RejectsCredentialsInTheBaseURLWithoutPrintingThem(t *testing.T) {
	const secret = "s3cret-pw" //pragma: allowlist secret
	cases := []struct {
		base      string
		wantInErr string // empty: any error will do, as long as it keeps the secret out
	}{
		{"http://user:" + secret + "@localhost:8080", "credentials"},
		{"http://" + secret + "@localhost:8080", "credentials"},
		{"https://user:" + secret + "@localhost:8080/", "credentials"},
		{"http://user:" + secret + "@localhost:notaport", ""}, // fails to parse: that error must not echo the URL either
	}
	for _, tc := range cases {
		l, err := NewLocal(tc.base, "")
		if err == nil || l != nil {
			t.Errorf("NewLocal(%q) = %v, %v, want an error and no provider", tc.base, l, err)
			continue
		}
		if strings.Contains(err.Error(), secret) {
			t.Errorf("NewLocal(%q) error shows the password: %v", tc.base, err)
		}
		if tc.wantInErr != "" && !strings.Contains(err.Error(), tc.wantInErr) {
			t.Errorf("NewLocal(%q) error = %v, want one that mentions %q", tc.base, err, tc.wantInErr)
		}
	}
}

func TestNewLocal_DoesNotRetryByDefault(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After-Ms", "1") // lets a client that does retry finish fast
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	defer srv.Close()

	l, err := NewLocal(srv.URL, "") // no options: the default retry count
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Complete(context.Background(), ask("m", "x")); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want the server's", err)
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("server saw %d requests, want 1: a failed generation must not be run again by a hidden retry", n)
	}
}

func TestNewLocal_RejectsAQueryFragmentOrPathWithoutPrintingTheURL(t *testing.T) {
	cases := []struct {
		base      string
		wantInErr string
	}{
		{"http://localhost:8080?token=SECRETQ", "query"},
		{"http://localhost:8080/?token=SECRETQ", "query"},
		{"http://localhost:8080?", "query"}, // an empty query still breaks the /v1 that is added after it
		{"http://localhost:8080#SECRETF", "fragment"},
		{"http://localhost:8080#", "fragment"},  // an empty fragment still swallows the /v1 that is added after it
		{"http://localhost:8080/#", "fragment"}, // the same with a slash before it: the final # keeps the slash from being trimmed
		{"http://localhost:8080/v1", "path"},
		{"http://localhost:8080/v1/", "path"}, // the trailing slash is trimmed first, the path stays
		{"http://localhost:8080/SECRETPATH/api", "path"},
		{"ftp://localhost:8080?token=SECRETQ", "http://host:port"}, // the scheme error must not echo the query either
	}
	for _, tc := range cases {
		l, err := NewLocal(tc.base, "")
		if err == nil || l != nil {
			t.Errorf("NewLocal(%q) = %v, %v, want an error and no provider", tc.base, l, err)
			continue
		}
		if !strings.Contains(err.Error(), tc.wantInErr) {
			t.Errorf("NewLocal(%q) error = %v, want one that mentions %q", tc.base, err, tc.wantInErr)
		}
		if strings.Contains(err.Error(), "SECRET") || strings.Contains(err.Error(), "localhost") {
			t.Errorf("NewLocal(%q) error repeats part of the URL: %v", tc.base, err)
		}
	}
}

// recorder starts a server that counts the requests it gets and remembers the last Authorization header.
func recorder(t *testing.T) (srv *httptest.Server, hits func() int, auth func() string) {
	t.Helper()
	var mu sync.Mutex
	var n int
	var last string
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		n++
		last = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, reply(assistant(t, "hi"), "stop", usageJSON))
	}))
	t.Cleanup(srv.Close)
	return srv,
		func() int { mu.Lock(); defer mu.Unlock(); return n },
		func() string { mu.Lock(); defer mu.Unlock(); return last }
}

func TestNewLocal_TheCheckedBaseURLAndKeyWinOverOptions(t *testing.T) {
	for _, key := range []string{"real-key", ""} {
		t.Run("key "+key, func(t *testing.T) {
			checked, checkedHits, checkedAuth := recorder(t)
			decoy, decoyHits, _ := recorder(t)
			// The options name the decoy server and another key. They are applied before the checked
			// values, so the request must still go to the checked server, with the checked key.
			l, err := NewLocal(checked.URL, key,
				openai.WithBaseURL(decoy.URL), openai.WithApiKey("key-from-an-option"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.Complete(context.Background(), ask("m", "x")); err != nil {
				t.Fatalf("Complete: %v", err)
			}
			if n := decoyHits(); n != 0 {
				t.Errorf("the decoy server named by an option got %d requests, want 0", n)
			}
			if n := checkedHits(); n != 1 {
				t.Errorf("the checked server got %d requests, want 1", n)
			}
			want := ""
			if key != "" {
				want = "Bearer " + key
			}
			if got := checkedAuth(); !strings.EqualFold(got, want) {
				t.Errorf("Authorization = %q, want %q", got, want)
			}
		})
	}
}

func TestNewLocal_AnOptionOverridesTheDefaultRetryCount(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After-Ms", "1") // keeps the retry fast
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":{"message":"boom"}}`)
	}))
	defer srv.Close()

	l, err := NewLocal(srv.URL, "", openai.WithMaxRetries(1))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.Complete(context.Background(), ask("m", "x")); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error = %v, want the server's", err)
	}
	if n := calls.Load(); n != 2 {
		t.Errorf("server saw %d requests, want 2: the first one and the one retry the option asked for", n)
	}
}

func TestNewLocal_ARefusedConnectionFailsAtOnceByDefault(t *testing.T) {
	// Take a free port and close it again, so nothing listens there.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	base := "http://" + ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}

	l, err := NewLocal(base, "") // building a provider makes no connection, so this succeeds
	if err != nil {
		t.Fatalf("NewLocal: %v, want no error: it does not dial", err)
	}
	start := time.Now()
	_, err = l.Complete(context.Background(), ask("m", "x"))
	took := time.Since(start)
	if !errors.Is(err, syscall.ECONNREFUSED) {
		t.Fatalf("Complete error = %v, want a refused connection", err)
	}
	// The SDK's default of 5 retries waits about 12 to 15 s on a refused connection. With none it is instant.
	if took > 2*time.Second {
		t.Errorf("Complete took %v to fail, want an immediate failure: a refused connection must not be retried by default", took)
	}
}
