package llm

import (
	"context"
	"fmt"
)

// Role represents the role of a message sender.
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message represents a single message in a conversation.
type Message struct {
	Role       Role
	Content    string
	ToolCalls  []ToolCall // assistant responses only
	ToolCallID string     // tool-result messages only
}

// ToolCall represents a tool/function call requested by the model.
type ToolCall struct {
	ID        string // provider-assigned call ID
	Function  string // function name
	Arguments string // raw JSON string
}

// ToolDef defines a tool available to the model.
type ToolDef struct {
	Name        string
	Description string
	Parameters  map[string]any // JSON Schema
}

// Response holds the model's reply from a chat completion.
type Response struct {
	Content   string
	ToolCalls []ToolCall
}

// Client is the provider-agnostic interface for LLM chat completions.
type Client interface {
	ChatCompletion(ctx context.Context, model string, messages []Message, tools []ToolDef) (*Response, error)
}

// NewClient creates an LLM client for the given provider.
func NewClient(provider, apiKey string) (Client, error) {
	switch provider {
	case "openai":
		return NewOpenAIClient(apiKey), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q", provider)
	}
}
