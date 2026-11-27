package openai

// These tests run the real Client against a scripted local server (net/http/httptest). They need no
// .env and no model server. The server answers the nth request with the nth scripted answer and
// records every request, so a test can check both what the code returns and what it sent.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/champ-isaac/travis/models"
)

type answer struct {
	status int
	body   string
}

type request struct {
	method, path string
	body         map[string]any
}

func (r request) messages() []map[string]any {
	var out []map[string]any
	list, _ := r.body["messages"].([]any)
	for _, m := range list {
		mm, _ := m.(map[string]any)
		out = append(out, mm)
	}
	return out
}

// scripted starts the server and returns a client for it and a function that lists the requests seen.
// A request beyond the script fails the test and gets a 500.
func scripted(t *testing.T, answers ...answer) (*Client, func() []request) {
	t.Helper()
	var mu sync.Mutex
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(raw, &body)
		mu.Lock()
		i := len(got)
		got = append(got, request{method: r.Method, path: r.URL.Path, body: body})
		mu.Unlock()
		if i >= len(answers) {
			t.Errorf("unexpected request %d to %s: the script has %d answers", i+1, r.URL.Path, len(answers))
			http.Error(w, "script exhausted", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(answers[i].status)
		_, _ = io.WriteString(w, answers[i].body)
	}))
	t.Cleanup(srv.Close)
	c := New(WithBaseURL(srv.URL), WithVersion("v1"), WithApiKey("k"), WithMaxRetries(0))
	return c, func() []request {
		mu.Lock()
		defer mu.Unlock()
		return append([]request(nil), got...)
	}
}

func okBody(content, extra string) string {
	return fmt.Sprintf(`{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":%q%s}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, content, extra)
}

func ok(content string) answer { return answer{200, okBody(content, "")} }

func withToolCalls(calls ...string) string { return `,"tool_calls":[` + strings.Join(calls, ",") + `]` }

func toolCallJSON(id, name, args string) string {
	q, _ := json.Marshal(args)
	return fmt.Sprintf(`{"id":%q,"type":"function","function":{"name":%q,"arguments":%s}}`, id, name, q)
}

func calcCall(id, args string) string { return toolCallJSON(id, "calculator", args) }

const (
	mulArgs    = `{"operator":"multiply","first_number":2,"second_number":3}`
	divZero    = `{"operator":"divide","first_number":1,"second_number":0}`
	badOpArgs  = `{"operator":"power","first_number":2,"second_number":3}`
	notJSON    = `not json`
	emptyReply = `{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":1,"completion_tokens":0,"total_tokens":1}}`
)

var (
	http500   = answer{500, `{"error":{"message":"boom"}}`}
	badJSON   = answer{200, `{"choices":[`}
	noChoices = answer{200, emptyReply}
)

func prompt(role models.Role, content string) models.Prompt {
	return models.Prompt{Role: role, Content: content}
}

func TestAskCompletion_SendsTheRequestAndReturnsTheReply(t *testing.T) {
	c, seen := scripted(t, ok("pong"))
	resp, err := c.AskCompletion(context.Background(), "model-1", []string{"basic_calculator"},
		prompt(models.User, "hello"), prompt(models.System, "be brief"))
	if err != nil {
		t.Fatal(err)
	}
	if got := resp.Choices[0].Message.Content; got != "pong" {
		t.Errorf("reply = %q, want pong", got)
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
	msgs := r.messages()
	if len(msgs) != 2 || msgs[0]["role"] != "user" || msgs[0]["content"] != "hello" ||
		msgs[1]["role"] != "system" || msgs[1]["content"] != "be brief" {
		t.Errorf("messages = %v, want user hello then system be brief", msgs)
	}
	tools, _ := r.body["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %v, want the one calculator schema", r.body["tools"])
	}
	fn := tools[0].(map[string]any)["function"].(map[string]any)
	if fn["name"] != "calculator" {
		t.Errorf("tool name = %v, want calculator", fn["name"])
	}
}

func TestAskCompletion_WithoutToolsSendsNone(t *testing.T) {
	c, seen := scripted(t, ok("hi"))
	if _, err := c.AskCompletion(context.Background(), "m", nil, prompt(models.User, "x")); err != nil {
		t.Fatal(err)
	}
	if tools, _ := seen()[0].body["tools"].([]any); len(tools) != 0 {
		t.Errorf("tools = %v, want none", tools)
	}
}

func TestAskCompletion_ReturnsAnErrorWhenTheCallFails(t *testing.T) {
	tests := []struct {
		name         string
		answers      []answer
		tools        []string
		wantErr      string
		wantRequests int
	}{
		{"http 500", []answer{http500}, nil, "500", 1},
		{"unreadable body", []answer{badJSON}, nil, "", 1},
		{"no choices", []answer{noChoices}, nil, "no choices", 1},
		{"unknown tool schema", nil, []string{"nope"}, `schema file for tool "nope" not found`, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, seen := scripted(t, tc.answers...)
			resp, err := c.AskCompletion(context.Background(), "m", tc.tools, prompt(models.User, "x"))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want an error containing %q", err, tc.wantErr)
			}
			if resp != nil {
				t.Errorf("resp = %v, want nil on error", resp)
			}
			if n := len(seen()); n != tc.wantRequests {
				t.Errorf("requests = %d, want %d", n, tc.wantRequests)
			}
		})
	}
}

