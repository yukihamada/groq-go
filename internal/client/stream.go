package client

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"strings"
)

var ErrStreamDone = errors.New("stream done")

// StreamReader reads SSE events from the API
type StreamReader struct {
	reader  io.ReadCloser
	scanner *bufio.Scanner
}

// NewStreamReader creates a new stream reader
func NewStreamReader(reader io.ReadCloser) *StreamReader {
	return &StreamReader{
		reader:  reader,
		scanner: bufio.NewScanner(reader),
	}
}

// Read reads the next chunk from the stream
func (s *StreamReader) Read() (*StreamChunk, error) {
	for s.scanner.Scan() {
		line := s.scanner.Text()

		// Skip empty lines
		if line == "" {
			continue
		}

		// SSE format: "data: {...}"
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		data := strings.TrimPrefix(line, "data: ")

		// Check for stream end
		if data == "[DONE]" {
			return nil, ErrStreamDone
		}

		var chunk StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return nil, err
		}

		return &chunk, nil
	}

	if err := s.scanner.Err(); err != nil {
		return nil, err
	}

	return nil, io.EOF
}

// Close closes the underlying reader
func (s *StreamReader) Close() error {
	return s.reader.Close()
}

// CollectResponse collects all chunks into a complete response
func (s *StreamReader) CollectResponse() (*Message, string, error) {
	var content strings.Builder
	var toolCalls []ToolCall
	var finishReason string
	toolCallsMap := make(map[int]*ToolCall)

	for {
		chunk, err := s.Read()
		if err == ErrStreamDone {
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
			if choice.Delta.Content != "" {
				content.WriteString(choice.Delta.Content)
			}

			// Handle streaming tool calls
			for _, tc := range choice.Delta.ToolCalls {
				existing, ok := toolCallsMap[tc.Index]
				if !ok {
					toolCallsMap[tc.Index] = &ToolCall{
						ID:   tc.ID,
						Type: tc.Type,
						Function: FunctionCall{
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

	// Convert map to slice
	for i := 0; i < len(toolCallsMap); i++ {
		if tc, ok := toolCallsMap[i]; ok {
			toolCalls = append(toolCalls, *tc)
		}
	}

	msg := &Message{
		Role:      "assistant",
		Content:   content.String(),
		ToolCalls: toolCalls,
	}

	return msg, finishReason, nil
}

