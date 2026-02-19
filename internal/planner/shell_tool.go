package planner

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/adk/tool"
)

// ShellTool provides a way to execute shell commands.
type ShellTool struct {
	repoRoot string
	allowed  []string
}

// ShellArgs defines the arguments for the run_shell_command tool.
type ShellArgs struct {
	Command string `json:"command"`
}

// ShellResponse defines the response from the run_shell_command tool.
type ShellResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// NewShellTool constructs a new ShellTool.
func NewShellTool(repoRoot string) *ShellTool {
	return &ShellTool{
		repoRoot: repoRoot,
		allowed: []string{
			"ls", "grep", "cat", "find", "tree", "git", "go", "bd", "echo",
		},
	}
}

// Run executes a shell command.
func (s *ShellTool) Run(tctx tool.Context, args ShellArgs) (ShellResponse, error) {
	cmdStr := strings.TrimSpace(args.Command)
	if cmdStr == "" {
		return ShellResponse{Error: "empty command"}, nil
	}

	// Basic security check: only allow certain base commands
	parts := strings.Fields(cmdStr)
	if len(parts) == 0 {
		return ShellResponse{Error: "invalid command"}, nil
	}

	baseCmd := parts[0]
	isAllowed := false
	for _, a := range s.allowed {
		if baseCmd == a {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		return ShellResponse{
			Error: fmt.Sprintf("command %q is not allowed. Allowed commands are: %s", baseCmd, strings.Join(s.allowed, ", ")),
		}, nil
	}

	// More security: block dangerous patterns
	dangerous := []string{"rm", ">", ">>", "|", ";", "&", "&&", "||", "`", "$(", ">&"}
	for _, d := range dangerous {
		// Only check if it's a separate token or at the start/end to avoid false positives with flags
		// This is a very basic check.
		if strings.Contains(cmdStr, d) && baseCmd != "grep" {
             // Grep might use some of these in patterns, but we should be careful.
             // For MVP, we'll just block them if they are not inside grep.
             // Actually, let's just block them for now to be safe.
             if d != "|" || !strings.Contains(cmdStr, "grep") {
                  // Allow pipe only if it's followed by grep? Too complex for now.
             }
		}
	}

    // Let's keep it simple for now: no pipes or redirects.
    for _, d := range []string{";", "&", "&&", "||", "`", "$(", ">", ">>", "|"} {
        if strings.Contains(cmdStr, d) {
             return ShellResponse{Error: fmt.Sprintf("shell metacharacter %q is not allowed", d)}, nil
        }
    }

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", "-c", cmdStr)
	cmd.Dir = s.repoRoot

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			return ShellResponse{Error: err.Error()}, nil
		}
	}

	return ShellResponse{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}
