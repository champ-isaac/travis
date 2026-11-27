package openai

// AskStreaming tests. Same approach as completion_test.go: a scripted local server (httptest), no .env
// and no model server. The server speaks server-sent events (SSE) on POST /v1/chat/completions, in the
// format mlx_lm.server uses: "data:" lines only, a last chunk with empty choices that holds the usage,
// then "data: [DONE]".

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

const (
	streamID   = "chatcmpl-1"
	doneEvent  = "data: [DONE]\n\n"
	keepalive  = ": keepalive 1/20\n\n" // an SSE comment: the server sends these while it works, and they carry no data
	errorEvent = "data: {\"error\":{\"message\":\"overloaded\"}}\n\n"
)

func dataEvent(payload string) string { return "data: " + payload + "\n\n" }

// chunkEvent is one chat.completion.chunk with one choice. finish "" means finish_reason null.
// extra is appended to the chunk object, for example `,"usage":null`.
func chunkEvent(content, finish string, extra ...string) string {
	fr := "null"
	if finish != "" {
		fr = fmt.Sprintf("%q", finish)
	}
	return dataEvent(fmt.Sprintf(`{"id":%q,"object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":{"role":"assistant","content":%q},"finish_reason":%s}]%s}`,
		streamID, content, fr, strings.Join(extra, "")))
}

// usageEvent is the last chunk before [DONE] when the request sets include_usage: empty choices and the totals.
func usageEvent(prompt, completion, reasoning int) string {
	details := ""
	if reasoning > 0 {
		details = fmt.Sprintf(`,"completion_tokens_details":{"reasoning_tokens":%d}`, reasoning)
	}
	return dataEvent(fmt.Sprintf(`{"id":%q,"object":"chat.completion","created":1,"model":"m","choices":[],"usage":{"prompt_tokens":%d,"completion_tokens":%d,"total_tokens":%d%s}}`,
		streamID, prompt, completion, prompt+completion, details))
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

// render turns each output into one short string: content:..., id:... (with usage:prompt/completion/total/reasoning
// when there is usage) or error:...
func render(outs []*models.StreamingOutput) []string {
	var s []string
	for _, o := range outs {
		switch {
		case o.Error != nil:
			s = append(s, "error:"+o.Error.Error())
		case o.Usage != nil:
			s = append(s, fmt.Sprintf("id:%s usage:%d/%d/%d/%d", o.Id, o.Usage.PromptTokens, o.Usage.CompletionTokens, o.Usage.TotalTokens, o.Usage.ReasoningTokens))
		case o.Id != "":
			s = append(s, "id:"+o.Id)
		default:
			s = append(s, "content:"+o.Content)
		}
	}
	return s
}

func ask(t *testing.T, c *Client, ctx context.Context) []string {
	t.Helper()
	return render(drain(t, c.AskStreaming(ctx, "m", nil, models.Prompt{Role: models.User, Content: "hi"})))
}

func TestAskStreaming_StreamsTheContentThenIDAndUsage(t *testing.T) {
	c, seen := scriptedStream(t, sseAnswer{chunks: []string{
		keepalive,
		chunkEvent("", ""), // the first chunk carries the role and no content: no output for it
		chunkEvent("Hel", ""),
		keepalive,
		chunkEvent("lo", ""),
		chunkEvent("", "stop"),
		usageEvent(20, 24, 3),
		doneEvent,
	}})
	outs := drain(t, c.AskStreaming(context.Background(), "model-1", nil,
		models.Prompt{Role: models.User, Content: "hello"},
		models.Prompt{Role: models.System, Content: "be brief"}))

	want := []string{"content:Hel", "content:lo", "id:chatcmpl-1 usage:20/24/44/3"}
	if got := render(outs); strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("outputs = %v, want %v", got, want)
	}

	reqs := seen()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	r := reqs[0]
	if r.method != http.MethodPost || r.path != "/v1/chat/completions" {
		t.Errorf("request = %s %s, want POST /v1/chat/completions", r.method, r.path)
	}
	if r.body["model"] != "model-1" || r.body["stream"] != true {
		t.Errorf("body = %v, want model model-1 and stream true", r.body)
	}
	msgs := r.messages()
	if len(msgs) != 2 || msgs[0]["role"] != "user" || msgs[0]["content"] != "hello" ||
		msgs[1]["role"] != "system" || msgs[1]["content"] != "be brief" {
		t.Errorf("messages = %v, want user hello then system be brief: every prompt is sent, in order", msgs)
	}
	for _, key := range []string{"input", "instructions"} {
		if _, ok := r.body[key]; ok {
			t.Errorf("body has %q, a field of the Responses API", key)
		}
	}
	if tools, _ := r.body["tools"].([]any); len(tools) != 0 {
		t.Errorf("tools = %v, want none: tool support is not implemented in AskStreaming", tools)
	}
}