func TestAgentTask_ReturnsTheFinalReply(t *testing.T) {
	tests := []struct {
		name         string
		answers      []answer
		wantReply    string
		wantRequests int
	}{
		{"no tool call", []answer{ok("Taipei")}, "Taipei", 1},
		{"one calculator call", []answer{
			{200, okBody("", withToolCalls(calcCall("t1", mulArgs)))},
			ok("the answer is 6"),
		}, "the answer is 6", 2},
		{"two calculator calls", []answer{
			{200, okBody("", withToolCalls(calcCall("t1", mulArgs), calcCall("t2", mulArgs)))},
			ok("first follow-up"),
			ok("second follow-up"),
		}, "second follow-up", 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, seen := scripted(t, tc.answers...)
			resp, err := c.AgentTask(context.Background(), "m", []string{"basic_calculator"}, prompt(models.User, "q"))
			if err != nil {
				t.Fatal(err)
			}
			if got := resp.Choices[0].Message.Content; got != tc.wantReply {
				t.Errorf("reply = %q, want %q", got, tc.wantReply)
			}
			if n := len(seen()); n != tc.wantRequests {
				t.Errorf("requests = %d, want %d", n, tc.wantRequests)
			}
		})
	}
}

func TestAgentTask_SendsTheCalculatorResultBack(t *testing.T) {
	c, seen := scripted(t,
		answer{200, okBody("", withToolCalls(calcCall("t1", mulArgs)))},
		ok("done"))
	if _, err := c.AgentTask(context.Background(), "m", []string{"basic_calculator"}, prompt(models.User, "2 x 3")); err != nil {
		t.Fatal(err)
	}
	reqs := seen()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2", len(reqs))
	}
	msgs := reqs[1].messages()
	if len(msgs) != 1 || msgs[0]["role"] != "tool" || msgs[0]["content"] != "6.00" {
		t.Errorf("follow-up messages = %v, want one tool message with content 6.00 (2 x 3, two decimals)", msgs)
	}
}

