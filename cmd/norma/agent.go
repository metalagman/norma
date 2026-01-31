package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/workflows/normaloop/models"
	"github.com/spf13/cobra"
)

var (
	agentConfigStr string
)

var agentCmd = &cobra.Command{
	Use:    "agent",
	Short:  "Internal agent execution tools",
	Hidden: true,
}

var agentLoopCmd = &cobra.Command{
	Use:   "loop",
	Short: "Run a loop agent",
	RunE: func(cmd *cobra.Command, args []string) error {
		var cfg config.AgentConfig
		if err := json.Unmarshal([]byte(agentConfigStr), &cfg); err != nil {
			return fmt.Errorf("failed to unmarshal agent config: %w", err)
		}

		// Read AgentRequest from stdin (standard ADK behavior)
		// ADK might send a prompt before the JSON, so we need to be robust.
		stdinData, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("failed to read from stdin: %w", err)
		}

		jsonData, ok := agent.ExtractJSON(stdinData)
		if !ok {
			return fmt.Errorf("failed to find JSON in stdin")
		}

		var req models.AgentRequest
		if err := json.Unmarshal(jsonData, &req); err != nil {
			return fmt.Errorf("failed to unmarshal agent request: %w", err)
		}

		ctx := context.Background()
		out, err := agent.RunLoop(ctx, cfg, req, os.Stdout, os.Stderr, "", "", "")
		if err != nil {
			return err
		}

		// Write output.json as required by Norma
		if req.Paths.RunDir != "" {
			outputPath := fmt.Sprintf("%s/output.json", req.Paths.RunDir)
			_ = os.WriteFile(outputPath, out, 0o644)
		}

		// Write to stdout as required by ADK
		fmt.Print(string(out))
		return nil
	},
}

func initAgentCmd() {
	agentLoopCmd.Flags().StringVar(&agentConfigStr, "config", "", "JSON serialized AgentConfig")
	_ = agentLoopCmd.MarkFlagRequired("config")

	agentCmd.AddCommand(agentLoopCmd)
	rootCmd.AddCommand(agentCmd)
}
