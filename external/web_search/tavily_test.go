package web_search

import (
	"context"
	"os"
	"testing"

	"github.com/champ-isaac/travis/pkg/utils"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
)

func TestClient_Search(t *testing.T) {
	// Calls the live Tavily API: needs .env with TAVILY_BASEURL and TAVILY_TOKEN; skip when they are missing.
	envPath, err := utils.AbsFilePath(".env")
	assert.NoError(t, err)
	if envPath == "" {
		t.Skip("skipping: no .env in the project")
	}
	err = godotenv.Load(envPath)
	assert.NoError(t, err)

	baseUrl := os.Getenv("TAVILY_BASEURL")
	token := os.Getenv("TAVILY_TOKEN")
	if baseUrl == "" || token == "" {
		t.Skip("skipping: TAVILY_BASEURL or TAVILY_TOKEN is not set in .env")
	}

	client := New(
		WithBaseURL(baseUrl),
		WithToken(token),
		WithSessionID("5874812a-2e9b-43ea-8978-6cc9225b587b"),
		WithHumanID("h_4f9ac"),
		WithSearchDepth("advanced"),
	)
	assert.NotNil(t, client)
	data, err := client.Search(context.Background(), "Kipchoge's marathon world record")
	assert.NoError(t, err)

	t.Log("results: ", data.Results)
}
