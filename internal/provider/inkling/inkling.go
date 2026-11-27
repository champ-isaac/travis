// Package inkling decodes the raw text that mlx_lm.server returns for Inkling 2 Mini.
//
// The server sends no tool_calls and no separate thinking field. Everything is text in the reply
// content, wrapped in control tokens. One reply is a run of messages:
//
//	<|message_model|><|content_thinking|>…<|end_message|>
//	<|message_model|><|content_text|>…<|end_message|>
//	<|message_model|>calculator<|content_invoke_tool_json|>{"name":"calculator","args":{…}}<|end_message|>
//
// The format is known only from replies captured from the server (internal/provider/inkling/testdata).
// A model or server update may change it.
package inkling

import (
	"bytes"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	messageStart = "<|message_model|>"
	messageEnd   = "<|end_message|>"
	thinkingTag  = "<|content_thinking|>"
	textTag      = "<|content_text|>"
	invokeTag    = "<|content_invoke_tool_json|>"

	whitespace = " \t\r\n"
)

// anyControlToken matches every token of the form <|name|>, where name is letters, digits and
// underscores, including tokens this package does not know.
var anyControlToken = regexp.MustCompile(`<\|\w+\|>`)

// ToolCall is one tool call the model asked for.
type ToolCall struct {
	ID        string // generated here, the model does not send one
	Name      string
	Arguments string // compact JSON object, the value of the model's "args" key, as the model wrote it
}

// Reply is a decoded reply. Thinking and Text hold no control tokens. Arguments is the model's JSON as
// sent, so a string inside it may hold any text, control-token look-alikes included.
type Reply struct {
	Thinking  string
	Text      string
	ToolCalls []ToolCall
}

// Decode turns the content of a complete reply into a Reply.
//
// Content with no <|message_model|> token is taken as plain text, with any stray control tokens removed.
// Text outside the messages (before the first, between two, after the last) is treated the same way
// everywhere: kept in Text as written, unless it is only whitespace. A reply cut off before its last
// <|end_message|> is decoded as far as it goes.
//
// These are errors, because guessing would be worse than failing: a tool call whose JSON is broken, cut
// off, has no name or has args that are not an object, and a message of a kind this package does not
// know (showing it as the answer could leak the model's thinking).
//
// On an error the returned Reply is not empty. It holds everything decoded before the message that
// failed, which the caller can log or keep. It is not a complete reply, so the caller decides whether
// to use it. The failing message is not in it.
func Decode(content string) (Reply, error) {
	var r Reply
	outside, rest, inMessage := strings.Cut(content, messageStart)
	r.addOutside(outside)
	for inMessage {
		var err error
		if rest, err = r.addMessage(rest); err != nil {
			return r, err
		}
		outside, rest, inMessage = strings.Cut(rest, messageStart)
		r.addOutside(outside)
	}
	return r, nil
}

// addOutside adds text that sits outside any message. Whitespace-only gaps are ignored.
func (r *Reply) addOutside(s string) {
	if s = strip(s); strings.TrimSpace(s) != "" {
		r.Text += s
	}
}

// addMessage decodes the message that starts at rest, which is the reply just after <|message_model|>.
// It returns what follows the message.
func (r *Reply) addMessage(rest string) (string, error) {
	body := strings.TrimLeft(rest, whitespace) // whitespace before the kind tag is not part of the message
	switch {
	case strings.HasPrefix(body, thinkingTag):
		text, after, _ := strings.Cut(strings.TrimPrefix(body, thinkingTag), messageEnd) // no end token: runs to the end of the reply
		r.Thinking += strip(text)
		return after, nil
	case strings.HasPrefix(body, textTag):
		text, after, _ := strings.Cut(strings.TrimPrefix(body, textTag), messageEnd)
		r.Text += strip(text)
		return after, nil
	}

	// A tool call has its invoke token before the message's end token. The end of its JSON is found by
	// parsing it, not by searching for <|end_message|>, because a string argument may hold that text.
	end, invoke := strings.Index(body, messageEnd), strings.Index(body, invokeTag)
	if invoke >= 0 && (end < 0 || invoke < end) {
		call, after, err := parseCall(strings.TrimSpace(body[:invoke]), body[invoke+len(invokeTag):])
		if err != nil {
			return "", err
		}
		r.ToolCalls = append(r.ToolCalls, call)
		return after, nil
	}

	msg, after, _ := strings.Cut(body, messageEnd)
	if msg != "" {
		return "", fmt.Errorf("inkling: unknown message kind %.40q", msg)
	}
	return after, nil // an empty message, or a reply cut off right after <|message_model|>
}

// parseCall reads the tool call JSON at the start of src: {"name": "...", "args": {...}}. The name before
// the invoke token is used only when the JSON has none. It returns the call and what follows the
// message, with the <|end_message|> that closes it consumed.
func parseCall(prefixName, src string) (ToolCall, string, error) {
	var j struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args"`
	}
	dec := json.NewDecoder(strings.NewReader(src))
	if err := dec.Decode(&j); err != nil {
		return ToolCall{}, "", fmt.Errorf("inkling: tool call JSON: %w", err)
	}
	after := src[dec.InputOffset():]
	switch trimmed := strings.TrimLeft(after, whitespace); {
	case strings.HasPrefix(trimmed, messageEnd):
		after = strings.TrimPrefix(trimmed, messageEnd)
	case trimmed == "": // the reply was cut off right after the JSON
		after = ""
	default:
		return ToolCall{}, "", fmt.Errorf("inkling: tool call JSON: unexpected text after the JSON: %.40q", trimmed)
	}

	name := j.Name
	if name == "" {
		name = prefixName
	}
	if name == "" {
		return ToolCall{}, "", errors.New("inkling: tool call has no name")
	}
	args := "{}"
	if len(j.Args) > 0 && !bytes.Equal(j.Args, []byte("null")) {
		if j.Args[0] != '{' { // RawMessage holds the value without surrounding whitespace
			return ToolCall{}, "", fmt.Errorf("inkling: tool call %q: args must be a JSON object, got %.40s", name, j.Args)
		}
		var buf bytes.Buffer
		if err := json.Compact(&buf, j.Args); err != nil { // unreachable after a successful Decode, kept as a guard
			return ToolCall{}, "", fmt.Errorf("inkling: tool call args: %w", err)
		}
		args = buf.String()
	}
	return ToolCall{ID: newID(), Name: name, Arguments: args}, after, nil
}

// newID returns a random ID. It is not a counter, so IDs from different replies in one conversation never collide.
func newID() string {
	return "call_" + strings.ToLower(rand.Text()[:12])
}

func strip(s string) string {
	return anyControlToken.ReplaceAllString(s, "")
}
