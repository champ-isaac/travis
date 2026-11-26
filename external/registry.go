package external

import "context"

type Tool[Req any, Resp any] struct {
	Name        string
	Description string
	Parameters  map[string]any
	Execute     func(context.Context, Req) (Resp, error)
}

type ToolRegistry[Req any, Resp any] struct {
	tools map[string]*Tool[Req, Resp]
}

func (r *ToolRegistry[Req, Resp]) Register(tool *Tool[Req, Resp]) {
	r.tools[tool.Name] = tool
}

func (r *ToolRegistry[Req, Resp]) GetSchema() []any {
	schemas := make([]any, 0, len(r.tools))
	for _, tool := range r.tools {
		schemas = append(schemas, []any{
			"name", tool.Name,
			"description", tool.Description,
			"parameters", tool.Parameters,
		})
	}
	return schemas
}
