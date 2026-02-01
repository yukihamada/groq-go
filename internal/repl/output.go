package repl

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/fatih/color"
)

// Output handles formatted output to the terminal
type Output struct {
	writer io.Writer
}

// NewOutput creates a new output handler
func NewOutput(w io.Writer) *Output {
	return &Output{writer: w}
}

// Print prints a message
func (o *Output) Print(format string, args ...any) {
	fmt.Fprintf(o.writer, format, args...)
}

// Println prints a message with a newline
func (o *Output) Println(args ...any) {
	fmt.Fprintln(o.writer, args...)
}

// Printf prints a formatted message
func (o *Output) Printf(format string, args ...any) {
	fmt.Fprintf(o.writer, format, args...)
}

// Assistant prints assistant output in a distinct style
func (o *Output) Assistant(text string) {
	o.Println()
	o.Print("%s", text)
	if !strings.HasSuffix(text, "\n") {
		o.Println()
	}
	o.Println()
}

// ToolCall prints a tool call notification with argument summary
func (o *Output) ToolCall(name string, args string) {
	cyan := color.New(color.FgCyan, color.Bold)
	gray := color.New(color.FgHiBlack)

	cyan.Fprintf(o.writer, "● %s", name)

	// Parse and display key arguments
	summary := o.summarizeArgs(name, args)
	if summary != "" {
		gray.Fprintf(o.writer, " %s", summary)
	}
	fmt.Fprintln(o.writer)
}

// summarizeArgs creates a brief summary of tool arguments
func (o *Output) summarizeArgs(toolName string, args string) string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		return ""
	}

	switch toolName {
	case "Read":
		if fp, ok := parsed["file_path"].(string); ok {
			return shortenPath(fp)
		}
	case "Write":
		if fp, ok := parsed["file_path"].(string); ok {
			return shortenPath(fp)
		}
	case "Edit":
		if fp, ok := parsed["file_path"].(string); ok {
			return shortenPath(fp)
		}
	case "Glob":
		if p, ok := parsed["pattern"].(string); ok {
			return p
		}
	case "Grep":
		if p, ok := parsed["pattern"].(string); ok {
			if len(p) > 30 {
				p = p[:30] + "..."
			}
			return fmt.Sprintf("/%s/", p)
		}
	case "Bash":
		if cmd, ok := parsed["command"].(string); ok {
			if len(cmd) > 50 {
				cmd = cmd[:50] + "..."
			}
			return cmd
		}
	}
	return ""
}

// shortenPath shortens a file path for display
func shortenPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) <= 3 {
		return path
	}
	return ".../" + strings.Join(parts[len(parts)-2:], "/")
}

// ToolResult prints a tool result
func (o *Output) ToolResult(name string, result string, isError bool) {
	if isError {
		red := color.New(color.FgRed)
		red.Fprintf(o.writer, "  ✗ ")
		// Show error message
		lines := strings.Split(result, "\n")
		if len(lines) > 0 {
			errLine := strings.TrimSpace(lines[0])
			if len(errLine) > 80 {
				errLine = errLine[:80] + "..."
			}
			red.Fprintln(o.writer, errLine)
		}
		return
	}

	// Show success indicator and brief result
	green := color.New(color.FgGreen)
	gray := color.New(color.FgHiBlack)

	green.Fprintf(o.writer, "  ✓ ")

	// Show a brief summary of the result
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) == 1 && len(lines[0]) < 80 {
		gray.Fprintln(o.writer, lines[0])
	} else if len(lines) > 0 {
		// Show line count or first line
		if len(lines) > 3 {
			gray.Fprintf(o.writer, "(%d lines)\n", len(lines))
		} else {
			for _, line := range lines {
				if len(line) > 80 {
					line = line[:80] + "..."
				}
				gray.Fprintln(o.writer, line)
			}
		}
	}
}

// Error prints an error message
func (o *Output) Error(format string, args ...any) {
	c := color.New(color.FgRed)
	c.Fprintf(o.writer, "Error: "+format+"\n", args...)
}

// Warning prints a warning message
func (o *Output) Warning(format string, args ...any) {
	c := color.New(color.FgYellow)
	c.Fprintf(o.writer, "Warning: "+format+"\n", args...)
}

// Success prints a success message
func (o *Output) Success(format string, args ...any) {
	c := color.New(color.FgGreen)
	c.Fprintf(o.writer, format+"\n", args...)
}

// Info prints an info message
func (o *Output) Info(format string, args ...any) {
	c := color.New(color.FgBlue)
	c.Fprintf(o.writer, format+"\n", args...)
}

// Muted prints muted/gray text
func (o *Output) Muted(format string, args ...any) {
	c := color.New(color.FgHiBlack)
	c.Fprintf(o.writer, format+"\n", args...)
}

// StreamToken prints a single token during streaming
func (o *Output) StreamToken(token string) {
	fmt.Fprint(o.writer, token)
}

// StreamEnd ends a streaming output
func (o *Output) StreamEnd() {
	fmt.Fprintln(o.writer)
}
