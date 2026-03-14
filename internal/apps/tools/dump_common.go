package tools

import (
	"context"
	"io"
	"os"

	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
)

type dumpRunFunc func(
	ctx context.Context,
	workingDir string,
	command []string,
	jsonOutput bool,
	logLevel zerolog.Level,
	stdout io.Writer,
	stderr io.Writer,
) error

// DumpRunFunc executes a dump inspection against a command.
type DumpRunFunc = dumpRunFunc

// DumpDeps customizes dump command runtime dependencies.
type DumpDeps struct {
	RunInspector DumpRunFunc
}

func resolveDumpRunFunc(deps DumpDeps, fallback DumpRunFunc) dumpRunFunc {
	if deps.RunInspector != nil {
		return deps.RunInspector
	}
	return fallback
}

func newDumpCommand(
	runtime RuntimeConfig,
	use string,
	short string,
	long string,
	example string,
	requireCommand func(cmd *cobra.Command, args []string) ([]string, error),
	run dumpRunFunc,
) *cobra.Command {
	jsonOutput := false
	debugLogs := false
	cmd := &cobra.Command{
		Use:          use,
		Short:        short,
		Long:         long,
		Example:      example,
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			serverCommand, err := requireCommand(cmd, args)
			if err != nil {
				return err
			}

			workingDir, err := os.Getwd()
			if err != nil {
				return err
			}
			logLevel := runtime.resolveLogLevel(zerolog.ErrorLevel, debugLogs)
			return run(
				cmd.Context(),
				workingDir,
				serverCommand,
				jsonOutput,
				logLevel,
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)
		},
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print output as JSON")
	if runtime.IncludeDebugFlag {
		cmd.Flags().BoolVar(&debugLogs, "debug", false, "enable debug logging")
	}
	return cmd
}
