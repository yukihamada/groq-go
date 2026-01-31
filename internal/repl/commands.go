package repl

import (
	"strings"
)

// Command represents a slash command
type Command struct {
	Name        string
	Description string
	Handler     func(r *REPL, args string) error
}

// DefaultCommands returns the built-in commands
func DefaultCommands() map[string]Command {
	return map[string]Command{
		"help": {
			Name:        "help",
			Description: "Show available commands",
			Handler:     cmdHelp,
		},
		"clear": {
			Name:        "clear",
			Description: "Clear conversation history",
			Handler:     cmdClear,
		},
		"model": {
			Name:        "model",
			Description: "Show or change the current model",
			Handler:     cmdModel,
		},
		"exit": {
			Name:        "exit",
			Description: "Exit the REPL",
			Handler:     cmdExit,
		},
		"quit": {
			Name:        "quit",
			Description: "Exit the REPL",
			Handler:     cmdExit,
		},
	}
}

func cmdHelp(r *REPL, args string) error {
	r.output.Println()
	r.output.Info("Available commands:")
	r.output.Println()
	r.output.Muted("  /help   - Show this help message")
	r.output.Muted("  /clear  - Clear conversation history")
	r.output.Muted("  /model  - Show or set model (e.g., /model llama-3.1-8b-instant)")
	r.output.Muted("  /exit   - Exit groq-go")
	r.output.Println()
	r.output.Info("Tips:")
	r.output.Muted("  - Press Ctrl+C to cancel current operation")
	r.output.Muted("  - Press Ctrl+D to exit")
	r.output.Println()
	return nil
}

func cmdClear(r *REPL, args string) error {
	r.history.Clear()
	// Re-add system message
	r.history.Add(r.context.SystemMessage())
	r.output.Success("Conversation cleared")
	return nil
}

func cmdModel(r *REPL, args string) error {
	args = strings.TrimSpace(args)
	if args == "" {
		r.output.Info("Current model: %s", r.client.Model())
		r.output.Println()
		r.output.Muted("Available models:")
		r.output.Muted("  - llama-3.3-70b-versatile (default)")
		r.output.Muted("  - llama-3.1-8b-instant")
		r.output.Muted("  - llama-3.2-90b-vision-preview")
		r.output.Muted("  - mixtral-8x7b-32768")
		return nil
	}

	r.client.SetModel(args)
	r.output.Success("Model changed to: %s", args)
	return nil
}

func cmdExit(r *REPL, args string) error {
	return ErrExit
}

// ParseCommand parses a slash command from input
func ParseCommand(input string) (cmd string, args string, isCmd bool) {
	if !strings.HasPrefix(input, "/") {
		return "", "", false
	}

	input = strings.TrimPrefix(input, "/")
	parts := strings.SplitN(input, " ", 2)
	cmd = strings.ToLower(parts[0])

	if len(parts) > 1 {
		args = parts[1]
	}

	return cmd, args, true
}
