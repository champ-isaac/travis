package lmstudio

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/champ-isaac/travis/models"
)

type Client struct {
	baseURL string
	token   string
	timeout time.Duration
	client  *http.Client
}

type Option func(*Client)

func WithBaseURL(baseUrl string) Option {
	return func(c *Client) {
		c.baseURL = baseUrl
	}
}

func WithToken(token string) Option {
	return func(c *Client) {
		c.token = token
	}
}

func WithTimeout(timeout time.Duration) Option {
	return func(client *Client) {
		client.timeout = timeout
	}
}

func New(options ...Option) *Client {
	c := &Client{
		baseURL: "https://localhost:1234",
		timeout: time.Second * 10, // setup individual timeout (optional)
	}
	for _, option := range options {
		option(c)
	}
	c.client = &http.Client{Timeout: c.timeout}
	return c
}

func (c *Client) LoadAIModel(ctx context.Context, model string) error {
	aiModel := models.AIModel{
		Model: model,
	}
	bs, err := json.Marshal(aiModel)
	if err != nil {
		return err
	}
	url := fmt.Sprintf("%s/%s", c.baseURL, "api/v1/models/load")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bs))
	if err != nil {
		return err
	}
	req.Header.Add("Content-Type", "application/json")
	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", c.token))

	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Fatal(err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("loadAIModel: invalid status code: %d", resp.StatusCode)
	}

	data, err := json.MarshalIndent(resp.Body, "", "  ")
	log.Printf("response body: \n%s\n", string(data))
	return nil
}
