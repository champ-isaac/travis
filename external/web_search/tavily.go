package web_search

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

//
//	Implement Tavily Golang SDK
//

type SearchRequest struct {
	Query          string   `json:"query"`
	SearchDepth    string   `json:"search_depth"`
	IncludeDomains []string `json:"include_domains"`
	MaxResults     int      `json:"max_results"`
	StartDate      string   `json:"start_date,omitempty"`
	EndDate        string   `json:"end_date,omitempty"`
	TimeRange      string   `json:"time_range,omitempty"`
}

type Client struct {
	timeout        time.Duration
	token          string
	baseURL        string
	client         *http.Client
	searchDepth    string
	includeDomains []string
	startDate      string
	endDate        string
	timeRange      string
	maxResults     int
	sessionID      string
	humanID        string
}

type Option func(*Client)

func WithTimeout(timeout time.Duration) Option {
	return func(c *Client) {
		c.timeout = timeout
	}
}

func WithToken(token string) Option {
	return func(c *Client) {
		c.token = token
	}
}

func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = baseURL
	}
}

func WithSearchDepth(searchDepth string) Option {
	return func(c *Client) {
		c.searchDepth = searchDepth
	}
}

func WithIncludeDomains(includeDomains []string) Option {
	return func(c *Client) {
		c.includeDomains = includeDomains
	}
}

func WithMaxResults(maxResults int) Option {
	return func(c *Client) {
		c.maxResults = maxResults
	}
}

func WithTimeRange(timeRange string) Option {
	return func(c *Client) {
		c.timeRange = timeRange
	}
}

func WithStartDate(startDate string) Option {
	return func(c *Client) {
		c.startDate = startDate
	}
}

func WithEndDate(endDate string) Option {
	return func(c *Client) {
		c.endDate = endDate
	}
}

func WithSessionID(sessionID string) Option {
	return func(c *Client) {
		c.sessionID = sessionID
	}
}

func WithHumanID(humanID string) Option {
	return func(c *Client) {
		c.humanID = humanID
	}
}

func New(opts ...Option) *Client {
	c := &Client{
		timeout:     time.Second * 10,
		searchDepth: "basic", //advanced/fast/ultra-fast
		timeRange:   "month",
		maxResults:  3,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.token == "" {
		return nil
	}
	c.client = &http.Client{Timeout: c.timeout}

	return c
}

func (c *Client) Search(ctx context.Context, query string) (*models.SearchResponse, error) {
	body := SearchRequest{
		Query:          query,
		SearchDepth:    c.searchDepth,
		IncludeDomains: c.includeDomains,
		MaxResults:     c.maxResults,
		TimeRange:      c.timeRange,
	}
	bs, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	url := fmt.Sprintf("%s/%s", c.baseURL, "search")
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bs))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("X-Session-Id", c.sessionID)
	req.Header.Set("X-Human-Id", c.humanID)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			log.Fatal(err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("loadAIModel: invalid status code: %d", resp.StatusCode)
	}
	var searchResponse models.SearchResponse
	if err = json.NewDecoder(resp.Body).Decode(&searchResponse); err != nil {
		return nil, err
	}
	return &searchResponse, nil
}
