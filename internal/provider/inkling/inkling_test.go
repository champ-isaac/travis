package inkling

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// loadFixture returns the exact content of a reply captured from the local server.
// See testdata/README.md for how the files were captured.
func loadFixture(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var fx struct {
		FinishReason string `json:"finish_reason"`
		Content      string `json:"content"`
	}
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	if fx.FinishReason != "stop" {
		t.Fatalf("%s: finish_reason = %q, a cut-off reply is not a usable fixture", name, fx.FinishReason)
	}
	if !strings.Contains(fx.Content, "<|") {
		t.Fatalf("%s: fixture has no control tokens, so it does not test stripping", name)
	}
	return fx.Content
}

// calcArgs mirrors the arguments of schemas/basic_calculator.json.
type calcArgs struct {
	Operator     string  `json:"operator"`
	FirstNumber  float64 `json:"first_number"`
	SecondNumber float64 `json:"second_number"`
}

func mustDecode(t *testing.T, content string) Reply {
	t.Helper()
	r, err := Decode(content)
	if err != nil {
		t.Fatalf("Decode: unexpected error %v", err)
	}
	return r
}

// assertClean fails if any field of the reply still holds a control token.
func assertClean(t *testing.T, r Reply) {
	t.Helper()
	fields := []string{r.Thinking, r.Text}
	for _, c := range r.ToolCalls {
		fields = append(fields, c.ID, c.Name, c.Arguments)
	}
	for _, f := range fields {
		if strings.Contains(f, "<|") || strings.Contains(f, "|>") {
			t.Errorf("control token left in %q", f)
		}
	}
}

func assertCall(t *testing.T, c ToolCall, name string, want calcArgs) {
	t.Helper()
	if c.Name != name {
		t.Errorf("tool name = %q, want %q", c.Name, name)
	}
	if c.ID == "" {
		t.Error("tool call has no ID")
	}
	var got calcArgs
	if err := json.Unmarshal([]byte(c.Arguments), &got); err != nil {
		t.Fatalf("arguments %q are not valid JSON for the calculator: %v", c.Arguments, err)
	}
	if got != want {
		t.Errorf("arguments = %+v, want %+v", got, want)
	}
}

func TestDecode_PlainReplyStripsControlTokens(t *testing.T) {
	r := mustDecode(t, loadFixture(t, "plain"))
	assertClean(t, r)
	if r.Text != "Hello!" {
		t.Errorf("Text = %q, want %q", r.Text, "Hello!")
	}
	if !strings.HasPrefix(r.Thinking, `The user asks: "Say hello in one short sentence."`) || !strings.HasSuffix(r.Thinking, "Final exactly that.") {
		t.Errorf("Thinking = %q, want the whole thinking part and nothing around it", r.Thinking)
	}
	if len(r.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %+v, want none", r.ToolCalls)
	}
}

func TestDecode_FinalAnswerAfterAToolResult(t *testing.T) {
	r := mustDecode(t, loadFixture(t, "final_answer"))
	assertClean(t, r)
	if r.Text != "1234 × 5678 = 7,006,652" {
		t.Errorf("Text = %q, want the answer sentence", r.Text)
	}
	if !strings.HasPrefix(r.Thinking, "The user asks for 1234 x 5678 using calculator tool.") {
		t.Errorf("Thinking = %q", r.Thinking)
	}
	if len(r.ToolCalls) != 0 {
		t.Errorf("ToolCalls = %+v, want none", r.ToolCalls)
	}
}

func TestDecode_SingleToolCall(t *testing.T) {
	r := mustDecode(t, loadFixture(t, "single_tool_call"))
	assertClean(t, r)
	if len(r.ToolCalls) != 1 {
		t.Fatalf("ToolCalls = %+v, want exactly 1", r.ToolCalls)
	}
	assertCall(t, r.ToolCalls[0], "calculator", calcArgs{Operator: "multiply", FirstNumber: 1234, SecondNumber: 5678})
	if r.Text != "" {
		t.Errorf("Text = %q, want empty: the model sent only a tool call", r.Text)
	}
	if !strings.HasPrefix(r.Thinking, "The user wants 1234 x 5678 using calculator tool.") {
		t.Errorf("Thinking = %q", r.Thinking)
	}
}

