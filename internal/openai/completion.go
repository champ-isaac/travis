package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"github.com/champ-isaac/travis/external"
	"github.com/champ-isaac/travis/models"
	"github.com/champ-isaac/travis/pkg/utils"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/packages/param"
)

// AgentTask call->execute->return->reason
func (c *Client) AgentTask(ctx context.Context, model string, tools []string, prompts ...models.Prompt) (*openai.ChatCompletion, error) {
	var err error
	//completionMsgParamUnion := make([]openai.ChatCompletionMessageParamUnion, 0)
	//for _, prompt := range prompts {
	//	completionMsgParamUnion = append(completionMsgParamUnion, c.ofCompletion(prompt.Role, prompt.Content))
	//}
	//
	//completionToolUnionParam := make([]openai.ChatCompletionToolUnionParam, 0)
	//
	//completionToolUnionParam, err = c.LoadExternToolSchemas(ctx, tools...)
	//if err != nil {
	//	return nil, err
	//}

	//messages := make([]string, 0)
	//messages := make([]openai.ChatCompletionMessage, 0)
	resp, err := c.AskCompletion(ctx, model, tools, prompts...)
	if err != nil {
		return nil, err
	}

	//resp, err := c.client.Chat.Completions.New(
	//	ctx,
	//	openai.ChatCompletionNewParams{
	//		Model:       model,
	//		Messages:    completionMsgParamUnion,
	//		Temperature: param.Opt[float64]{Value: 0.7},
	//		Logprobs:    param.Opt[bool]{Value: true},
	//		Tools:       completionToolUnionParam,
	//		//				ToolChoice: openai.ChatCompletionToolChoiceOptionUnionParam{
	//		//,					OfAuto:               param.Opt[string]{},
	//		//					OfAllowedTools:       nil,
	//		//					OfFunctionToolChoice: nil,
	//		//					OfCustomToolChoice:   nil,
	//		//				}(),
	//	})
	//if err != nil {
	//	return nil, err
	//}
	message := resp.Choices[0].Message //TODO: should we keep previous message Id, instead of Message?
	//messages = append(messages, openai.ChatCompletionMessage{
	//	Role:      "assistant",
	//	Content:   message.Content,
	//	ToolCalls: message.ToolCalls,
	//})

	//if len(message.ToolCalls) == 0 {
	//	return resp, nil
	//}
	for _, tool := range message.ToolCalls {
		funcName := tool.Function.Name
		funcArgs := tool.Function.Arguments
		log.Printf("calling %s with args %v", funcName, funcArgs)
		switch funcName {
		case "calculator":
			var args external.CalcArgs
			err = json.Unmarshal([]byte(funcArgs), &args)
			if err != nil {
				log.Printf("failed to unmarshal calculator args: %v", err)
				return nil, err
			}

			result, err := external.Calculator(ctx, args)
			if err != nil {
				return nil, err
			}
			resp, err = c.AskCompletion(ctx, model, tools, models.Prompt{
				Role:    models.Tool,
				Content: strconv.FormatFloat(result, 'f', 2, 64),
			})
			if err != nil {
				return nil, err
			}
		}
	}

	//finalResp, err := c.client.Chat.Completions.New(
	//	ctx,
	//	openai.ChatCompletionNewParams{
	//		Model:    model,
	//		Messages: []openai.ChatCompletionMessageParamUnion{},
	//	},
	//)
	//return finalResp, err
	return resp, nil
}

// AskCompletion is Complete at the fixed temperature 0.7, and it also asks for log-probabilities, as it always has.
func (c *Client) AskCompletion(ctx context.Context, model string, tools []string, prompts ...models.Prompt) (*openai.ChatCompletion, error) {
	return c.complete(ctx, model, 0.7, true, tools, prompts...)
}

// Complete sends one chat completion. The temperature is always sent, 0 included, so a caller that
// asks for 0 gets 0 and not the server's default. It does not ask for log-probabilities: nothing
// reads them, and they make every reply larger.
func (c *Client) Complete(ctx context.Context, model string, temperature float64, tools []string, prompts ...models.Prompt) (*openai.ChatCompletion, error) {
	return c.complete(ctx, model, temperature, false, tools, prompts...)
}

