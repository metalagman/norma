package acpcmd

import (
	"context"
	"io"
	"strings"

	"github.com/spf13/cobra"
)

type GeminiOptions struct {
	Prompt     string
	Model      string
	GeminiBin  string
	GeminiArgs []string
}

func GeminiCommand() *cobra.Command {
	opts := GeminiOptions{GeminiBin: "gemini"}
	return newModelACPRunCommand(modelACPCommandConfig{
		Use:        "gemini",
		Short:      "Run Gemini CLI in ACP mode through Go ADK",
		InfoShort:  "Inspect Gemini ACP agent capabilities and auth methods",
		Prompt:     &opts.Prompt,
		Model:      &opts.Model,
		Binary:     &opts.GeminiBin,
		Args:       &opts.GeminiArgs,
		ModelHelp:  "Gemini model name",
		BinaryFlag: "gemini-bin",
		BinaryHelp: "Gemini executable path",
		ArgsFlag:   "gemini-arg",
		ArgsHelp:   "extra Gemini CLI argument (repeatable)",
		Run: func(ctx context.Context, repoRoot string, stdin io.Reader, stdout, stderr io.Writer) error {
			return RunGeminiACP(ctx, repoRoot, opts, stdin, stdout, stderr)
		},
	})
}

func GeminiInfoCommand() *cobra.Command {
	opts := GeminiOptions{GeminiBin: "gemini"}
	return newModelACPInfoCommand(modelACPCommandConfig{
		Use:        "gemini",
		Short:      "Run Gemini CLI in ACP mode through Go ADK",
		InfoShort:  "Inspect Gemini ACP agent capabilities and auth methods",
		Prompt:     &opts.Prompt,
		Model:      &opts.Model,
		Binary:     &opts.GeminiBin,
		Args:       &opts.GeminiArgs,
		ModelHelp:  "Gemini model name",
		BinaryFlag: "gemini-bin",
		BinaryHelp: "Gemini executable path",
		ArgsFlag:   "gemini-arg",
		ArgsHelp:   "extra Gemini CLI argument (repeatable)",
		RunInfo: func(ctx context.Context, repoRoot string, jsonOutput bool, stdout io.Writer, stderr io.Writer) error {
			return RunGeminiACPInfo(ctx, repoRoot, opts, jsonOutput, stdout, stderr)
		},
	})
}

func RunGeminiACP(ctx context.Context, repoRoot string, opts GeminiOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	return runStandardACP(ctx, repoRoot, opts.Prompt, BuildGeminiACPCommand(opts), runtimeSpec{
		component:   "playground.gemini_acp",
		name:        "GeminiACP",
		description: "Gemini CLI playground agent via ACP",
		startMsg:    "starting Gemini ACP playground",
	}, stdin, stdout, stderr)
}

func BuildGeminiACPCommand(opts GeminiOptions) []string {
	cmd := []string{opts.GeminiBin, "--experimental-acp"}
	if strings.TrimSpace(opts.Model) != "" {
		cmd = append(cmd, "--model", opts.Model)
	}
	cmd = append(cmd, opts.GeminiArgs...)
	return cmd
}

func RunGeminiACPInfo(
	ctx context.Context,
	repoRoot string,
	opts GeminiOptions,
	jsonOutput bool,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return runACPInfo(
		ctx,
		repoRoot,
		BuildGeminiACPCommand(opts),
		"playground.gemini_acp_info",
		"inspecting Gemini ACP agent",
		jsonOutput,
		stdout,
		stderr,
	)
}
