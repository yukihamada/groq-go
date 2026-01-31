package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"groq-go/internal/tool"
)

// CodeExecTool executes code in a sandboxed environment
type CodeExecTool struct{}

func NewCodeExecTool() *CodeExecTool {
	return &CodeExecTool{}
}

func (t *CodeExecTool) Name() string {
	return "CodeExec"
}

func (t *CodeExecTool) Description() string {
	return "Execute code in a sandboxed environment. Supports JavaScript (Node.js), Python, Go, and shell scripts. Use for testing code snippets, running calculations, or executing simple programs."
}

func (t *CodeExecTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"language": map[string]any{
				"type":        "string",
				"description": "Programming language: 'javascript', 'python', 'go', or 'shell'",
				"enum":        []string{"javascript", "python", "go", "shell"},
			},
			"code": map[string]any{
				"type":        "string",
				"description": "The code to execute",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Maximum execution time in seconds (default: 10, max: 30)",
			},
		},
		"required": []string{"language", "code"},
	}
}

func (t *CodeExecTool) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	var params struct {
		Language string `json:"language"`
		Code     string `json:"code"`
		Timeout  int    `json:"timeout"`
	}

	if err := json.Unmarshal(args, &params); err != nil {
		return tool.Result{Content: err.Error(), IsError: true}, nil
	}

	// Validate language
	validLangs := map[string]bool{"javascript": true, "python": true, "go": true, "shell": true}
	if !validLangs[params.Language] {
		return tool.Result{Content: "Unsupported language: " + params.Language, IsError: true}, nil
	}

	// Set timeout (default 10s, max 30s)
	timeout := params.Timeout
	if timeout <= 0 {
		timeout = 10
	}
	if timeout > 30 {
		timeout = 30
	}

	// Create temp directory for execution
	tmpDir, err := os.MkdirTemp("", "codeexec-")
	if err != nil {
		return tool.Result{Content: "Failed to create temp directory: " + err.Error(), IsError: true}, nil
	}
	defer os.RemoveAll(tmpDir)

	var result string
	var execErr error

	switch params.Language {
	case "javascript":
		result, execErr = executeJavaScript(ctx, tmpDir, params.Code, timeout)
	case "python":
		result, execErr = executePython(ctx, tmpDir, params.Code, timeout)
	case "go":
		result, execErr = executeGo(ctx, tmpDir, params.Code, timeout)
	case "shell":
		result, execErr = executeShell(ctx, tmpDir, params.Code, timeout)
	}

	if execErr != nil {
		return tool.Result{Content: result + "\nError: " + execErr.Error(), IsError: true}, nil
	}

	return tool.Result{Content: result}, nil
}

func executeJavaScript(ctx context.Context, dir, code string, timeout int) (string, error) {
	// Write code to file
	filePath := filepath.Join(dir, "script.js")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		return "", err
	}

	// Check if node is available
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return "", fmt.Errorf("Node.js not installed")
	}

	return runCommand(ctx, dir, nodePath, []string{filePath}, timeout)
}

func executePython(ctx context.Context, dir, code string, timeout int) (string, error) {
	// Write code to file
	filePath := filepath.Join(dir, "script.py")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		return "", err
	}

	// Try python3 first, then python
	pythonPath, err := exec.LookPath("python3")
	if err != nil {
		pythonPath, err = exec.LookPath("python")
		if err != nil {
			return "", fmt.Errorf("Python not installed")
		}
	}

	return runCommand(ctx, dir, pythonPath, []string{filePath}, timeout)
}

func executeGo(ctx context.Context, dir, code string, timeout int) (string, error) {
	// Wrap code in main package if needed
	if !strings.Contains(code, "package ") {
		code = "package main\n\n" + code
	}
	if !strings.Contains(code, "func main()") {
		code = code + "\n\nfunc main() {}"
	}

	// Write code to file
	filePath := filepath.Join(dir, "main.go")
	if err := os.WriteFile(filePath, []byte(code), 0644); err != nil {
		return "", err
	}

	// Check if go is available
	goPath, err := exec.LookPath("go")
	if err != nil {
		return "", fmt.Errorf("Go not installed")
	}

	return runCommand(ctx, dir, goPath, []string{"run", filePath}, timeout)
}

func executeShell(ctx context.Context, dir, code string, timeout int) (string, error) {
	// Security: restrict dangerous commands
	dangerous := []string{"rm -rf", "sudo", "chmod", "chown", "mkfs", "dd if=", "> /dev/", "curl", "wget", "nc ", "netcat"}
	codeLower := strings.ToLower(code)
	for _, d := range dangerous {
		if strings.Contains(codeLower, d) {
			return "", fmt.Errorf("command not allowed for security reasons: %s", d)
		}
	}

	// Write code to file
	filePath := filepath.Join(dir, "script.sh")
	if err := os.WriteFile(filePath, []byte("#!/bin/bash\nset -e\n"+code), 0755); err != nil {
		return "", err
	}

	return runCommand(ctx, dir, "/bin/bash", []string{filePath}, timeout)
}

func runCommand(ctx context.Context, dir, command string, args []string, timeout int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir

	// Restrict environment
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + dir,
		"TMPDIR=" + dir,
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	output := stdout.String()
	if stderr.Len() > 0 {
		if output != "" {
			output += "\n--- stderr ---\n"
		}
		output += stderr.String()
	}

	// Trim output if too long
	const maxOutput = 10000
	if len(output) > maxOutput {
		output = output[:maxOutput] + "\n... (output truncated)"
	}

	if ctx.Err() == context.DeadlineExceeded {
		return output, fmt.Errorf("execution timed out after %d seconds", timeout)
	}

	return output, err
}
