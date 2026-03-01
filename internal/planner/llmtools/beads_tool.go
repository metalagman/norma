package llmtools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
)

const (
	BeadsToolName        = "beads"
	BeadsToolDescription = "Interact with the Beads issue tracker. Operations: list, show, create, update, close, reopen, delete, ready. Always use --sandbox and --json."
)

// BeadsArgs defines the arguments for the beads tool.
type BeadsArgs struct {
	Op   string   `json:"op"`
	Args []string `json:"args,omitempty"`
}

// BeadsResponse defines the response from the beads tool.
type BeadsResponse struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

// BeadsTool provides structured access to the beads CLI.
type BeadsTool struct {
	repoRoot string
}

// NewBeadsTool constructs a new BeadsTool.
func NewBeadsTool(repoRoot string) *BeadsTool {
	return &BeadsTool{repoRoot: repoRoot}
}

// NewBeadsCommandTool creates the planner beads tool.
func NewBeadsCommandTool(repoRoot string) (tool.Tool, error) {
	bt := NewBeadsTool(repoRoot)
	return functiontool.New(functiontool.Config{
		Name:        BeadsToolName,
		Description: BeadsToolDescription,
	}, bt.Run)
}

// Run executes a beads command.
func (b *BeadsTool) Run(tctx tool.Context, args BeadsArgs) (BeadsResponse, error) {
	allowedOps := map[string]bool{
		"list":   true,
		"show":   true,
		"create": true,
		"update": true,
		"close":  true,
		"reopen": true,
		"delete": true,
		"ready":  true,
	}

	if !allowedOps[args.Op] {
		return BeadsResponse{
			Error: fmt.Sprintf("unsupported operation: %s", args.Op),
		}, nil
	}

	// Enforce reason for state-changing ops
	if args.Op == "close" || args.Op == "reopen" || args.Op == "delete" {
		hasReason := false
		for i, arg := range args.Args {
			if arg == "--reason" || arg == "-r" {
				if i+1 < len(args.Args) && args.Args[i+1] != "" {
					hasReason = true
					break
				}
			}
			if strings.HasPrefix(arg, "--reason=") && len(arg) > 9 {
				hasReason = true
				break
			}
		}
		if !hasReason {
			return BeadsResponse{
				Error: fmt.Sprintf("operation %s requires a non-empty --reason", args.Op),
			}, nil
		}
	}

	// Prepare command arguments
	cmdArgs := make([]string, 0, 3+len(args.Args))
	cmdArgs = append(cmdArgs, args.Op, "--sandbox", "--json")
	cmdArgs = append(cmdArgs, args.Args...)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bd", cmdArgs...)
	cmd.Dir = b.repoRoot
	cmd.Env = os.Environ()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			return BeadsResponse{Error: err.Error()}, nil
		}
	}

	return BeadsResponse{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
	}, nil
}
