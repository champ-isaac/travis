package main

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCalculator(t *testing.T) {
	ctx := context.Background()
	c, err := New()
	assert.NoError(t, err)
	model, err := c.getLoadedModel(ctx)
	assert.NoError(t, err)
	t.Log(model)

	tools, err := c.client.LoadExternToolSchemas(ctx, "basic_calculator")
	assert.NoError(t, err)
	t.Log(tools)
	//c.client.AskCompletion(ctx, model, models.Prompt{})
}