func TestDecode_MultipleToolCallsKeepOrderAndGetDistinctIDs(t *testing.T) {
	r := mustDecode(t, loadFixture(t, "multiple_tool_calls"))
	assertClean(t, r)
	if len(r.ToolCalls) != 2 {
		t.Fatalf("ToolCalls = %+v, want exactly 2", r.ToolCalls)
	}
	assertCall(t, r.ToolCalls[0], "calculator", calcArgs{Operator: "multiply", FirstNumber: 12, SecondNumber: 34})
	assertCall(t, r.ToolCalls[1], "calculator", calcArgs{Operator: "add", FirstNumber: 56, SecondNumber: 78})
	if r.ToolCalls[0].ID == r.ToolCalls[1].ID {
		t.Errorf("both calls got the ID %q, the tool results could not be matched back", r.ToolCalls[0].ID)
	}
	if r.Text != "" {
		t.Errorf("Text = %q, want empty", r.Text)
	}
	// the blank lines inside the thinking belong to it and must survive
	if !strings.Contains(r.Thinking, "I need to make two calculator calls.") || !strings.Contains(r.Thinking, "\n\n12 times 34: multiply 12 by 34.\n56 plus 78: add 56 and 78.\n\n") {
		t.Errorf("Thinking lost its line breaks: %q", r.Thinking)
	}
}

// A new ID for every decode: with the full message history of step 7, two turns must not both hold "call_1".
func TestDecode_IDsDifferBetweenDecodes(t *testing.T) {
	content := loadFixture(t, "single_tool_call")
	a, b := mustDecode(t, content), mustDecode(t, content)
	if len(a.ToolCalls) != 1 || len(b.ToolCalls) != 1 {
		t.Fatalf("want 1 tool call in each decode, got %d and %d", len(a.ToolCalls), len(b.ToolCalls))
	}
	if a.ToolCalls[0].ID == b.ToolCalls[0].ID {
		t.Errorf("two decodes of the same reply gave the same ID %q", a.ToolCalls[0].ID)
	}
}

// The cases below are written by hand, not captured. They pin down what the decoder does when the
// server's output is not the usual shape. The wire format is known only from the replies seen so far.
func TestDecode_Unusual(t *testing.T) {
	const (
		start  = "<|message_model|>"
		end    = "<|end_message|>"
		think  = "<|content_thinking|>"
		text   = "<|content_text|>"
		invoke = "<|content_invoke_tool_json|>"
	)
	tests := []struct {
		name         string
		in           string
		wantText     string
		wantThinking string
		wantCalls    int
		wantErr      string
	}{
		{name: "empty reply", in: ""},
		{name: "no control tokens at all", in: "Just words.", wantText: "Just words."},
		{name: "stray control token in plain text", in: "Hi <|end_message|>there", wantText: "Hi there"},
		{name: "unknown control token inside a text message", in: start + text + "a<|content_new|>b" + end, wantText: "ab"},
		{name: "reply cut off before the end token", in: start + think + "hm" + end + start + text + "Partly", wantThinking: "hm", wantText: "Partly"},
		{name: "text then a tool call", in: start + text + "Let me check." + end + start + "calculator" + invoke + `{"name":"calculator","args":{"operator":"add","first_number":1,"second_number":2}}` + end, wantText: "Let me check.", wantCalls: 1},
		{name: "tool call JSON is broken", in: start + "calculator" + invoke + `{"name":"calculator","args":{"operator":` + end, wantErr: "tool call"},
		{name: "tool call cut off in the JSON", in: start + "calculator" + invoke + `{"name":"calculator","args":{"opera`, wantErr: "tool call"},
		{name: "tool call with no name anywhere", in: start + invoke + `{"args":{}}` + end, wantErr: "name"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Decode(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			assertClean(t, r)
			if r.Text != tc.wantText || r.Thinking != tc.wantThinking || len(r.ToolCalls) != tc.wantCalls {
				t.Errorf("got text %q, thinking %q, %d calls; want text %q, thinking %q, %d calls",
					r.Text, r.Thinking, len(r.ToolCalls), tc.wantText, tc.wantThinking, tc.wantCalls)
			}
		})
	}
}

