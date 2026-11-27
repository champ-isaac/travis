package openai

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/champ-isaac/travis/models"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/packages/ssestream"
)

// skipEmptyEvents wraps the SDK's SSE decoder. The SDK dispatches an event at every blank line, so a
// comment-only block such as ": keepalive 3/20" (which mlx_lm.server sends while it works) becomes an
// event with no data, and the whole stream then fails with "unexpected end of JSON input". The SSE spec
// says to dispatch nothing when the data is empty, so this drops those events. An event whose only
// "data:" line is empty or blank reaches here as whitespace, so it is dropped too.
type skipEmptyEvents struct{ ssestream.Decoder }

func (d skipEmptyEvents) Next() bool {
	for d.Decoder.Next() {
		if len(bytes.TrimSpace(d.Decoder.Event().Data)) > 0 {
			return true
		}
	}
	return false
}

// Registered for the whole process: every SDK stream with one of these content types goes through it.
// The SDK picks a decoder by the exact Content-Type value, so the charset form needs its own entry.
func init() {
	wrap := func(body io.ReadCloser) ssestream.Decoder {
		// A response with no registered content type gets the SDK's default decoder.
		return skipEmptyEvents{ssestream.NewDecoder(&http.Response{Header: http.Header{}, Body: body})}
	}
	for _, contentType := range []string{"text/event-stream", "text/event-stream; charset=utf-8"} {
		ssestream.RegisterDecoder(contentType, wrap)
	}
}

// AskStreaming streams a reply from POST /v1/chat/completions. It sends every prompt as a chat
// message and always asks for stream_options.include_usage, so the server ends the stream with one
// chunk that holds the token counts.
//
// The channel gets one output per piece of content, then one last output with the response id and
// the usage (Usage is nil if the server sent none). Any failure is sent as an output with Error
// set, and the channel is then closed. A stream that ends without a finish_reason is a failure, and so is one
// that carries no chunks at all.
//
// Deltas are passed through as the server sends them, control tokens included: the Inkling decoder
// (build step 2) does not exist yet. Tool support is not implemented for streaming, so a non-empty
// tools is an error and no request is made.
func (c *Client) AskStreaming(ctx context.Context, model string, tools []string, prompts ...models.Prompt) <-chan *models.StreamingOutput {
	out := make(chan *models.StreamingOutput)

	go func() {
		defer close(out)
		// send gives up when ctx is cancelled, so this goroutine never blocks on a reader that has gone away.
		// If ctx is cancelled, an output that has not been received yet may be dropped.
		send := func(o *models.StreamingOutput) bool {
			select {
			case out <- o:
				return true
			case <-ctx.Done():
				return false
			}
		}
		if len(prompts) == 0 {
			send(&models.StreamingOutput{Error: errors.New("no prompts specified")})
			return
		}
		if prompts[0].Content == "" {
			send(&models.StreamingOutput{Error: errors.New("prompt content can't be empty")})
			return
		}

		if len(tools) > 0 {
			send(&models.StreamingOutput{Error: errors.New("tools are not supported when streaming yet")})
			return
		}

		messages := make([]openai.ChatCompletionMessageParamUnion, 0, len(prompts))
		for _, prompt := range prompts {
			msg, err := c.ofCompletion(prompt.Role, prompt.Content)
			if err != nil {
				send(&models.StreamingOutput{Error: err})
				return
			}
			messages = append(messages, msg)
		}
		stream := c.llm.Chat.Completions.NewStreaming(ctx, openai.ChatCompletionNewParams{
			Model:    model,
			Messages: messages,
			StreamOptions: openai.ChatCompletionStreamOptionsParam{
				IncludeUsage: param.Opt[bool]{Value: true},
			},
		})
		defer func() { _ = stream.Close() }() // closes the connection when we return early

		var id string
		var usage *models.Usage
		finished := false
		chunks := 0
		for stream.Next() {
			chunks++
			chunk := stream.Current()
			if chunk.ID != "" {
				id = chunk.ID
			}
			// The usage chunk has an empty choices list, so read usage before looking at choices.
			// Valid is false when the field is missing or null, which is how the other chunks carry it.
			if chunk.JSON.Usage.Valid() {
				usage = &models.Usage{
					PromptTokens:     chunk.Usage.PromptTokens,
					CompletionTokens: chunk.Usage.CompletionTokens,
					TotalTokens:      chunk.Usage.TotalTokens,
					ReasoningTokens:  chunk.Usage.CompletionTokensDetails.ReasoningTokens,
				}
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			choice := chunk.Choices[0]
			if choice.Delta.Content != "" {
				if !send(&models.StreamingOutput{Content: choice.Delta.Content}) {
					return
				}
			}
			if choice.FinishReason != "" {
				finished = true
			}
		}
		if err := stream.Err(); err != nil {
			send(&models.StreamingOutput{Error: err})
			return
		}
		if chunks == 0 {
			// Nothing parsed at all: typically a server that ignored stream: true and sent one plain JSON reply.
			send(&models.StreamingOutput{Error: errors.New("server sent no stream chunks (it may have ignored stream: true)")})
			return
		}
		if !finished {
			send(&models.StreamingOutput{Error: errors.New("stream ended without a finish_reason")})
			return
		}
		send(&models.StreamingOutput{Id: id, Usage: usage})
	}()
	return out
}
