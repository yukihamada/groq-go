package logging

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"
)

// Level represents the log level
type Level int

const (
	DEBUG Level = iota
	INFO
	WARN
	ERROR
)

func (l Level) String() string {
	switch l {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// Entry represents a structured log entry
type Entry struct {
	Timestamp string         `json:"timestamp"`
	Level     string         `json:"level"`
	Component string         `json:"component,omitempty"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
	Caller    string         `json:"caller,omitempty"`
}

// Logger is a structured logger
type Logger struct {
	mu        sync.Mutex
	out       io.Writer
	level     Level
	component string
	format    Format
}

// Format defines the output format
type Format int

const (
	FormatJSON Format = iota
	FormatText
)

var (
	defaultLogger *Logger
	once          sync.Once
)

// Default returns the default logger
func Default() *Logger {
	once.Do(func() {
		format := FormatJSON
		if os.Getenv("LOG_FORMAT") == "text" {
			format = FormatText
		}
		level := INFO
		switch strings.ToUpper(os.Getenv("LOG_LEVEL")) {
		case "DEBUG":
			level = DEBUG
		case "WARN":
			level = WARN
		case "ERROR":
			level = ERROR
		}
		defaultLogger = New(os.Stdout, level, "", format)
	})
	return defaultLogger
}

// New creates a new Logger
func New(out io.Writer, level Level, component string, format Format) *Logger {
	return &Logger{
		out:       out,
		level:     level,
		component: component,
		format:    format,
	}
}

// WithComponent creates a sub-logger with a component name
func (l *Logger) WithComponent(component string) *Logger {
	return &Logger{
		out:       l.out,
		level:     l.level,
		component: component,
		format:    l.format,
	}
}

// SetLevel sets the minimum log level
func (l *Logger) SetLevel(level Level) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.level = level
}

// SetFormat sets the output format
func (l *Logger) SetFormat(format Format) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.format = format
}

// log writes a log entry
func (l *Logger) log(level Level, msg string, fields map[string]any) {
	if level < l.level {
		return
	}

	entry := Entry{
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Level:     level.String(),
		Component: l.component,
		Message:   msg,
		Fields:    fields,
	}

	// Add caller info for errors
	if level >= ERROR {
		if _, file, line, ok := runtime.Caller(2); ok {
			// Shorten file path
			parts := strings.Split(file, "/")
			if len(parts) > 2 {
				file = strings.Join(parts[len(parts)-2:], "/")
			}
			entry.Caller = fmt.Sprintf("%s:%d", file, line)
		}
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.format == FormatJSON {
		data, _ := json.Marshal(entry)
		fmt.Fprintln(l.out, string(data))
	} else {
		l.writeText(entry)
	}
}

func (l *Logger) writeText(e Entry) {
	// Format: [LEVEL] [component] message key=value ...
	ts := e.Timestamp[:19] // Trim to second precision
	var sb strings.Builder
	sb.WriteString(ts)
	sb.WriteString(" [")
	sb.WriteString(e.Level)
	sb.WriteString("]")

	if e.Component != "" {
		sb.WriteString(" [")
		sb.WriteString(e.Component)
		sb.WriteString("]")
	}

	sb.WriteString(" ")
	sb.WriteString(e.Message)

	for k, v := range e.Fields {
		sb.WriteString(" ")
		sb.WriteString(k)
		sb.WriteString("=")
		sb.WriteString(fmt.Sprintf("%v", v))
	}

	if e.Caller != "" {
		sb.WriteString(" caller=")
		sb.WriteString(e.Caller)
	}

	fmt.Fprintln(l.out, sb.String())
}

// Debug logs a debug message
func (l *Logger) Debug(msg string, fields ...any) {
	l.log(DEBUG, msg, toFields(fields))
}

// Info logs an info message
func (l *Logger) Info(msg string, fields ...any) {
	l.log(INFO, msg, toFields(fields))
}

// Warn logs a warning message
func (l *Logger) Warn(msg string, fields ...any) {
	l.log(WARN, msg, toFields(fields))
}

// Error logs an error message
func (l *Logger) Error(msg string, fields ...any) {
	l.log(ERROR, msg, toFields(fields))
}

// toFields converts variadic key-value pairs to a map
func toFields(args []any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	fields := make(map[string]any)
	for i := 0; i < len(args)-1; i += 2 {
		if key, ok := args[i].(string); ok {
			fields[key] = args[i+1]
		}
	}
	return fields
}

// Package-level convenience functions

// Debug logs a debug message
func Debug(msg string, fields ...any) {
	Default().Debug(msg, fields...)
}

// Info logs an info message
func Info(msg string, fields ...any) {
	Default().Info(msg, fields...)
}

// Warn logs a warning message
func Warn(msg string, fields ...any) {
	Default().Warn(msg, fields...)
}

// Error logs an error message
func Error(msg string, fields ...any) {
	Default().Error(msg, fields...)
}

// WithComponent returns a logger with the given component name
func WithComponent(component string) *Logger {
	return Default().WithComponent(component)
}
