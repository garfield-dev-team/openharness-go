package hitl

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// CLIAdapter handles HITL interactions via stdin/stdout.
type CLIAdapter struct {
	reader io.Reader
	writer io.Writer
}

func NewCLIAdapter(r io.Reader, w io.Writer) *CLIAdapter {
	return &CLIAdapter{reader: r, writer: w}
}

// AskUser implements tools.AskUserFunc for CLI environments.
func (a *CLIAdapter) AskUser(ctx context.Context, question string, options []string) (string, error) {
	fmt.Fprintf(a.writer, "\n╭─ Question from assistant ─────────────────────────\n")
	fmt.Fprintf(a.writer, "│ %s\n", question)

	if len(options) > 0 {
		fmt.Fprintf(a.writer, "│\n")
		for i, opt := range options {
			fmt.Fprintf(a.writer, "│  [%d] %s\n", i+1, opt)
		}
		fmt.Fprintf(a.writer, "╰──────────────────────────────────────────────────\n")
		fmt.Fprintf(a.writer, "Enter choice (1-%d) or type your answer: ", len(options))
	} else {
		fmt.Fprintf(a.writer, "╰──────────────────────────────────────────────────\n")
		fmt.Fprintf(a.writer, "Your answer: ")
	}

	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(a.reader)
		if scanner.Scan() {
			lineCh <- scanner.Text()
		} else {
			if err := scanner.Err(); err != nil {
				errCh <- err
			} else {
				errCh <- io.EOF
			}
		}
	}()

	select {
	case line := <-lineCh:
		answer := strings.TrimSpace(line)
		if len(options) > 0 {
			if idx, err := strconv.Atoi(answer); err == nil && idx >= 1 && idx <= len(options) {
				return options[idx-1], nil
			}
		}
		return answer, nil
	case err := <-errCh:
		return "", fmt.Errorf("failed to read user input: %w", err)
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// AskPermission implements tools.AskPermissionFunc for CLI environments.
func (a *CLIAdapter) AskPermission(ctx context.Context, toolName, reason string) (bool, error) {
	fmt.Fprintf(a.writer, "\n╭─ Permission request ──────────────────────────────\n")
	fmt.Fprintf(a.writer, "│ Tool: %s\n", toolName)
	fmt.Fprintf(a.writer, "│ Reason: %s\n", reason)
	fmt.Fprintf(a.writer, "╰──────────────────────────────────────────────────\n")
	fmt.Fprintf(a.writer, "Allow? (y/n): ")

	lineCh := make(chan string, 1)
	errCh := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(a.reader)
		if scanner.Scan() {
			lineCh <- scanner.Text()
		} else {
			if err := scanner.Err(); err != nil {
				errCh <- err
			} else {
				errCh <- io.EOF
			}
		}
	}()

	select {
	case line := <-lineCh:
		answer := strings.TrimSpace(strings.ToLower(line))
		return answer == "y" || answer == "yes", nil
	case err := <-errCh:
		return false, fmt.Errorf("failed to read user input: %w", err)
	case <-ctx.Done():
		return false, ctx.Err()
	}
}