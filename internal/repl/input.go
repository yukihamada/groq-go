package repl

import (
	"bufio"
	"io"
	"os"
	"strings"

	"github.com/chzyer/readline"
)

// Input handles user input with readline support
type Input struct {
	rl       *readline.Instance
	isPiped  bool
	scanner  *bufio.Scanner
}

// NewInput creates a new input handler
func NewInput() (*Input, error) {
	// Check if stdin is a pipe
	stat, _ := os.Stdin.Stat()
	isPiped := (stat.Mode() & os.ModeCharDevice) == 0

	if isPiped {
		// Use simple scanner for piped input
		return &Input{
			isPiped: true,
			scanner: bufio.NewScanner(os.Stdin),
		}, nil
	}

	// Use readline for interactive input
	rl, err := readline.NewEx(&readline.Config{
		Prompt:            "> ",
		HistoryFile:       "",
		InterruptPrompt:   "^C",
		EOFPrompt:         "exit",
		HistorySearchFold: true,
	})
	if err != nil {
		return nil, err
	}

	return &Input{rl: rl, isPiped: false}, nil
}

// ReadLine reads a line of input from the user
func (i *Input) ReadLine() (string, error) {
	if i.isPiped {
		if i.scanner.Scan() {
			return strings.TrimSpace(i.scanner.Text()), nil
		}
		if err := i.scanner.Err(); err != nil {
			return "", err
		}
		return "", io.EOF
	}

	line, err := i.rl.Readline()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

// SetPrompt changes the prompt
func (i *Input) SetPrompt(prompt string) {
	if i.rl != nil {
		i.rl.SetPrompt(prompt)
	}
}

// Close closes the readline instance
func (i *Input) Close() error {
	if i.rl != nil {
		return i.rl.Close()
	}
	return nil
}

// IsPiped returns true if input is from a pipe
func (i *Input) IsPiped() bool {
	return i.isPiped
}

// IsInterrupt checks if the error is an interrupt (Ctrl+C)
func IsInterrupt(err error) bool {
	return err == readline.ErrInterrupt
}

// IsEOF checks if the error is EOF (Ctrl+D)
func IsEOF(err error) bool {
	return err == io.EOF
}
