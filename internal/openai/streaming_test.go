package openai

// AskStreaming tests. Same approach as completion_test.go: a scripted local server (httptest), no .env
// and no model server. The server speaks server-sent events (SSE) on POST /v1/responses.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/champ-isaac/travis/models"
)

type sseAnswer struct {
	status      int
	contentType string   // default text/event-stream
	chunks      []string // written one by one, each flushed
	abort       bool     // after the chunks, drop the connection without ending the stream cleanly
}

// scriptedStream serves the same answer to every request and records the requests.
func scriptedStream(t *testing.T, a sseAnswer) (*Client, func() []request) {
	t.Helper()
	var mu sync.Mutex
	var got []request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, request{method: r.Method, path: r.URL.Path, body: decodeBody(raw)})
		mu.Unlock()
		ct := a.contentType
		if ct == "" {
			ct = "text/event-stream"
		}
		w.Header().Set("Content-Type", ct)
		status := a.status
		if status == 0 {
			status = 200
		}
		w.WriteHeader(status)
		for _, c := range a.chunks {
			_, _ = io.WriteString(w, c)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		if a.abort {
			panic(http.ErrAbortHandler) // closes the connection without the final chunk
		}
	}))
	t.Cleanup(srv.Close)
	c := New(WithBaseURL(srv.URL), WithVersion("v1"), WithApiKey("k"), WithMaxRetries(0))
	return c, func() []request {
		mu.Lock()
		defer mu.Unlock()
		return append([]request(nil), got...)
	}
}

func decodeBody(raw []byte) map[string]any {
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	return m
}

func sse(eventType, data string) string { return "event: " + eventType + "\ndata: " + data + "\n\n" }

const respObj = `"response":{"id":%q,"object":"response","created_at":1,"status":"completed","model":"m","output":[],"parallel_tool_calls":false,"tool_choice":"auto","tools":[]}`

func createdEvent(id string) string {
	return sse("response.created", fmt.Sprintf(`{"type":"response.created","sequence_number":0,`+respObj+`}`, id))
}

func deltaEvent(text string) string {
	return sse("response.output_text.delta", fmt.Sprintf(`{"type":"response.output_text.delta","item_id":"i","output_index":0,"content_index":0,"delta":%q,"sequence_number":1,"logprobs":[]}`, text))
}

func completedEvent(id string) string {
	return sse("response.completed", fmt.Sprintf(`{"type":"response.completed","sequence_number":9,`+respObj+`}`, id))
}

// drain reads the channel until AskStreaming closes it, and fails the test if it never does.
func drain(t *testing.T, ch <-chan *models.StreamingOutput) []*models.StreamingOutput {
	t.Helper()
	var got []*models.StreamingOutput
	timeout := time.After(10 * time.Second)
	for {
		select {
		case o, ok := <-ch:
			if !ok {
				return got
			}
			got = append(got, o)
		case <-timeout:
			t.Fatal("AskStreaming did not close its channel within 10s")
		}
	}
}

// render turns each output into one short string: content:..., id:... or error:...
func render(outs []*models.StreamingOutput) []string {
	var s []string
	for _, o := range outs {
		switch {
		case o.Error != nil:
			s = append(s, "error:"+o.Error.Error())
		case o.Id != "":
			s = append(s, "id:"+o.Id)
		default:
			s = append(s, "content:"+o.Content)
		}
	}
	return s
}

func TestAskStreaming_StreamsTheDeltasThenTheResponseID(t *testing.T) {
	c, seen := scriptedStream(t, sseAnswer{chunks: []string{
		createdEvent("resp_created"), // ignored: the ID is taken from response.completed
		deltaEvent("Hel"),
		deltaEvent("lo"),
		sse("response.output_text.done", `{"type":"response.output_text.done","item_id":"i","output_index":0,"content_index":0,"text":"Hello","sequence_number":2,"logprobs":[]}`), // ignored
		completedEvent("resp_1"),
	}})
	outs := drain(t, c.AskStreaming(context.Background(), "model-1", []string{"basic_calculator"},
		models.Prompt{Role: models.User, Content: "hello"},
		models.Prompt{Role: models.System, Content: "ignored: only the first prompt is sent"}))

	want := []string{"content:Hel", "content:lo", "id:resp_1"}
	if got := render(outs); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("outputs = %v, want %v", got, want)
	}

	reqs := seen()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.method != http.MethodPost || r.path != "/v1/responses" {
		t.Errorf("request = %s %s, want POST /v1/responses", r.method, r.path)
	}
	if r.body["model"] != "model-1" || r.body["input"] != "hello" || r.body["stream"] != true {
		t.Errorf("body = %v, want model model-1, input hello, stream true", r.body)
	}
	if r.body["instructions"] != "You're a helpful assistant" {
		t.Errorf("instructions = %v", r.body["instructions"])
	}
	if tools, _ := r.body["tools"].([]any); len(tools) != 0 {
		t.Errorf("tools = %v, want none: tool support is stubbed in AskStreaming", tools)
	}
}

func TestAskStreaming_RejectsBadPromptsWithoutCallingTheServer(t *testing.T) {
	tests := []struct {
		name    string
		prompts []models.Prompt
		want    string
	}{
		{"no prompts", nil, "no prompts specified"},
		{"empty first prompt", []models.Prompt{{Role: models.User, Content: ""}, {Role: models.User, Content: "second is not used"}}, "prompt content can't be empty"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, seen := scriptedStream(t, sseAnswer{chunks: []string{completedEvent("never")}})
			outs := drain(t, c.AskStreaming(context.Background(), "m", nil, tc.prompts...))
			if len(outs) != 1 || outs[0].Error == nil || outs[0].Error.Error() != tc.want {
				t.Errorf("outputs = %v, want exactly one error %q", render(outs), tc.want)
			}
			if n := len(seen()); n != 0 {
				t.Errorf("requests = %d, want 0", n)
			}
		})
	}
}

