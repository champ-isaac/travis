package main

import (
	"context"
	"errors"
	"log"
	"os"

	ai "github.com/champ-isaac/travis/internal/openai"
	"github.com/champ-isaac/travis/models"
	"github.com/champ-isaac/travis/pkg/utils"
	"github.com/joho/godotenv"
)

type Client struct {
	client *ai.Client
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c, err := New()
	if err != nil {
		log.Println(err)
		return
	}

	model, err := c.getLoadedModel(ctx)
	if err != nil {
		log.Println(err)
		return
	}
	log.Println(model)

	//>>>>>> AskCompletion
	//resp, err := c.client.AskCompletion(ctx, model, []string{}, models.Prompt{
	//	Role:    models.User,
	//	Content: "Your name is Travis and what can you do?",
	//}, models.Prompt{
	//	Role:    models.Developer,
	//	Content: "You are a helpful assistant. Use tools when needed. When you have a final answer, respond without calling any tools.",
	//}, models.Prompt{
	//	Role:    models.System,
	//	Content: "you're a helpful assistant. Use tools when needed. When you have a final answer, respond without calling any tools.",
	//})

	//>>>>>> AgentTask
	resp, err := c.client.AgentTask(ctx, model, []string{"basic_calculator"}, models.Prompt{
		Role:    models.User,
		Content: "What's the capital of Taiwan?",
	})
	if err != nil {
		log.Println(err)
		return
	}
	log.Println(resp.Choices[0].Message.Content)
	log.Println("input tokens: ", resp.Usage.PromptTokens)
	log.Println("output tokens: ", resp.Usage.CompletionTokens)
	log.Println("reasoning tokens: ", resp.Usage.CompletionTokensDetails.ReasoningTokens)

	resp, err = c.client.AgentTask(ctx, model, []string{"basic_calculator"}, models.Prompt{
		Role:    models.User,
		Content: "What's is 1234 x 5678",
	})
	if err != nil {
		log.Println(err)
		return
	}

	log.Println(resp.Choices[0].Message.Content)
	log.Println("tool call: ", resp.Choices[0].Message.ToolCalls)
	log.Println("input tokens: ", resp.Usage.PromptTokens)
	log.Println("output tokens: ", resp.Usage.CompletionTokens)
	log.Println("reasoning tokens: ", resp.Usage.CompletionTokensDetails.ReasoningTokens)

	//>>>>>> AskStreaming
	/*
		out := c.client.AskStreaming(ctx, model, models.Prompt{
			Role:    models.User,
			Content: "who's there?",
		}, models.Prompt{
			Role: models.Developer,
			Content: "you're a helpful assistant",
		}, models.Prompt{
			Role: models.System,
			Content: "you're a helpful assistant",
		})
		for o := range out {
			if o.Error != nil {
				log.Println(o.Error)
				break
			}
			fmt.Print(o.Content)
			_ = os.Stdout.Sync()
		}
	*/
}

func New() (*Client, error) {
	envPath, err := utils.AbsFilePath(".env")
	if err != nil {
		return nil, err
	}
	err = godotenv.Load(envPath)
	if err != nil {
		return nil, err
	}
	baseUrl := os.Getenv("BASE_URL")
	token := os.Getenv("API_KEY")
	version := os.Getenv("VERSION")

	return &Client{
		client: ai.New(
			ai.WithBaseURL(baseUrl),
			ai.WithVersion(version),
			ai.WithApiKey(token),
			ai.WithMaxRetries(5),
			ai.WithMaxSteps(20)),
	}, nil
}

func (c *Client) getLoadedModel(ctx context.Context) (string, error) {
	resp, err := c.client.ListModels(ctx)
	if err != nil {
		return "", err
	}
	if len(resp.Data) == 0 {
		return "", errors.New("no models loaded")
	}
	return resp.Data[0].ID, nil
}
