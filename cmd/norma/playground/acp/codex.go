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
	Prompt string
	Model  string
	Name   string

	BridgeBin string
}

func CodexCommand() *cobra.Command {
	opts := CodexOptions{}
	return newACPPlaygroundCommand(
		"codex",
		"Run Codex MCP server through ACP proxy and Go ADK",
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(&opts.Prompt, "prompt", "", "single prompt to run; if empty starts a REPL")
			cmd.Flags().StringVar(&opts.Model, "model", "", "Codex model name")
			cmd.Flags().StringVar(&opts.Name, "name", "", "ACP agent name exposed by the Codex proxy")
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
		"Inspect Codex ACP proxy capabilities and auth methods",
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(&opts.Model, "model", "", "Codex model name")
			cmd.Flags().StringVar(&opts.Name, "name", "", "ACP agent name exposed by the Codex proxy")
			cmd.Flags().StringVar(&opts.BridgeBin, "bridge-bin", "", "Codex ACP proxy executable path (defaults to current norma binary)")
		},
		func(ctx context.Context, repoRoot string, jsonOutput bool, stdout io.Writer, stderr io.Writer) error {
			return RunCodexACPInfo(ctx, repoRoot, opts, jsonOutput, stdout, stderr)
		},
	)
}

func CodexWebCommand() *cobra.Command {
	opts := CodexOptions{}
	return newACPWebCommand(
		"codex [-- <web launcher args...>]",
		"Run Codex ACP proxy with the ADK web launcher",
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(&opts.Model, "model", "", "Codex model name")
			cmd.Flags().StringVar(&opts.Name, "name", "", "ACP agent name exposed by the Codex proxy")
			cmd.Flags().StringVar(&opts.BridgeBin, "bridge-bin", "", "Codex ACP proxy executable path (defaults to current norma binary)")
		},
		func(ctx context.Context, repoRoot string, launcherArgs []string, stderr io.Writer) error {
			return RunCodexACPWeb(ctx, repoRoot, opts, launcherArgs, stderr)
		},
	)
}

func RunCodexACP(ctx context.Context, repoRoot string, opts CodexOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	acpCmd, err := BuildCodexACPCommand(opts)
	if err != nil {
		return err
	}
	return runStandardACP(ctx, repoRoot, opts.Prompt, acpCmd, opts.Model, runtimeSpec{
		component:   "playground.codex_acp",
		name:        "CodexACP",
		description: "Codex MCP server via ACP proxy",
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

	cmd := []string{bridgeBin, "--debug", "proxy", "codex-acp"}
	if strings.TrimSpace(opts.Model) != "" {
		cmd = append(cmd, "--codex-model", strings.TrimSpace(opts.Model))
	}
	if strings.TrimSpace(opts.Name) != "" {
		cmd = append(cmd, "--name", strings.TrimSpace(opts.Name))
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
		opts.Model,
		"playground.codex_acp_info",
		"inspecting Codex ACP proxy",
		jsonOutput,
		stdout,
		stderr,
	)
}

func RunCodexACPWeb(
	ctx context.Context,
	repoRoot string,
	opts CodexOptions,
	launcherArgs []string,
	stderr io.Writer,
) error {
	acpCmd, err := BuildCodexACPCommand(opts)
	if err != nil {
		return err
	}
	return runACPWeb(ctx, repoRoot, acpCmd, opts.Model, runtimeSpec{
		component:   "playground.codex_acp_web",
		name:        "CodexACPWeb",
		description: "Codex MCP server via ACP proxy (web launcher)",
		startMsg:    "starting Codex ACP web launcher",
	}, launcherArgs, stderr)
}
