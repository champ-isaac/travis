package openai

import (
	"context"
	"errors"
	"log"

	"github.com/champ-isaac/travis/models"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

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

		// Tool support is stubbed until build step 5 replaces this function.
		stream := c.llm.Responses.NewStreaming(ctx, responses.ResponseNewParams{
			Model: model,
			//PreviousResponseID: param.Opt[string]{Value: ""},
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{
					Value: prompts[0].Content,
				},
			},
			Instructions: param.Opt[string]{Value: "You're a helpful assistant"},
		})
		completed := false
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "response.output_text.delta":
				if !send(&models.StreamingOutput{Content: event.Delta}) {
					return
				}
			case "response.completed":
				completed = true
				log.Println("\n\n>>>>completed!")
				//for streaming, can get response id when response completed
				if !send(&models.StreamingOutput{Id: event.Response.ID}) {
					return
				}
			}
		}
		if err := stream.Err(); err != nil {
			send(&models.StreamingOutput{Error: err})
			return
		}
		if !completed {
			send(&models.StreamingOutput{Error: errors.New("stream ended without a response.completed event")})
		}
	}()
	return out
}