func TestDecode_ToolCallWithoutArgsGetsAnEmptyObject(t *testing.T) {
	r := mustDecode(t, `<|message_model|>clock<|content_invoke_tool_json|>{"name":"clock"}<|end_message|>`)
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].Name != "clock" || r.ToolCalls[0].Arguments != "{}" {
		t.Errorf("ToolCalls = %+v, want one call named clock with arguments {}", r.ToolCalls)
	}
}

// The name comes from the JSON. When the JSON has none, the name before the invoke token is used.
func TestDecode_ToolNameFallsBackToTheTokenPrefix(t *testing.T) {
	r := mustDecode(t, `<|message_model|>calculator<|content_invoke_tool_json|>{"args":{"operator":"add"}}<|end_message|>`)
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].Name != "calculator" || r.ToolCalls[0].Arguments != `{"operator":"add"}` {
		t.Errorf("ToolCalls = %+v", r.ToolCalls)
	}
}

// Review finding 1: the arguments are documented as a JSON object. A number, string, array or bool would
// pass through and only fail later, when the caller unmarshals them into the tool's argument struct.
func TestDecode_ToolCallArgsMustBeAJSONObject(t *testing.T) {
	call := func(args string) string {
		return messageStart + "calc" + invokeTag + `{"name":"calc","args":` + args + `}` + messageEnd
	}
	for name, args := range map[string]string{"number": `5`, "string": `"1234 * 5678"`, "array": `[1,2]`, "bool": `true`} {
		t.Run(name, func(t *testing.T) {
			r, err := Decode(call(args))
			if err == nil || !strings.Contains(err.Error(), "args must be a JSON object") || !strings.Contains(err.Error(), `"calc"`) {
				t.Fatalf("err = %v, want an error saying the args of %q must be a JSON object", err, "calc")
			}
			if len(r.ToolCalls) != 0 {
				t.Errorf("ToolCalls = %+v, want none alongside the error", r.ToolCalls)
			}
		})
	}
	// null still means no arguments, like leaving the key out.
	r := mustDecode(t, call(`null`))
	if len(r.ToolCalls) != 1 || r.ToolCalls[0].Arguments != "{}" {
		t.Errorf("ToolCalls = %+v, want one call with arguments {}", r.ToolCalls)
	}
}

