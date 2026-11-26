package web_search

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestWikipediaClient_WikipediaSearch(t *testing.T) {
	wikipediaClient := NewWikipediaClient("", 10*time.Second)
	data, err := wikipediaClient.WikipediaSearchPage(context.Background(), "the distance between Earth and Moon")
	assert.NoError(t, err)
	t.Log(data)
}
