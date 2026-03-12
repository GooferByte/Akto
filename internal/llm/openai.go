package llm

import (
	"context"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// OpenAIClient implements Client using the OpenAI API.
type OpenAIClient struct {
	client openai.Client
}

// NewOpenAIClient creates an OpenAI-backed LLM client.
func NewOpenAIClient(apiKey string) *OpenAIClient {
	return &OpenAIClient{
		client: openai.NewClient(option.WithAPIKey(apiKey)),
	}
}

// ChatCompletion sends a chat completion request to OpenAI.
func (o *OpenAIClient) ChatCompletion(ctx context.Context, model string, messages []Message, tools []ToolDef) (*Response, error) {
	oaiMessages := make([]openai.ChatCompletionMessageParamUnion, len(messages))
	for i, m := range messages {
		oaiMessages[i] = toOpenAIMessage(m)
	}

	oaiTools := make([]openai.ChatCompletionToolUnionParam, len(tools))
	for i, t := range tools {
		oaiTools[i] = toOpenAITool(t)
	}

	completion, err := o.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(model),
		Messages: oaiMessages,
		Tools:    oaiTools,
	})
	if err != nil {
		return nil, fmt.Errorf("openai chat completion: %w", err)
	}

	if len(completion.Choices) == 0 {
		return &Response{}, nil
	}

	choice := completion.Choices[0]
	resp := &Response{Content: choice.Message.Content}

	for _, tc := range choice.Message.ToolCalls {
		resp.ToolCalls = append(resp.ToolCalls, ToolCall{
			ID:        tc.ID,
			Function:  tc.Function.Name,
			Arguments: tc.Function.Arguments,
		})
	}

	return resp, nil
}

// toOpenAIMessage converts an llm.Message to the OpenAI SDK param type.
func toOpenAIMessage(m Message) openai.ChatCompletionMessageParamUnion {
	switch m.Role {
	case RoleSystem:
		return openai.SystemMessage(m.Content)
	case RoleUser:
		return openai.UserMessage(m.Content)
	case RoleTool:
		return openai.ToolMessage(m.Content, m.ToolCallID)
	case RoleAssistant:
		msg := openai.AssistantMessage(m.Content)
		if len(m.ToolCalls) > 0 {
			calls := make([]openai.ChatCompletionMessageToolCallUnionParam, len(m.ToolCalls))
			for i, tc := range m.ToolCalls {
				calls[i] = openai.ChatCompletionMessageToolCallUnionParam{
					OfFunction: &openai.ChatCompletionMessageFunctionToolCallParam{
						ID: tc.ID,
						Function: openai.ChatCompletionMessageFunctionToolCallFunctionParam{
							Name:      tc.Function,
							Arguments: tc.Arguments,
						},
					},
				}
			}
			msg.OfAssistant.ToolCalls = calls
		}
		return msg
	default:
		return openai.UserMessage(m.Content)
	}
}

// toOpenAITool converts an llm.ToolDef to the OpenAI SDK tool param type.
func toOpenAITool(t ToolDef) openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
		Name:        t.Name,
		Description: openai.String(t.Description),
		Parameters:  openai.FunctionParameters(t.Parameters),
	})
}