// Review finding 2: whitespace before the kind tag is skipped, and a message of a kind this package does not
// know is an error. Before, both fell through to Text, so private thinking could show up as the answer.
func TestDecode_MessageKinds(t *testing.T) {
	const call = `calculator` + invokeTag + `{"name":"calculator","args":{"operator":"add","first_number":1,"second_number":2}}`
	tests := []struct {
		name         string
		in           string
		wantThinking string
		wantText     string
		wantCalls    int
		wantErr      string
	}{
		{name: "newline before the thinking tag", in: messageStart + "\n" + thinkingTag + "private reasoning" + messageEnd + messageStart + textTag + "Answer" + messageEnd, wantThinking: "private reasoning", wantText: "Answer"},
		{name: "spaces before the text tag", in: messageStart + "  " + textTag + "Answer" + messageEnd, wantText: "Answer"},
		{name: "newline before a tool call", in: messageStart + "\n" + call + messageEnd, wantCalls: 1},
		{name: "whitespace after the tag is kept", in: messageStart + textTag + " Answer\n" + messageEnd, wantText: " Answer\n"},
		{name: "unknown kind token", in: messageStart + "<|content_reasoning|>private reasoning" + messageEnd + messageStart + textTag + "Answer" + messageEnd, wantErr: "unknown message kind"},
		{name: "message with no kind token", in: messageStart + "just words" + messageEnd, wantErr: "unknown message kind"},
		{name: "empty message is skipped", in: messageStart + messageEnd + messageStart + textTag + "ok" + messageEnd, wantText: "ok"},
		{name: "reply cut off right after the start token", in: messageStart + textTag + "ok" + messageEnd + messageStart, wantText: "ok"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Decode(tc.in)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				if r.Text != "" || r.Thinking != "" {
					t.Errorf("got Text %q and Thinking %q alongside the error, want none", r.Text, r.Thinking)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error %v", err)
			}
			assertClean(t, r)
			if r.Thinking != tc.wantThinking || r.Text != tc.wantText || len(r.ToolCalls) != tc.wantCalls {
				t.Errorf("got thinking %q, text %q, %d calls; want thinking %q, text %q, %d calls",
					r.Thinking, r.Text, len(r.ToolCalls), tc.wantThinking, tc.wantText, tc.wantCalls)
			}
		})
	}
}

// Review finding 3: text outside any message is handled one way everywhere. Non-blank text is kept as
// Text, exactly as written, wherever it sits. A gap that is only whitespace is ignored. Before, text before
// the first message was kept, and text between messages or after the last one was silently dropped.
func TestDecode_TextOutsideMessages(t *testing.T) {
	a := messageStart + textTag + "A" + messageEnd
	b := messageStart + textTag + "B" + messageEnd
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "before the first message", in: "Lead " + a, want: "Lead A"},
		{name: "between two messages", in: a + " middle " + b, want: "A middle B"},
		{name: "after the last message", in: a + " tail", want: "A tail"},
		{name: "in all three places", in: "x " + a + " y " + b + " z", want: "x A y B z"},
		{name: "whitespace-only gaps are ignored", in: "\n" + a + "\n\n" + b + "\n", want: "AB"},
		{name: "a stray control token in a gap is stripped", in: a + "<|content_new|>" + b, want: "AB"},
		{name: "no messages at all is plain text", in: "Just words.", want: "Just words."},
		{name: "plain text keeps its own whitespace", in: "  indented\n", want: "  indented\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := mustDecode(t, tc.in)
			assertClean(t, r)
			if r.Text != tc.want {
				t.Errorf("Text = %q, want %q", r.Text, tc.want)
			}
		})
	}
}

// Review finding 1: control tokens are not only lower-case letters. A token with a digit or a capital
// ("<|reserved_12|>") used to stay in the text.
func TestDecode_ControlTokensWithDigitsAndCapitals(t *testing.T) {
	tests := []struct {
		name         string
		in           string
		wantText     string
		wantThinking string
	}{
		{name: "in a text message", in: messageStart + textTag + "a <|reserved_12|>b <|Channel|>c <|tok2A|>d" + messageEnd, wantText: "a b c d"},
		{name: "in thinking", in: messageStart + thinkingTag + "x <|reserved_1|>y" + messageEnd, wantThinking: "x y"},
		{name: "outside a message", in: "<|pad_0|>Lead " + messageStart + textTag + "A" + messageEnd, wantText: "Lead A"},
		{name: "in plain text", in: "Hi <|Reserved_Special_Token_3|>there", wantText: "Hi there"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := mustDecode(t, tc.in)
			assertClean(t, r)
			if r.Text != tc.wantText || r.Thinking != tc.wantThinking {
				t.Errorf("got text %q, thinking %q; want text %q, thinking %q", r.Text, r.Thinking, tc.wantText, tc.wantThinking)
			}
		})
	}
}

