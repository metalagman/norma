package acpcmd

import "github.com/spf13/cobra"

// Command builds the `norma playground acp` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "acp",
		Short:        "ACP playground commands for model integrations",
		SilenceUsage: true,
	}

	infoCmd := &cobra.Command{
		Use:          "info",
		Short:        "Inspect ACP CLI capabilities and auth methods",
		SilenceUsage: true,
	}
	infoCmd.AddCommand(GeminiInfoCommand())
	infoCmd.AddCommand(OpenCodeInfoCommand())
	infoCmd.AddCommand(CodexInfoCommand())

	webCmd := &cobra.Command{
		Use:          "web",
		Short:        "Run ACP playground models with the ADK web launcher",
		SilenceUsage: true,
	}
	webCmd.AddCommand(GeminiWebCommand())
	webCmd.AddCommand(OpenCodeWebCommand())
	webCmd.AddCommand(CodexWebCommand())

	cmd.AddCommand(GeminiCommand())
	cmd.AddCommand(OpenCodeCommand())
	cmd.AddCommand(CodexCommand())
	cmd.AddCommand(infoCmd)
	cmd.AddCommand(webCmd)
	return cmd
}
