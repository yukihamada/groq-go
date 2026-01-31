package conversation

import (
	"groq-go/internal/client"
)

// History manages conversation history
type History struct {
	messages []client.Message
	maxSize  int
}

// NewHistory creates a new conversation history
func NewHistory(maxSize int) *History {
	if maxSize <= 0 {
		maxSize = 100
	}
	return &History{
		messages: make([]client.Message, 0),
		maxSize:  maxSize,
	}
}

// Add appends a message to the history
func (h *History) Add(msg client.Message) {
	h.messages = append(h.messages, msg)

	// Trim if exceeds max size (keep system message if present)
	if len(h.messages) > h.maxSize {
		// Keep first message if it's a system message
		startIdx := 0
		if len(h.messages) > 0 && h.messages[0].Role == "system" {
			startIdx = 1
		}

		// Calculate how many to trim
		excess := len(h.messages) - h.maxSize
		if excess > 0 {
			if startIdx == 1 {
				// Keep system message, trim from the beginning of conversation
				h.messages = append(h.messages[:1], h.messages[1+excess:]...)
			} else {
				h.messages = h.messages[excess:]
			}
		}
	}
}

// AddAll appends multiple messages to the history
func (h *History) AddAll(msgs []client.Message) {
	for _, msg := range msgs {
		h.Add(msg)
	}
}

// Messages returns all messages in the history
func (h *History) Messages() []client.Message {
	return h.messages
}

// Clear removes all messages from the history
func (h *History) Clear() {
	h.messages = make([]client.Message, 0)
}

// Len returns the number of messages
func (h *History) Len() int {
	return len(h.messages)
}

// Last returns the last message, or nil if empty
func (h *History) Last() *client.Message {
	if len(h.messages) == 0 {
		return nil
	}
	return &h.messages[len(h.messages)-1]
}
