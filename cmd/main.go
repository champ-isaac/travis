package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"

	ai "github.com/champ-isaac/travis/internal/openai"
	"github.com/champ-isaac/travis/models"
	"github.com/joho/godotenv"
)

type Client struct {
	client *ai.Client
}

func main() {
	ctx := context.Background()
	c, err := New()
	if err != nil {
		log.Fatal(err)
	}

	model, err := c.getLoadedModel(ctx)
	if err != nil {
		log.Fatal(err)
	}
	log.Println(model)
	//result, err := c.client.AskCompletion(ctx, model, models.Prompt{
	//	Role:    "user",
	//	Content: "what's Golang?",
	//})
	out := c.client.Streaming(ctx, model, models.Prompt{
		Role:    "user",
		Content: "what's a Golang",
	})
	for o := range out {
		if o.Error != nil {
			log.Println(o.Error)
			break
		}
		fmt.Print(o.Content)
		_ = os.Stdout.Sync()
	}
}

func New() (*Client, error) {
	err := godotenv.Load(".env")
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
			ai.WithApiKey(token)),
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
