package openai

import (
	"context"
	"log"

	"github.com/champ-isaac/travis/models"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/pagination"
	"github.com/openai/openai-go/v3/packages/param"
	"github.com/openai/openai-go/v3/responses"
)

type Client struct {
	baseUrl string
	version string
	apiKey  string
	client  openai.Client
}

type Option func(*Client)

func WithBaseURL(baseUrl string) Option {
	return func(c *Client) {
		c.baseUrl = baseUrl
	}
}

func WithVersion(version string) Option {
	return func(c *Client) {
		c.version = version
	}
}

func WithApiKey(apiKey string) Option {
	return func(c *Client) {
		c.apiKey = apiKey //pragma: allowlist secret
	}
}

func New(options ...Option) *Client {
	c := &Client{
		baseUrl: "http://localhost:1234",
		version: "v1",
	}
	for _, opt := range options {
		opt(c)
	}
	reqOpts := make([]option.RequestOption, 0)
	reqOpts = append(reqOpts, option.WithBaseURL(c.baseUrl+"/"+c.version))
	reqOpts = append(reqOpts, option.WithMaxRetries(5))

	if c.apiKey == "" {
		log.Println("api key can't be empty")
		return nil
	}
	reqOpts = append(reqOpts, option.WithAPIKey(c.apiKey))
	c.client = openai.NewClient(reqOpts...)
	return c
}

func (c *Client) ListModels(ctx context.Context) (*pagination.Page[openai.Model], error) {
	resp, err := c.client.Models.List(ctx)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) AskCompletion(ctx context.Context, model string, prompts ...models.Prompt) (string, error) {
	completionMsgParamUnion := make([]openai.ChatCompletionMessageParamUnion, 0)
	for _, prompt := range prompts {
		completionMsgParamUnion = append(completionMsgParamUnion, ofCompletion(prompt.Role, prompt.Content))
	}
	resp, err := c.client.Chat.Completions.New(
		ctx,
		openai.ChatCompletionNewParams{
			Model:       model,
			Messages:    completionMsgParamUnion,
			Temperature: param.Opt[float64]{Value: 0.7},
		})
	if err != nil {
		return "", err
	}
	return resp.Choices[0].Message.Content, nil
}

func (c *Client) AskStreaming(ctx context.Context, model string, prompts ...models.Prompt) <-chan *models.StreamingOutput {
	out := make(chan *models.StreamingOutput)

	go func() {
		defer close(out)

		stream := c.client.Responses.NewStreaming(ctx, responses.ResponseNewParams{
			Model: model,
			Input: responses.ResponseNewParamsInputUnion{
				OfString: param.Opt[string]{
					Value: prompts[0].Content,
				},
			},
		})
		for stream.Next() {
			event := stream.Current()
			switch event.Type {
			case "response.output_text.delta":
				out <- &models.StreamingOutput{Content: event.Delta}
			case "response.completed":
				log.Println("\n\n>>>>completed!")
			}
		}
		if err := stream.Err(); err != nil {
			out <- &models.StreamingOutput{Error: err}
		}
	}()
	return out
}

func ofCompletion(role, content string) openai.ChatCompletionMessageParamUnion {
	ofString := param.Opt[string]{Value: content}

	switch role {
	case "system":
		return openai.ChatCompletionMessageParamUnion{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}
	case "user":
		return openai.ChatCompletionMessageParamUnion{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}
	case "assistant":
		return openai.ChatCompletionMessageParamUnion{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}
	default:
		return openai.ChatCompletionMessageParamUnion{}
	}
}