func (c *Client) complete(ctx context.Context, model string, temperature float64, logprobs bool, tools []string, prompts ...models.Prompt) (*openai.ChatCompletion, error) {
	completionMsgParamUnion := make([]openai.ChatCompletionMessageParamUnion, 0)
	for _, prompt := range prompts {
		msg, err := c.ofCompletion(prompt.Role, prompt.Content)
		if err != nil {
			return nil, err
		}
		completionMsgParamUnion = append(completionMsgParamUnion, msg)
	}

	completionToolUnionParam, err := c.LoadExternToolSchemas(ctx, tools...)
	if err != nil {
		return nil, err
	}

	params := openai.ChatCompletionNewParams{
		Model:       model,
		Messages:    completionMsgParamUnion,
		Temperature: openai.Float(temperature), // not param.Opt{Value: t}: that leaves 0 out of the request
		Tools:       completionToolUnionParam,
		//StreamOptions: openai.ChatCompletionStreamOptionsParam{
		//	IncludeObfuscation: param.Opt[bool]{Value: true},
		//	IncludeUsage:       param.Opt[bool]{Value: true},
		//},
	}
	if logprobs {
		params.Logprobs = param.Opt[bool]{Value: true}
	}
	resp, err := c.llm.Chat.Completions.New(ctx, params)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("server returned no choices for model %q", model)
	}
	return resp, nil
}

// ofCompletion builds one chat message. It returns an error for a role it cannot send, so that a
// prompt is never turned into an empty message and dropped without notice.
func (c *Client) ofCompletion(role models.Role, content string) (openai.ChatCompletionMessageParamUnion, error) {
	ofString := param.Opt[string]{Value: content}

	switch role {
	case "system":
		return openai.ChatCompletionMessageParamUnion{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}, nil
	case "user":
		return openai.ChatCompletionMessageParamUnion{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}, nil
	case "assistant":
		return openai.ChatCompletionMessageParamUnion{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}, nil
	case "tool":
		return openai.ChatCompletionMessageParamUnion{
			OfTool: &openai.ChatCompletionToolMessageParam{
				Content: openai.ChatCompletionToolMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}, nil
	case "developer":
		return openai.ChatCompletionMessageParamUnion{
			OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}, nil
	case "function":
		return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("unsupported prompt role %q: the function role is deprecated", role)
	default:
		return openai.ChatCompletionMessageParamUnion{}, fmt.Errorf("unsupported prompt role %q", role)
	}
}

func (c *Client) LoadExternToolSchemas(ctx context.Context, tools ...string) ([]openai.ChatCompletionToolUnionParam, error) {
	completionTools := make([]openai.ChatCompletionToolUnionParam, 0)
	if len(tools) == 0 {
		return completionTools, nil
	}
	projectRoot, err := utils.ProjectRoot()
	if err != nil {
		return nil, err
	}
	// os.Root confines every read to the project root, so a tool name cannot reach a file outside it.
	root, err := os.OpenRoot(projectRoot)
	if err != nil {
		return nil, err
	}
	defer func() { _ = root.Close() }() // read-only, nothing to flush
	//responses.ToolUnionParam{
	//	OfFunction:           nil,
	//	OfFileSearch:         nil,
	//	OfComputer:           nil,
	//	OfComputerUsePreview: nil,
	//	OfWebSearch:          nil,
	//	OfMcp:                nil,
	//	OfCodeInterpreter:    nil,
	//	OfImageGeneration:    nil,
	//	OfLocalShell:         nil,
	//	OfShell:              nil,
	//	OfCustom:             nil,
	//	OfNamespace:          nil,
	//	OfToolSearch:         nil,
	//	OfWebSearchPreview:   nil,
	//	OfApplyPatch:         nil,
	//}
	for _, tool := range tools {
		absPath, err := utils.AbsFilePath(tool + ".json")
		if err != nil {
			return nil, err
		}
		if absPath == "" {
			return nil, fmt.Errorf("schema file for tool %q not found under %s", tool, projectRoot)
		}
		relPath, err := filepath.Rel(projectRoot, absPath)
		if err != nil {
			return nil, err
		}
		bs, err := root.ReadFile(relPath)
		if err != nil {
			return nil, err
		}
		var schema openai.ChatCompletionFunctionToolParam
		if err := json.Unmarshal(bs, &schema); err != nil {
			return nil, err
		}
		completionTools = append(completionTools, openai.ChatCompletionToolUnionParam{
			OfFunction: &schema,
		})
	}
	return completionTools, nil
}
