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
		if len(prompts) == 0 {
			out <- &models.StreamingOutput{Error: errors.New("no prompts specified")}
			return
		}
		if prompts[0].Content == "" {
			out <- &models.StreamingOutput{Error: errors.New("prompt content can't be empty")}
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
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "response.output_text.delta":
				out <- &models.StreamingOutput{Content: event.Delta}
			case "response.completed":
				log.Println("\n\n>>>>completed!")
				//for streaming, can get response id when response completed
				out <- &models.StreamingOutput{Id: event.Response.ID}
			}
		}
		if err := stream.Err(); err != nil {
			out <- &models.StreamingOutput{Error: err}
		}
	}()
	return out
}