// The SDK picks its SSE decoder by the exact Content-Type value. With a charset parameter the keepalive
// comment would again become an empty event and fail the stream, unless that form is registered too.
func TestAskStreaming_KeepalivesWithACharsetContentType(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "text/event-stream; charset=utf-8"} {
		t.Run(contentType, func(t *testing.T) {
			c, _ := scriptedStream(t, sseAnswer{contentType: contentType, chunks: []string{
				keepalive, chunkEvent("hi", ""), keepalive, chunkEvent("", "stop"), usageEvent(2, 3, 0), doneEvent,
			}})
			got := ask(t, c, context.Background())
			want := []string{"content:hi", "id:chatcmpl-1 usage:2/3/5/0"}
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("outputs = %v, want %v", got, want)
			}
		})
	}
}

// Without include_usage the server sends no usage chunk, and the app would lose the token counts.
func TestAskStreaming_AlwaysAsksForUsage(t *testing.T) {
	c, seen := scriptedStream(t, sseAnswer{chunks: []string{chunkEvent("x", "stop"), doneEvent}})
	ask(t, c, context.Background())
	opts, _ := seen()[0].body["stream_options"].(map[string]any)
	if opts["include_usage"] != true {
		t.Errorf("stream_options = %v, want include_usage true", seen()[0].body["stream_options"])
	}
}

func TestAskStreaming_ReadsUsageOnlyFromTheFinalChunk(t *testing.T) {
	null := `,"usage":null` // OpenAI's own servers put usage:null on every other chunk
	c, _ := scriptedStream(t, sseAnswer{chunks: []string{
		chunkEvent("a", "", null),
		chunkEvent("b", "", null),
		chunkEvent("", "length", null),
		usageEvent(7, 9, 0),
		doneEvent,
	}})
	got := ask(t, c, context.Background())
	want := []string{"content:a", "content:b", "id:chatcmpl-1 usage:7/9/16/0"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("outputs = %v, want %v: exactly one output with usage", got, want)
	}
}