// Review finding 2: when a message fails to decode, the Reply returned with the error holds everything
// decoded before it. A reply cut off in its second tool call used to lose the thinking, the text and the
// first call. The failing message itself is not in the partial reply.
func TestDecode_ErrorKeepsWhatWasDecodedBefore(t *testing.T) {
	good := messageStart + "calculator" + invokeTag + `{"name":"calculator","args":{"operator":"add","first_number":1,"second_number":2}}` + messageEnd
	before := messageStart + thinkingTag + "plan" + messageEnd + messageStart + textTag + "First, " + messageEnd + good
	tests := []struct{ name, failing string }{
		{name: "second call cut off by max_tokens", failing: messageStart + "calculator" + invokeTag + `{"name":"calculator","args":{"a`},
		{name: "second call with args that are not an object", failing: messageStart + "calculator" + invokeTag + `{"name":"calculator","args":5}` + messageEnd},
		{name: "unknown message kind", failing: messageStart + "<|content_reasoning|>hidden" + messageEnd},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Decode(before + tc.failing)
			if err == nil {
				t.Fatal("want an error")
			}
			if r.Thinking != "plan" || r.Text != "First, " {
				t.Errorf("partial reply has thinking %q and text %q, want %q and %q", r.Thinking, r.Text, "plan", "First, ")
			}
			if len(r.ToolCalls) != 1 {
				t.Fatalf("partial reply has %d tool calls, want the 1 decoded before the failure", len(r.ToolCalls))
			}
			assertCall(t, r.ToolCalls[0], "calculator", calcArgs{Operator: "add", FirstNumber: 1, SecondNumber: 2})
		})
	}
}

// Review finding 3: the end of a tool call is where its JSON ends, not the first <|end_message|> after it.
// A string argument may hold that text. Before, the call was cut there and failed.
func TestDecode_EndTokenInsideAJSONStringArgument(t *testing.T) {
	const arg = `{"s":"a` + messageEnd + `b"}`
	echo := messageStart + "echo" + invokeTag + `{"name":"echo","args":` + arg + `}` + messageEnd
	after := messageStart + textTag + "after" + messageEnd

	t.Run("the call is decoded whole and the next message still is", func(t *testing.T) {
		r := mustDecode(t, echo+after)
		if len(r.ToolCalls) != 1 || r.ToolCalls[0].Name != "echo" || r.ToolCalls[0].Arguments != arg {
			t.Fatalf("ToolCalls = %+v, want one echo call with arguments %s", r.ToolCalls, arg)
		}
		if r.Text != "after" {
			t.Errorf("Text = %q, want %q", r.Text, "after")
		}
	})
	t.Run("two such calls in a row", func(t *testing.T) {
		r := mustDecode(t, echo+echo)
		if len(r.ToolCalls) != 2 || r.ToolCalls[1].Arguments != arg {
			t.Errorf("ToolCalls = %+v, want two echo calls", r.ToolCalls)
		}
	})
	t.Run("whitespace between the JSON and the end token", func(t *testing.T) {
		r := mustDecode(t, messageStart+"echo"+invokeTag+`{"name":"echo","args":{}}`+" \n"+messageEnd+after)
		if len(r.ToolCalls) != 1 || r.Text != "after" {
			t.Errorf("got %d calls and text %q, want 1 call and %q", len(r.ToolCalls), r.Text, "after")
		}
	})
	t.Run("a reply cut off right after complete JSON still decodes", func(t *testing.T) {
		r := mustDecode(t, messageStart+"echo"+invokeTag+`{"name":"echo","args":{}}`)
		if len(r.ToolCalls) != 1 {
			t.Errorf("got %d calls, want 1", len(r.ToolCalls))
		}
	})
	t.Run("words after the JSON are an error, not a silent drop", func(t *testing.T) {
		_, err := Decode(messageStart + "echo" + invokeTag + `{"name":"echo","args":{}} and then words` + messageEnd)
		if err == nil || !strings.Contains(err.Error(), "unexpected text after the JSON") {
			t.Errorf("err = %v, want one saying there is unexpected text after the JSON", err)
		}
	})
}
