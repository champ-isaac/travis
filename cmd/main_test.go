package main

import (
	"context"
	"errors"
	"net/url"
	"testing"

	"github.com/champ-isaac/travis/pkg/utils"
	"github.com/stretchr/testify/assert"
)

func TestCalculator(t *testing.T) {
	// Needs the local .env (BASE_URL, API_KEY, VERSION) and a running model server; skip when either is missing.
	envPath, err := utils.AbsFilePath(".env")
	assert.NoError(t, err)
	if envPath == "" {
		t.Skip("skipping: no .env in the project")
	}
	ctx := context.Background()
	c, err := New()
	assert.NoError(t, err)
	model, err := c.getLoadedModel(ctx)
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		t.Skipf("skipping: model server is not reachable: %v", err)
	}
	assert.NoError(t, err)
	t.Log(model)

	tools, err := c.client.LoadExternToolSchemas(ctx, "basic_calculator")
	assert.NoError(t, err)
	t.Log(tools)
	//c.client.AskCompletion(ctx, model, models.Prompt{})
}
