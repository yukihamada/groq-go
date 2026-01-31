package repl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"groq-go/internal/client"
	"groq-go/internal/conversation"
	"groq-go/internal/tool"
)

var ErrExit = errors.New("exit requested")

// REPL is the Read-Eval-Print Loop for the CLI
type REPL struct {
	client   *client.Client
	registry *tool.Registry
	executor *tool.Executor
	history  *conversation.History
	context  *conversation.Context
	input    *Input
	output   *Output
	commands map[string]Command
}

// New creates a new REPL instance
func New(c *client.Client, registry *tool.Registry) (*REPL, error) {
	input, err := NewInput()
	if err != nil {
		return nil, fmt.Errorf("failed to initialize input: %w", err)
	}

	ctx := conversation.NewContext()
	history := conversation.NewHistory(100)
	history.Add(ctx.SystemMessage())

	return &REPL{
		client:   c,
		registry: registry,
		executor: tool.NewExecutor(registry),
		history:  history,
		context:  ctx,
		input:    input,
		output:   NewOutput(os.Stdout),
		commands: DefaultCommands(),
	}, nil
}

// Run starts the REPL loop
func (r *REPL) Run() error {
	defer r.input.Close()

	if !r.input.IsPiped() {
		r.printWelcome()
	}

	for {
		line, err := r.input.ReadLine()
		if IsEOF(err) {
			if !r.input.IsPiped() {
				r.output.Println()
				r.output.Muted("Goodbye!")
			}
			return nil
		}
		if IsInterrupt(err) {
			r.output.Println()
			continue
		}
		if err != nil {
			return fmt.Errorf("input error: %w", err)
		}

		if line == "" {
			continue
		}

		// Check for slash commands
		if cmd, args, isCmd := ParseCommand(line); isCmd {
			if handler, ok := r.commands[cmd]; ok {
				if err := handler.Handler(r, args); err != nil {
					if errors.Is(err, ErrExit) {
						if !r.input.IsPiped() {
							r.output.Muted("Goodbye!")
						}
						return nil
					}
					r.output.Error("%v", err)
				}
			} else {
				r.output.Error("Unknown command: /%s (type /help for available commands)", cmd)
			}
			continue
		}

		// Process user message
		if err := r.processMessage(line); err != nil {
			if errors.Is(err, context.Canceled) {
				r.output.Println()
				r.output.Warning("Cancelled")
				continue
			}
			r.output.Error("%v", err)
		}
	}
}

func (r *REPL) processMessage(userInput string) error {
	// Set up cancellation with Ctrl+C
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		select {
		case <-sigCh:
			cancel()
		case <-ctx.Done():
		}
	}()
	defer signal.Stop(sigCh)

	// Add user message to history
	r.history.Add(client.Message{
		Role:    "user",
		Content: userInput,
	})

	// Get tools for the API
	tools := r.registry.ToClientTools()

	// Main conversation loop
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		// Call the API with streaming
		stream, err := r.client.ChatCompletionStream(ctx, r.history.Messages(), tools)
		if err != nil {
			return fmt.Errorf("API error: %w", err)
		}

		// Collect the response while streaming
		msg, finishReason, err := r.streamResponse(ctx, stream)
		stream.Close()

		if err != nil {
			if errors.Is(err, context.Canceled) {
				return err
			}
			return fmt.Errorf("stream error: %w", err)
		}

		// Add assistant message to history
		r.history.Add(*msg)

		// Check if we need to execute tools
		if finishReason == "tool_calls" && len(msg.ToolCalls) > 0 {
			// Execute tool calls
			for _, tc := range msg.ToolCalls {
				r.output.ToolCall(tc.Function.Name, tc.Function.Arguments)

				result, _ := r.executor.ExecuteToolCall(ctx, tc)
				r.output.ToolResult(tc.Function.Name, result.Content, result.IsError)

				// Add tool result to history
				r.history.Add(client.Message{
					Role:       "tool",
					Content:    result.Content,
					ToolCallID: tc.ID,
				})
			}

			// Continue the loop to get the next response
			continue
		}

		// No more tool calls, we're done
		break
	}

	return nil
}

func (r *REPL) streamResponse(ctx context.Context, stream *client.StreamReader) (*client.Message, string, error) {
	var content string
	var toolCalls []client.ToolCall
	var finishReason string
	toolCallsMap := make(map[int]*client.ToolCall)

	r.output.Println()

	for {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		default:
		}

		chunk, err := stream.Read()
		if err == client.ErrStreamDone {
			break
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, "", err
		}

		if len(chunk.Choices) == 0 {
			continue
		}

		choice := chunk.Choices[0]
		if choice.FinishReason != "" {
			finishReason = choice.FinishReason
		}

		if choice.Delta != nil {
			// Stream content tokens
			if choice.Delta.Content != "" {
				r.output.StreamToken(choice.Delta.Content)
				content += choice.Delta.Content
			}

			// Accumulate tool calls
			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCallsMap[tc.Index]
				if !ok {
					toolCallsMap[tc.Index] = &client.ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: client.FunctionCall{
							Name:      tc.Function.Name,
							Arguments: tc.Function.Arguments,
						},
					}
				} else {
					if tc.ID != "" {
						existing.ID = tc.ID
					}
					if tc.Type != "" {
						existing.Type = tc.Type
					}
					if tc.Function.Name != "" {
						existing.Function.Name = tc.Function.Name
					}
					existing.Function.Arguments += tc.Function.Arguments
				}
			}
		}
	}

	// End streaming output
	if content != "" {
		r.output.StreamEnd()
	}
	r.output.Println()

	// Convert tool calls map to slice
	for i := 0; i < len(toolCallsMap); i++ {
		if tc, ok := toolCallsMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	msg := &client.Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: toolCalls,
	}

	return msg, finishReason, nil
}

func (r *REPL) printWelcome() {
	r.output.Println()
	r.output.Info("groq-go")
	r.output.Muted("Model: %s", r.client.Model())
	r.output.Muted("Type /help for commands, Ctrl+D to exit")
	r.output.Println()
}
