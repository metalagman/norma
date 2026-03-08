package acpcmd

import "github.com/spf13/cobra"

// Command builds the `norma playground acp` command group.
func Command() *cobra.Command {
	cmd := &cobra.Command{
		Use:          "acp",
		Short:        "ACP playground commands for model integrations",
		SilenceUsage: true,
	}

	peclCmd := &cobra.Command{
		Use:          "pecl",
		Short:        "Run ACP playground CLIs",
		SilenceUsage: true,
	}
	peclCmd.AddCommand(GeminiCommand())
	peclCmd.AddCommand(OpenCodeCommand())
	peclCmd.AddCommand(CodexCommand())

	infoCmd := &cobra.Command{
		Use:          "info",
		Short:        "Inspect ACP CLI capabilities and auth methods",
		SilenceUsage: true,
	}
	infoCmd.AddCommand(GeminiInfoCommand())
	infoCmd.AddCommand(OpenCodeInfoCommand())
	infoCmd.AddCommand(CodexInfoCommand())

	cmd.AddCommand(peclCmd)
	cmd.AddCommand(infoCmd)
	return cmd
}