func TestAgentTask_ReturnsAnErrorInsteadOfPanicking(t *testing.T) {
	calc := func(args string) answer { return answer{200, okBody("", withToolCalls(calcCall("t1", args)))} }
	tests := []struct {
		name         string
		answers      []answer
		tools        []string
		wantErr      string
		wantRequests int
	}{
		{"first call fails", []answer{http500}, []string{"basic_calculator"}, "500", 1},
		{"first answer unreadable", []answer{badJSON}, []string{"basic_calculator"}, "", 1},
		{"first answer has no choices", []answer{noChoices}, []string{"basic_calculator"}, "no choices", 1},
		{"unknown operator", []answer{calc(badOpArgs)}, []string{"basic_calculator"}, "unknown operator", 1},
		{"arguments are not JSON", []answer{calc(notJSON)}, []string{"basic_calculator"}, "invalid character", 1},
		{"divide by zero", []answer{calc(divZero)}, []string{"basic_calculator"}, "not a finite number", 1},
		{"follow-up call fails", []answer{calc(mulArgs), http500}, []string{"basic_calculator"}, "500", 2},
		{"follow-up has no choices", []answer{calc(mulArgs), noChoices}, []string{"basic_calculator"}, "no choices", 2},
		{"second calculator call is bad", []answer{
			{200, okBody("", withToolCalls(calcCall("t1", mulArgs), calcCall("t2", badOpArgs)))},
			ok("follow-up"),
		}, []string{"basic_calculator"}, "unknown operator", 2},
		{"unknown tool schema", nil, []string{"nope"}, "not found", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, seen := scripted(t, tc.answers...)
			resp, err := c.AgentTask(context.Background(), "m", tc.tools, prompt(models.User, "q"))
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want an error containing %q", err, tc.wantErr)
			}
			if resp != nil {
				t.Errorf("resp = %v, want nil on error", resp)
			}
			if n := len(seen()); n != tc.wantRequests {
				t.Errorf("requests = %d, want %d", n, tc.wantRequests)
			}
		})
	}
}

func TestLoadExternToolSchemas(t *testing.T) {
	c := New(WithApiKey("k"))
	ctx := context.Background()

	none, err := c.LoadExternToolSchemas(ctx)
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("no tools: got %v, %v; want an empty non-nil slice and no error", none, err)
	}

	two, err := c.LoadExternToolSchemas(ctx, "basic_calculator", "basic_calculator")
	if err != nil || len(two) != 2 {
		t.Fatalf("two tools: got %d, %v; want 2 and no error", len(two), err)
	}
	if name := two[0].OfFunction.Function.Name; name != "calculator" {
		t.Errorf("tool name = %q, want calculator", name)
	}

	if _, err := c.LoadExternToolSchemas(ctx, "nope"); err == nil || !strings.Contains(err.Error(), `schema file for tool "nope" not found`) {
		t.Errorf("unknown tool: err = %v, want a not-found error", err)
	}
}

func TestOfCompletion(t *testing.T) {
	c := New(WithApiKey("k"))
	for _, role := range []models.Role{models.User, models.System, models.Assistant, models.Tool, models.Developer} {
		b, err := json.Marshal(c.ofCompletion(role, "text"))
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(b, &m); err != nil {
			t.Fatalf("%s: %v (%s)", role, err, b)
		}
		if m["role"] != string(role) || m["content"] != "text" {
			t.Errorf("%s: marshalled to %s, want role %q and content text", role, b, role)
		}
	}
	// the deprecated "function" role and unknown roles give an empty message, not a panic
	for _, role := range []models.Role{"function", "bogus"} {
		b, _ := json.Marshal(c.ofCompletion(role, "text"))
		if strings.Contains(string(b), "text") {
			t.Errorf("%q: marshalled to %s, want an empty message", role, b)
		}
	}
}

func TestListModels_ReturnsAnErrorWhenTheServerFails(t *testing.T) {
	c, _ := scripted(t, http500)
	if page, err := c.ListModels(context.Background()); err == nil || page != nil {
		t.Errorf("got %v, %v; want a nil page and an error", page, err)
	}
}

func TestNewOptions(t *testing.T) {
	c := New(WithBaseURL("http://example.test:1"), WithVersion("v9"), WithApiKey("key"), WithMaxRetries(2), WithMaxSteps(7))
	if c.baseURL != "http://example.test:1" || c.version != "v9" ||
		c.apiKey != "key" || // pragma: allowlist secret
		c.maxRetries != 2 || c.maxSteps != 7 {
		t.Errorf("options not applied: %+v", c)
	}
}
