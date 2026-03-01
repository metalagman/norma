package llmtools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

// ShellTool provides a way to execute shell commands.
type ShellTool struct {
	repoRoot string
	allowed  []string
}

// ShellArgs defines the arguments for the run_shell_command tool.
type ShellArgs struct {
	Command      string `json:"command"`
	Cmd          string `json:"cmd,omitempty"`
	Input        string `json:"input,omitempty"`
	ShellCommand string `json:"shell_command,omitempty"`
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
			"ls", "grep", "cat", "find", "tree", "git", "go", "echo",
		},
	}
}

// NewShellCommandTool creates the planner run_shell_command tool.
func NewShellCommandTool(repoRoot string) (tool.Tool, error) {
	shell := NewShellTool(repoRoot)
	return functiontool.New(functiontool.Config{
		Name:        ShellToolName,
		Description: ShellToolDescription,
	}, shell.Run)
}

// Run executes a shell command.
func (s *ShellTool) Run(tctx tool.Context, args ShellArgs) (ShellResponse, error) {
	cmdStr := normalizeShellCommandArg(args)
	if cmdStr == "" {
		return ShellResponse{Error: "empty command"}, nil
	}

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

	// Keep it simple for MVP: no shell metacharacters or command chaining.
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

func normalizeShellCommandArg(args ShellArgs) string {
	candidates := []string{args.Command, args.Cmd, args.Input, args.ShellCommand}
	for _, candidate := range candidates {
		cmd := strings.TrimSpace(candidate)
		cmd = trimMatchingQuotes(cmd)
		if cmd != "" {
			return cmd
		}
	}
	return ""
}

func trimMatchingQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
		return s[1 : len(s)-1]
	}
	return s
}
