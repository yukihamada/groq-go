package tool

import (
	"context"
	"encoding/json"
)

// Result represents the result of a tool execution
type Result struct {
	Content string `json:"content"`
	IsError bool   `json:"is_error"`
}

// Tool is the interface that all tools must implement
type Tool interface {
	// Name returns the tool name
	Name() string

	// Description returns a description of what the tool does
	Description() string

	// Parameters returns the JSON schema for the tool parameters
	Parameters() map[string]any

	// Execute runs the tool with the given arguments
	Execute(ctx context.Context, args json.RawMessage) (Result, error)
}

// NewResult creates a successful result
func NewResult(content string) Result {
	return Result{
		Content: content,
		IsError: false,
	}
}

// NewErrorResult creates an error result
func NewErrorResult(err string) Result {
	return Result{
		Content: err,
		IsError: true,
	}
}
