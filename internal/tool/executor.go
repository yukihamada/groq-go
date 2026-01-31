package tool

import (
	"context"
	"encoding/json"
	"fmt"

	"groq-go/internal/client"
)

// Executor handles tool execution
type Executor struct {
	registry *Registry
}

// NewExecutor creates a new tool executor
func NewExecutor(registry *Registry) *Executor {
	return &Executor{
		registry: registry,
	}
}

// ExecuteToolCall executes a single tool call and returns the result
func (e *Executor) ExecuteToolCall(ctx context.Context, tc client.ToolCall) (Result, error) {
	tool, ok := e.registry.Get(tc.Function.Name)
	if !ok {
		return NewErrorResult(fmt.Sprintf("unknown tool: %s", tc.Function.Name)), nil
	}

	args := json.RawMessage(tc.Function.Arguments)
	result, err := tool.Execute(ctx, args)
	if err != nil {
		return NewErrorResult(fmt.Sprintf("tool execution error: %v", err)), nil
	}

	return result, nil
}

// ExecuteToolCalls executes multiple tool calls and returns messages with results
func (e *Executor) ExecuteToolCalls(ctx context.Context, toolCalls []client.ToolCall) []client.Message {
	messages := make([]client.Message, 0, len(toolCalls))

	for _, tc := range toolCalls {
		result, _ := e.ExecuteToolCall(ctx, tc)

		msg := client.Message{
			Role:       "tool",
			Content:    result.Content,
			ToolCallID: tc.ID,
		}
		messages = append(messages, msg)
	}

	return messages
}
