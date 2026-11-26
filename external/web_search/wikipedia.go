package web_search

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"time"
)

type WikipediaClient struct {
	client  *http.Client
	baseURL string
}

func NewWikipediaClient(baseURL string, timeout time.Duration) *WikipediaClient {
	c := &http.Client{
		Timeout: timeout,
	}
	return &WikipediaClient{client: c, baseURL: baseURL}
}

func (wc *WikipediaClient) WikipediaSearchPage(ctx context.Context, query string) (any, error) {
	params := url.Values{}
	params.Set("limit", "3")
	params.Set("q", query)
	searchURL := "https://en.wikipedia.org/w/rest.php/v1/search/page?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, err
	}
	//'User-Agent': 'MediaWiki REST API docs examples/0.1 (https://meta.wikimedia.org/wiki/User:APaskulin_(WMF))
	req.Header.Add("User-Agent", "AI-Agent")
	resp, err := wc.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err = resp.Body.Close(); err != nil {
			log.Printf("failed to close response body: %v", err)
		}
	}()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("wikipedia: invalid status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	data, err := json.MarshalIndent(body, "", "  ")
	log.Printf("response body: \n%s\n", string(data))
	return data, err
}

func (wc *WikipediaClient) WikipediaSearchTitle(ctx context.Context, query string) {
	//search/title
}

func (wc *WikipediaClient) WikipediaGetPage(ctx context.Context, query string) {}

func (wc *WikipediaClient) WikipediaGetSections(ctx context.Context, query string) {}

func (wc *WikipediaClient) WikipediaGetCategories(ctx context.Context, query string) {}
