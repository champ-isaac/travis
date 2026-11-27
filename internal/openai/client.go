package openai

import (
	"context"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/packages/pagination"
)

type Client struct {
	baseURL    string
	version    string
	apiKey     string
	maxRetries int
	maxSteps   int
	llm        openai.Client
}

type Option func(*Client)

func WithBaseURL(baseUrl string) Option {
	return func(c *Client) {
		c.baseURL = baseUrl
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

func WithMaxSteps(maxSteps int) Option {
	return func(c *Client) {
		c.maxSteps = maxSteps
	}
}

func WithMaxRetries(maxRetries int) Option {
	return func(c *Client) {
		c.maxRetries = maxRetries
	}
}

func New(options ...Option) *Client {
	c := &Client{ // defaults
		baseURL:    "http://localhost:8080",
		version:    "v1",
		maxRetries: 5,
		maxSteps:   20,
	}
	for _, opt := range options {
		opt(c)
	}
	reqOpts := make([]option.RequestOption, 0)
	reqOpts = append(reqOpts, option.WithBaseURL(c.baseURL+"/"+c.version))
	reqOpts = append(reqOpts, option.WithMaxRetries(c.maxRetries))
	// An empty key is fine: the SDK then sends no Authorization header (mlx_lm.server needs none).
	reqOpts = append(reqOpts, option.WithAPIKey(c.apiKey))
	c.llm = openai.NewClient(reqOpts...)
	return c
}

func (c *Client) ListModels(ctx context.Context) (*pagination.Page[openai.Model], error) {
	resp, err := c.llm.Models.List(ctx)
	if err != nil {
		return nil, err
	}
	return resp, nil
}
