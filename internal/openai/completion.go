package openai

import (
	"context"
	"encoding/json"
	"log"
	"os"
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

			result, _ := external.Calculator(args)
			resp, err = c.AskCompletion(ctx, model, tools, models.Prompt{
				Role:    models.Tool,
				Content: strconv.FormatFloat(result, 'f', 2, 64),
			})
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

func (c *Client) AskCompletion(ctx context.Context, model string, tools []string, prompts ...models.Prompt) (*openai.ChatCompletion, error) {
	var err error
	completionMsgParamUnion := make([]openai.ChatCompletionMessageParamUnion, 0)
	for _, prompt := range prompts {
		completionMsgParamUnion = append(completionMsgParamUnion, c.ofCompletion(prompt.Role, prompt.Content))
	}

	completionToolUnionParam := make([]openai.ChatCompletionToolUnionParam, 0)

	completionToolUnionParam, err = c.LoadExternToolSchemas(ctx, tools...)
	if err != nil {
		return nil, err
	}

	resp, err := c.client.Chat.Completions.New(
		ctx,
		openai.ChatCompletionNewParams{
			Model:       model,
			Messages:    completionMsgParamUnion,
			Temperature: param.Opt[float64]{Value: 0.7},
			Logprobs:    param.Opt[bool]{Value: true},
			Tools:       completionToolUnionParam,
			//StreamOptions: openai.ChatCompletionStreamOptionsParam{
			//	IncludeObfuscation: param.Opt[bool]{Value: true},
			//	IncludeUsage:       param.Opt[bool]{Value: true},
			//},
		})

	if err != nil {
		return nil, err
	}
	return resp, nil
}

func (c *Client) ofCompletion(role models.Role, content string) openai.ChatCompletionMessageParamUnion {
	ofString := param.Opt[string]{Value: content}

	switch role {
	case "system":
		return openai.ChatCompletionMessageParamUnion{
			OfSystem: &openai.ChatCompletionSystemMessageParam{
				Content: openai.ChatCompletionSystemMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}
	case "user":
		return openai.ChatCompletionMessageParamUnion{
			OfUser: &openai.ChatCompletionUserMessageParam{
				Content: openai.ChatCompletionUserMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}
	case "assistant":
		return openai.ChatCompletionMessageParamUnion{
			OfAssistant: &openai.ChatCompletionAssistantMessageParam{
				Content: openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}
	case "tool":
		return openai.ChatCompletionMessageParamUnion{
			OfTool: &openai.ChatCompletionToolMessageParam{
				Content: openai.ChatCompletionToolMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}
	case "developer":
		return openai.ChatCompletionMessageParamUnion{
			OfDeveloper: &openai.ChatCompletionDeveloperMessageParam{
				Content: openai.ChatCompletionDeveloperMessageParamContentUnion{
					OfString: ofString,
				},
			},
		}
	case "function":
		log.Println("function role is deprecated and don't use this role")
		return openai.ChatCompletionMessageParamUnion{}
	default:
		return openai.ChatCompletionMessageParamUnion{}
	}
}

func (c *Client) LoadExternToolSchemas(ctx context.Context, tools ...string) ([]openai.ChatCompletionToolUnionParam, error) {
	completionTools := make([]openai.ChatCompletionToolUnionParam, 0)
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
		bs, err := os.ReadFile(absPath)
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