func TestAskStreaming_ReportsAFailedCallAsTheLastOutput(t *testing.T) {
	prompt := models.Prompt{Role: models.User, Content: "hi"}
	tests := []struct {
		name       string
		answer     sseAnswer
		wantBefore []string // outputs expected before the error
		wantErr    string   // part of the error message
	}{
		{"http 500", sseAnswer{status: 500, contentType: "application/json", chunks: []string{`{"error":{"message":"boom"}}`}}, nil, "500"},
		{"error event in the stream", sseAnswer{chunks: []string{deltaEvent("par"), "data: {\"error\":{\"message\":\"overloaded\"}}\n\n"}}, []string{"content:par"}, "overloaded"},
		{"event that is not JSON", sseAnswer{chunks: []string{deltaEvent("par"), "data: {not json\n\n"}}, []string{"content:par"}, ""},
		{"connection dropped mid-stream", sseAnswer{chunks: []string{deltaEvent("par")}, abort: true}, []string{"content:par"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := scriptedStream(t, tc.answer)
			got := render(drain(t, c.AskStreaming(context.Background(), "m", nil, prompt)))
			if len(got) != len(tc.wantBefore)+1 {
				t.Fatalf("outputs = %v, want %v then one error", got, tc.wantBefore)
			}
			for i, w := range tc.wantBefore {
				if got[i] != w {
					t.Errorf("output %d = %q, want %q", i, got[i], w)
				}
			}
			last := got[len(got)-1]
			if !strings.HasPrefix(last, "error:") || !strings.Contains(last, tc.wantErr) {
				t.Errorf("last output = %q, want an error containing %q", last, tc.wantErr)
			}
		})
	}
}

func TestAskStreaming_StopsWhenTheContextIsAlreadyCancelled(t *testing.T) {
	c, _ := scriptedStream(t, sseAnswer{chunks: []string{deltaEvent("x"), completedEvent("resp_1")}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := render(drain(t, c.AskStreaming(ctx, "m", nil, models.Prompt{Role: models.User, Content: "hi"})))
	// The final error send and ctx.Done() are both ready, so the error may be dropped.
	// Either way no content or ID comes out, and the channel closes.
	if len(got) > 1 || (len(got) == 1 && (!strings.HasPrefix(got[0], "error:") || !strings.Contains(got[0], "context canceled"))) {
		t.Errorf("outputs = %v, want nothing or one context canceled error", got)
	}
}

const incompleteStreamErr = "error:stream ended without a response.completed event"

// A stream that ends without response.completed is an error, not a finished answer.
func TestAskStreaming_StreamEndingWithoutCompletedIsAnError(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"cut off after some text", []string{createdEvent("r"), deltaEvent("par")}, []string{"content:par", incompleteStreamErr}},
		{"no events at all", nil, []string{incompleteStreamErr}},
		{"only [DONE]", []string{"data: [DONE]\n\n"}, []string{incompleteStreamErr}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := scriptedStream(t, sseAnswer{chunks: tc.chunks})
			got := render(drain(t, c.AskStreaming(context.Background(), "m", nil, models.Prompt{Role: models.User, Content: "hi"})))
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("outputs = %v, want %v", got, tc.want)
			}
		})
	}
}

// askStreamingGoroutines returns the stack of each live goroutine that runs AskStreaming's goroutine body.
func askStreamingGoroutines() []string {
	buf := make([]byte, 1<<20)
	buf = buf[:runtime.Stack(buf, true)]
	var found []string
	for _, g := range strings.Split(string(buf), "\n\n") {
		if strings.Contains(g, "AskStreaming.func1") {
			found = append(found, g)
		}
	}
	return found
}

// waitFor polls cond for up to 5s.
func waitFor(cond func() bool) bool {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

// The reader takes one chunk and walks away. The goroutine ends up blocked on the channel send.
// Cancelling the context must make it exit, with nobody reading.
func TestAskStreaming_GoroutineExitsWhenTheContextIsCancelledWhileSendIsBlocked(t *testing.T) {
	c, _ := scriptedStream(t, sseAnswer{chunks: []string{deltaEvent("a"), deltaEvent("b"), deltaEvent("c"), completedEvent("resp_1")}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := c.AskStreaming(ctx, "m", nil, models.Prompt{Role: models.User, Content: "hi"})
	if first := <-ch; first.Content != "a" {
		t.Fatalf("first output = %+v, want content a", first)
	}

	// Blocked in the send closure (AskStreaming.func1.1): a select on the channel and ctx.Done().
	blocked := func() bool {
		for _, g := range askStreamingGoroutines() {
			if strings.Contains(g, "AskStreaming.func1.1") && (strings.Contains(g, "[select") || strings.Contains(g, "[chan send")) {
				return true
			}
		}
		return false
	}
	if !waitFor(blocked) {
		t.Fatalf("the streaming goroutine never blocked on its send:\n%v", askStreamingGoroutines())
	}

	cancel()
	if !waitFor(func() bool { return len(askStreamingGoroutines()) == 0 }) {
		t.Fatalf("the streaming goroutine is still alive 5s after cancel:\n%v", askStreamingGoroutines())
	}
}
