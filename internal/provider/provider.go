// Package provider is where the rest of Travis talks to a model. It holds the domain types, the
// Provider interface and Local, the implementation for an OpenAI-compatible server such as
// mlx_lm.server. Local runs every reply through the Inkling decoder (package inkling), so a caller
// gets clean text, thinking and tool calls and never sees a control token.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/champ-isaac/travis/internal/openai"
	"github.com/champ-isaac/travis/internal/provider/inkling"
	"github.com/champ-isaac/travis/models"
	sdk "github.com/openai/openai-go/v3"
)

// Request is one model call. The caller owns the conversation and sends all of it every time.
type Request struct {
	Model    string // the exact id the server reports at /v1/models: any other name can make the server drop its weights
	Messages []models.Prompt
	// Temperature is always sent, so 0 means 0 and not the server's default. A check that compares
	// two replies needs 0.
	Temperature float64
	Tools       []string // tool names, each loaded from its schema file
}

// ToolCall is one tool call the model asked for.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // a JSON object
}

// Response is one model reply. Text and Thinking hold no control tokens.
type Response struct {
	ID           string
	Text         string
	Thinking     string
	ToolCalls    []ToolCall
	FinishReason string        // "length" means the reply was cut off at the token limit
	Usage        *models.Usage // nil when the server sent none
}

// Provider is a model the core can call.
type Provider interface {
	Complete(ctx context.Context, req Request) (Response, error)
}

// Local is a Provider for an OpenAI-compatible server on this machine.
type Local struct {
	client *openai.Client
}

// defaultMaxRetries is how often the client repeats a failed request unless the caller passes
// openai.WithMaxRetries. The SDK would retry 5 times, on connection errors, 408, 409, 429 and every 5xx.
// A local single-GPU server that fails with a 5xx, for example out of memory, would then run the whole
// generation up to 5 more times, and a server that is down would take several seconds to fail. So
// there is no retry by default: the caller sees the failure at once and decides.
const defaultMaxRetries = 0

// NewLocal returns a Local for the server at baseURL, for example http://127.0.0.1:8080. Surrounding
// whitespace and trailing slashes are trimmed. After that the URL must be only a scheme, a host and a
// port: no credentials (the key is the separate apiKey argument, which may be empty because a local
// server needs none, and no Authorization header is sent then), no query or fragment, and no path,
// because the /v1 prefix is added for you and anything else would send requests to the wrong place.
// None of these checks puts the URL in its error message, so a secret put there by mistake does not
// reach a log that way. A URL that does not parse gives the parser's own reason instead, which can
// quote a few characters of the input, such as a bad escape sequence or port.
//
// A failed request is not retried (defaultMaxRetries is 0). opts are applied before the checked base
// URL and key, so they can override other defaults, for example openai.WithMaxRetries(2), but cannot
// change the URL or the key.
func NewLocal(baseURL, apiKey string, opts ...openai.Option) (*Local, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	u, err := url.Parse(baseURL)
	if err != nil {
		// url.Error would print the whole URL, so only its cause is kept. The cause can still quote a few
		// characters of the input.
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("provider: bad base URL: %w", err)
	}
	switch {
	case u.User != nil:
		return nil, errors.New("provider: base URL must not hold credentials: pass the key as the API key")
	case (u.Scheme != "http" && u.Scheme != "https") || u.Host == "":
		return nil, errors.New("provider: base URL must look like http://host:port")
	case strings.ContainsAny(baseURL, "?#"): // not RawQuery: an empty "?" or "#" also swallows the /v1 added after it
		return nil, errors.New("provider: base URL must not have a query or fragment")
	case u.Path != "":
		return nil, errors.New("provider: base URL must not have a path: the /v1 prefix is added for you")
	}
	all := append([]openai.Option{openai.WithMaxRetries(defaultMaxRetries)}, opts...)
	all = append(all, openai.WithBaseURL(baseURL), openai.WithApiKey(apiKey)) // last, so the checked values win
	return &Local{client: openai.New(all...)}, nil
}

// Complete sends the request and decodes the reply. It makes no call when the request has no model or
// no messages, or has a message role the server cannot take.
//
// When the reply cannot be decoded, for example a tool call with broken JSON, Complete returns an
// error together with a Response that holds the ID, the Usage (the call still cost tokens) and
// everything decoded before the failure.
//
// A reply cut off at the token limit has FinishReason "length". If the cut falls in text or thinking,
// it is decoded as far as it goes and is not an error. If it falls inside a tool call's JSON, that is an
// error like any broken tool call: the call cannot be run, and the Response holds what came before it.
func (l *Local) Complete(ctx context.Context, req Request) (Response, error) {
	if req.Model == "" {
		return Response{}, errors.New("provider: request has no model")
	}
	if len(req.Messages) == 0 {
		return Response{}, errors.New("provider: request has no messages")
	}
	cmp, err := l.client.Complete(ctx, req.Model, req.Temperature, req.Tools, req.Messages...)
	if err != nil {
		return Response{}, err
	}

	choice := cmp.Choices[0] // the client has checked there is one
	resp := Response{ID: cmp.ID, FinishReason: choice.FinishReason}
	if cmp.JSON.Usage.Valid() {
		resp.Usage = &models.Usage{
			PromptTokens:     cmp.Usage.PromptTokens,
			CompletionTokens: cmp.Usage.CompletionTokens,
			TotalTokens:      cmp.Usage.TotalTokens,
			ReasoningTokens:  cmp.Usage.CompletionTokensDetails.ReasoningTokens,
		}
	}

	reply, err := inkling.Decode(choice.Message.Content)
	resp.Text, resp.Thinking = reply.Text, reply.Thinking
	for _, call := range reply.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall(call))
	}
	if err != nil {
		return resp, fmt.Errorf("provider: decoding the reply: %w", err)
	}
	// Inkling 2 Mini on mlx_lm.server sends none, but a server that parses tool calls itself does.
	// They get the same checks as decoded ones, so the core never sees a call it cannot run.
	for _, call := range choice.Message.ToolCalls {
		tc, err := nativeToolCall(call)
		if err != nil {
			return resp, err
		}
		resp.ToolCalls = append(resp.ToolCalls, tc)
	}
	return resp, nil
}

// nativeToolCall checks a tool call that the server parsed itself. Only function calls with a name are
// accepted, and the arguments must be a JSON object. Missing or null arguments become {}, as in the decoder.
func nativeToolCall(call sdk.ChatCompletionMessageToolCallUnion) (ToolCall, error) {
	if call.Type != "function" {
		return ToolCall{}, fmt.Errorf("provider: server sent a %q tool call, only function tool calls are supported", call.Type)
	}
	name := call.Function.Name
	if name == "" {
		return ToolCall{}, errors.New("provider: tool call has no name")
	}
	args := strings.TrimSpace(call.Function.Arguments)
	if args == "" || args == "null" {
		args = "{}"
	}
	var obj map[string]json.RawMessage
	if args[0] != '{' || json.Unmarshal([]byte(args), &obj) != nil {
		return ToolCall{}, fmt.Errorf("provider: tool call %q: arguments must be a JSON object, got %.40q", name, args)
	}
	return ToolCall{ID: call.ID, Name: name, Arguments: args}, nil
}