// The server may send no usage chunk. The reply is complete, so this is not an error, but Usage is nil.
func TestAskStreaming_ServerSendsNoUsage(t *testing.T) {
	c, _ := scriptedStream(t, sseAnswer{chunks: []string{chunkEvent("par", ""), chunkEvent("", "stop"), doneEvent}})
	got := ask(t, c, context.Background())
	want := []string{"content:par", "id:chatcmpl-1"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("outputs = %v, want %v", got, want)
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
		{"unknown role", []models.Prompt{{Role: models.User, Content: "hi"}, {Role: "bogus", Content: "x"}}, `unsupported prompt role "bogus"`},
		{"deprecated function role", []models.Prompt{{Role: models.User, Content: "hi"}, {Role: "function", Content: "x"}}, `unsupported prompt role "function": the function role is deprecated`},
		{"empty role", []models.Prompt{{Role: "", Content: "hi"}}, `unsupported prompt role ""`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, seen := scriptedStream(t, sseAnswer{chunks: []string{chunkEvent("never", "stop"), doneEvent}})
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
	tests := []struct {
		name       string
		answer     sseAnswer
		wantBefore []string // outputs expected before the error
		wantErr    string   // part of the error message
	}{
		{"http 500", sseAnswer{status: 500, contentType: "application/json", chunks: []string{`{"error":{"message":"boom"}}`}}, nil, "500"},
		{"http 404", sseAnswer{status: 404, contentType: "text/plain", chunks: []string{"Not Found"}}, nil, "404"},
		{"error event in the stream", sseAnswer{chunks: []string{chunkEvent("par", ""), errorEvent}}, []string{"content:par"}, "overloaded"},
		{"event that is not JSON", sseAnswer{chunks: []string{chunkEvent("par", ""), "data: {not json\n\n"}}, []string{"content:par"}, ""},
		{"connection dropped mid-stream", sseAnswer{chunks: []string{chunkEvent("par", "")}, abort: true}, []string{"content:par"}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := scriptedStream(t, tc.answer)
			got := ask(t, c, context.Background())
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
	c, _ := scriptedStream(t, sseAnswer{chunks: []string{chunkEvent("x", ""), chunkEvent("", "stop"), usageEvent(1, 1, 0), doneEvent}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got := ask(t, c, ctx)
	// The final error send and ctx.Done() are both ready, so the error may be dropped.
	// Either way no content or ID comes out, and the channel closes.
	if len(got) > 1 || (len(got) == 1 && (!strings.HasPrefix(got[0], "error:") || !strings.Contains(got[0], "context canceled"))) {
		t.Errorf("outputs = %v, want nothing or one context canceled error", got)
	}
}

const (
	incompleteStreamErr = "error:stream ended without a finish_reason"
	noChunksErr         = "error:server sent no stream chunks (it may have ignored stream: true)"
)

// A stream that ends without a finish_reason is an error, not a finished answer.
func TestAskStreaming_StreamEndingWithoutFinishReasonIsAnError(t *testing.T) {
	tests := []struct {
		name   string
		chunks []string
		want   []string
	}{
		{"cut off after some text", []string{chunkEvent("par", "")}, []string{"content:par", incompleteStreamErr}},
		{"cut off, then [DONE]", []string{chunkEvent("par", ""), doneEvent}, []string{"content:par", incompleteStreamErr}},
		{"usage chunk but no finish chunk", []string{chunkEvent("par", ""), usageEvent(1, 1, 0), doneEvent}, []string{"content:par", incompleteStreamErr}},
		{"no events at all", nil, []string{noChunksErr}},
		{"only keepalives and [DONE]", []string{keepalive, doneEvent}, []string{noChunksErr}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := scriptedStream(t, sseAnswer{chunks: tc.chunks})
			got := ask(t, c, context.Background())
			if strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("outputs = %v, want %v", got, tc.want)
			}
		})
	}
}

// (1) An event whose only "data:" line is empty or blank is no event: the SDK keeps "\n" for it, which is
// not empty, so the decoder wrapper has to trim before it checks.
func TestAskStreaming_SkipsEventsWhoseDataIsBlank(t *testing.T) {
	for name, blank := range map[string]string{
		"empty data line":        "data:\n\n",
		"data line with a space": "data: \n\n",
		"data line with tabs":    "data:\t \t\n\n",
		"two blank data lines":   "data:\ndata: \n\n",
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := scriptedStream(t, sseAnswer{chunks: []string{blank, chunkEvent("hi", ""), blank, chunkEvent("", "stop"), usageEvent(1, 1, 0), doneEvent}})
			got := ask(t, c, context.Background())
			want := []string{"content:hi", "id:chatcmpl-1 usage:1/1/2/0"}
			if strings.Join(got, "|") != strings.Join(want, "|") {
				t.Errorf("outputs = %v, want %v", got, want)
			}
		})
	}
}

// (2) Tool support is not implemented for streaming. Passing tools used to be ignored without a word.
func TestAskStreaming_RejectsToolsWithoutCallingTheServer(t *testing.T) {
	c, seen := scriptedStream(t, sseAnswer{chunks: []string{chunkEvent("never", "stop"), doneEvent}})
	outs := drain(t, c.AskStreaming(context.Background(), "m", []string{"basic_calculator"}, models.Prompt{Role: models.User, Content: "hi"}))
	want := "tools are not supported when streaming yet"
	if len(outs) != 1 || outs[0].Error == nil || outs[0].Error.Error() != want {
		t.Errorf("outputs = %v, want exactly one error %q", render(outs), want)
	}
	if n := len(seen()); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}

// (3) A server that ignores stream: true sends one plain JSON reply. That is not a cut-off stream, and the
// message should say what happened.
func TestAskStreaming_ServerIgnoresStreamTrue(t *testing.T) {
	body := `{"id":"c","object":"chat.completion","created":1,"model":"m","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"hi"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`
	c, _ := scriptedStream(t, sseAnswer{contentType: "application/json", chunks: []string{body}})
	got := ask(t, c, context.Background())
	if len(got) != 1 || got[0] != noChunksErr {
		t.Errorf("outputs = %v, want only %q", got, noChunksErr)
	}
	if strings.Contains(got[0], "finish_reason") {
		t.Errorf("message %q blames a missing finish_reason, but no stream was received", got[0])
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
	c, _ := scriptedStream(t, sseAnswer{chunks: []string{chunkEvent("a", ""), chunkEvent("b", ""), chunkEvent("c", ""), chunkEvent("", "stop"), usageEvent(1, 3, 0), doneEvent}})
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
