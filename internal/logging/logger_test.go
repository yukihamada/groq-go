package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, DEBUG, "test", FormatJSON)

	logger.Info("test message", "key1", "value1", "key2", 42)

	var entry Entry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON log: %v", err)
	}

	if entry.Level != "INFO" {
		t.Errorf("Expected level INFO, got %s", entry.Level)
	}
	if entry.Component != "test" {
		t.Errorf("Expected component test, got %s", entry.Component)
	}
	if entry.Message != "test message" {
		t.Errorf("Expected message 'test message', got %s", entry.Message)
	}
	if entry.Fields["key1"] != "value1" {
		t.Errorf("Expected key1=value1, got %v", entry.Fields["key1"])
	}
	if entry.Fields["key2"] != float64(42) { // JSON numbers are float64
		t.Errorf("Expected key2=42, got %v", entry.Fields["key2"])
	}
}

func TestTextFormat(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, DEBUG, "test", FormatText)

	logger.Warn("warning message", "code", 500)

	output := buf.String()
	if !strings.Contains(output, "[WARN]") {
		t.Errorf("Expected [WARN] in output, got: %s", output)
	}
	if !strings.Contains(output, "[test]") {
		t.Errorf("Expected [test] in output, got: %s", output)
	}
	if !strings.Contains(output, "warning message") {
		t.Errorf("Expected 'warning message' in output, got: %s", output)
	}
	if !strings.Contains(output, "code=500") {
		t.Errorf("Expected 'code=500' in output, got: %s", output)
	}
}

func TestLogLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, WARN, "test", FormatJSON)

	logger.Debug("should not appear")
	logger.Info("should not appear")
	logger.Warn("should appear")
	logger.Error("should appear")

	output := buf.String()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Errorf("Expected 2 log lines, got %d: %s", len(lines), output)
	}
}

func TestErrorIncludesCaller(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, DEBUG, "test", FormatJSON)

	logger.Error("error message")

	var entry Entry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON log: %v", err)
	}

	if entry.Caller == "" {
		t.Error("Expected caller info for error log")
	}
	if !strings.Contains(entry.Caller, "logger_test.go") {
		t.Errorf("Expected caller to contain logger_test.go, got: %s", entry.Caller)
	}
}

func TestWithComponent(t *testing.T) {
	var buf bytes.Buffer
	logger := New(&buf, INFO, "parent", FormatJSON)

	child := logger.WithComponent("child")
	child.Info("child message")

	var entry Entry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to parse JSON log: %v", err)
	}

	if entry.Component != "child" {
		t.Errorf("Expected component 'child', got %s", entry.Component)
	}
}
