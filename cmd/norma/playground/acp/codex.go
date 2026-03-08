package acpcmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

type CodexOptions struct {
	Prompt    string
	CodexArgs []string

	BridgeBin string
}

func CodexCommand() *cobra.Command {
	opts := CodexOptions{}
	return newACPPlaygroundCommand(
		"codex",
		"Run Codex MCP server through ACP bridge and Go ADK",
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(&opts.Prompt, "prompt", "", "single prompt to run; if empty starts a REPL")
			cmd.Flags().StringArrayVar(&opts.CodexArgs, "codex-arg", nil, "extra Codex mcp-server argument (repeatable)")
		},
		func(ctx context.Context, repoRoot string, stdin io.Reader, stdout, stderr io.Writer) error {
			return RunCodexACP(ctx, repoRoot, opts, stdin, stdout, stderr)
		},
	)
}

func CodexInfoCommand() *cobra.Command {
	opts := CodexOptions{}
	return newACPInfoCommand(
		"codex",
		"Inspect Codex ACP bridge capabilities and auth methods",
		func(cmd *cobra.Command) {
			cmd.Flags().StringArrayVar(&opts.CodexArgs, "codex-arg", nil, "extra Codex mcp-server argument (repeatable)")
			cmd.Flags().StringVar(&opts.BridgeBin, "bridge-bin", "", "Codex ACP bridge executable path (defaults to current norma binary)")
		},
		func(ctx context.Context, repoRoot string, jsonOutput bool, stdout io.Writer, stderr io.Writer) error {
			return RunCodexACPInfo(ctx, repoRoot, opts, jsonOutput, stdout, stderr)
		},
	)
}

func RunCodexACP(ctx context.Context, repoRoot string, opts CodexOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	acpCmd, err := BuildCodexACPCommand(opts)
	if err != nil {
		return err
	}
	return runStandardACP(ctx, repoRoot, opts.Prompt, acpCmd, runtimeSpec{
		component:   "playground.codex_acp",
		name:        "CodexACP",
		description: "Codex MCP server via ACP bridge",
		startMsg:    "starting Codex ACP playground",
	}, stdin, stdout, stderr)
}

func BuildCodexACPCommand(opts CodexOptions) ([]string, error) {
	bridgeBin := strings.TrimSpace(opts.BridgeBin)
	if bridgeBin == "" {
		var err error
		bridgeBin, err = os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve executable path: %w", err)
		}
	}

	cmd := []string{bridgeBin, "playground", "codex-acp-bridge"}
	for _, arg := range opts.CodexArgs {
		cmd = append(cmd, "--codex-arg", arg)
	}
	return cmd, nil
}

func RunCodexACPInfo(
	ctx context.Context,
	repoRoot string,
	opts CodexOptions,
	jsonOutput bool,
	stdout io.Writer,
	stderr io.Writer,
) error {
	acpCmd, err := BuildCodexACPCommand(opts)
	if err != nil {
		return err
	}
	return runACPInfo(
		ctx,
		repoRoot,
		acpCmd,
		"playground.codex_acp_info",
		"inspecting Codex ACP bridge",
		jsonOutput,
		stdout,
		stderr,
	)
}
