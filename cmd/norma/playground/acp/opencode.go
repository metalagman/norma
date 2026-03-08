package acpcmd

import (
	"context"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

type OpenCodeOptions struct {
	Prompt       string
	Model        string
	OpenCodeBin  string
	OpenCodeArgs []string
}

func OpenCodeCommand() *cobra.Command {
	opts := OpenCodeOptions{OpenCodeBin: "opencode"}
	return newModelACPRunCommand(modelACPCommandConfig{
		Use:        "opencode",
		Short:      "Run OpenCode CLI in ACP mode through Go ADK",
		InfoShort:  "Inspect OpenCode ACP agent capabilities and auth methods",
		Prompt:     &opts.Prompt,
		Model:      &opts.Model,
		Binary:     &opts.OpenCodeBin,
		Args:       &opts.OpenCodeArgs,
		ModelHelp:  "OpenCode model name",
		BinaryFlag: "opencode-bin",
		BinaryHelp: "OpenCode executable path",
		ArgsFlag:   "opencode-arg",
		ArgsHelp:   "extra OpenCode ACP argument (repeatable)",
		Run: func(ctx context.Context, repoRoot string, stdin io.Reader, stdout, stderr io.Writer) error {
			return RunOpenCodeACP(ctx, repoRoot, opts, stdin, stdout, stderr)
		},
	})
}

func OpenCodeInfoCommand() *cobra.Command {
	opts := OpenCodeOptions{OpenCodeBin: "opencode"}
	return newModelACPInfoCommand(modelACPCommandConfig{
		Use:        "opencode",
		Short:      "Run OpenCode CLI in ACP mode through Go ADK",
		InfoShort:  "Inspect OpenCode ACP agent capabilities and auth methods",
		Prompt:     &opts.Prompt,
		Model:      &opts.Model,
		Binary:     &opts.OpenCodeBin,
		Args:       &opts.OpenCodeArgs,
		ModelHelp:  "OpenCode model name",
		BinaryFlag: "opencode-bin",
		BinaryHelp: "OpenCode executable path",
		ArgsFlag:   "opencode-arg",
		ArgsHelp:   "extra OpenCode ACP argument (repeatable)",
		RunInfo: func(ctx context.Context, repoRoot string, jsonOutput bool, stdout io.Writer, stderr io.Writer) error {
			return RunOpenCodeACPInfo(ctx, repoRoot, opts, jsonOutput, stdout, stderr)
		},
	})
}

func RunOpenCodeACP(ctx context.Context, repoRoot string, opts OpenCodeOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	return runStandardACP(ctx, repoRoot, opts.Prompt, BuildOpenCodeACPCommand(opts), runtimeSpec{
		component:   "playground.opencode_acp",
		name:        "OpenCodeACP",
		description: "OpenCode CLI playground agent via ACP",
		startMsg:    "starting OpenCode ACP playground",
	}, stdin, stdout, stderr)
}

func BuildOpenCodeACPCommand(opts OpenCodeOptions) []string {
	cmd := []string{opts.OpenCodeBin}
	if strings.TrimSpace(opts.Model) != "" {
		cmd = append(cmd, "--model", opts.Model)
	}
	cmd = append(cmd, "acp")
	cmd = append(cmd, opts.OpenCodeArgs...)
	return cmd
}

func RunOpenCodeACPInfo(
	ctx context.Context,
	repoRoot string,
	opts OpenCodeOptions,
	jsonOutput bool,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runACPInfo(
		ctx,
		repoRoot,
		BuildOpenCodeACPCommand(opts),
		"playground.opencode_acp_info",
		"inspecting OpenCode ACP agent",
		jsonOutput,
		stdout,
		stderr,
	)
}
