package mcpcmd

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/normahq/norma/v2/internal/apps/tasksmcp"
	"github.com/normahq/norma/v2/internal/task"
	"github.com/spf13/cobra"
)

var (
	runTasksServer     = tasksmcp.Run
	runTasksServerHTTP = tasksmcp.RunHTTP
	newTracker         = func(workingDir string) task.Tracker {
		tracker := task.NewBeadsTracker("")
		tracker.WorkingDir = workingDir
		return tracker
	}
)

// TasksCommand runs the tracker-parity tasks MCP server.
// By default it runs over stdio; use --http to run over HTTP.
func TasksCommand() *cobra.Command {
	var (
		workingDir string
		httpMode   bool
		httpAddr   string
	)

	cmd := &cobra.Command{
		Use:          "tasks",
		Short:        "Run norma task tracker MCP server",
		SilenceUsage: true,
		Args:         cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolvedWorkingDir := strings.TrimSpace(workingDir)
			if resolvedWorkingDir == "" {
				cwd, err := os.Getwd()
				if err != nil {
					return fmt.Errorf("resolve current working directory: %w", err)
				}
				resolvedWorkingDir = cwd
			}

			absoluteWorkingDir, err := filepath.Abs(resolvedWorkingDir)
			if err != nil {
				return fmt.Errorf("resolve absolute working directory %q: %w", resolvedWorkingDir, err)
			}

			tracker := newTracker(absoluteWorkingDir)

			if httpMode {
				addr := strings.TrimSpace(httpAddr)
				if addr == "" {
					addr = "localhost:8080"
				}
				return runTasksServerHTTP(cmd.Context(), tracker, addr)
			}

			return runTasksServer(cmd.Context(), tracker)
		},
	}

	cmd.Flags().StringVar(&workingDir, "working-dir", "", "Working directory for task context resolution (default: current directory)")
	cmd.Flags().BoolVar(&httpMode, "http", false, "Run over HTTP instead of stdio")
	cmd.Flags().StringVar(&httpAddr, "addr", "localhost:8080", "HTTP listen address (host:port)")
	return cmd
}
