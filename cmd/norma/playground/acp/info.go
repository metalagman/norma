package acpcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/metalagman/norma/internal/adk/acpagent"
	"github.com/rs/zerolog"
)

type acpInspectOutput struct {
	Command    []string                `json:"command"`
	Initialize *acp.InitializeResponse `json:"initialize,omitempty"`
	Session    *acp.NewSessionResponse `json:"session,omitempty"`
}

func runACPInfo(
	ctx context.Context,
	repoRoot string,
	command []string,
	component string,
	startMsg string,
	jsonOutput bool,
	stdout io.Writer,
	stderr io.Writer,
) error {
	lockedStderr := &syncWriter{writer: stderr}
	logger := zerolog.New(zerolog.ConsoleWriter{Out: lockedStderr, TimeFormat: time.RFC3339}).
		Level(zerolog.DebugLevel).
		With().Timestamp().Str("component", component).Logger()

	logger.Info().
		Str("repo_root", repoRoot).
		Strs("command", command).
		Msg(startMsg)

	client, err := acpagent.NewClient(ctx, acpagent.ClientConfig{
		Command:    command,
		WorkingDir: repoRoot,
		Stderr:     lockedStderr,
		Logger:     &logger,
	})
	if err != nil {
		return fmt.Errorf("create acp client: %w", err)
	}
	defer func() {
		if closeErr := client.Close(); closeErr != nil {
			logger.Warn().Err(closeErr).Msg("failed to close ACP client")
		}
	}()

	initResp, err := client.Initialize(ctx)
	if err != nil {
		return fmt.Errorf("initialize acp client: %w", err)
	}
	sessionResp, err := client.NewSession(ctx, repoRoot)
	if err != nil {
		return fmt.Errorf("create acp session: %w", err)
	}

	inspectOutput := &acpInspectOutput{
		Command:    append([]string(nil), command...),
		Initialize: &initResp,
		Session:    &sessionResp,
	}
	if jsonOutput {
		return writeACPInspectJSON(stdout, inspectOutput)
	}
	return writeACPInspectHuman(stdout, inspectOutput)
}

func writeACPInspectJSON(stdout io.Writer, inspectOutput *acpInspectOutput) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(inspectOutput)
}

func writeACPInspectHuman(stdout io.Writer, inspectOutput *acpInspectOutput) error {
	if inspectOutput.Initialize == nil {
		_, err := fmt.Fprintln(stdout, "Connected to ACP agent, but initialize result is empty.")
		return err
	}

	agentName := "unknown"
	agentVersion := "unknown"
	agentTitle := ""
	if inspectOutput.Initialize.AgentInfo != nil {
		if strings.TrimSpace(inspectOutput.Initialize.AgentInfo.Name) != "" {
			agentName = strings.TrimSpace(inspectOutput.Initialize.AgentInfo.Name)
		}
		if strings.TrimSpace(inspectOutput.Initialize.AgentInfo.Version) != "" {
			agentVersion = strings.TrimSpace(inspectOutput.Initialize.AgentInfo.Version)
		}
		if inspectOutput.Initialize.AgentInfo.Title != nil {
			agentTitle = strings.TrimSpace(*inspectOutput.Initialize.AgentInfo.Title)
		}
	}
	if _, err := fmt.Fprintf(stdout, "Agent: %s %s\n", agentName, agentVersion); err != nil {
		return err
	}
	if agentTitle != "" {
		if _, err := fmt.Fprintf(stdout, "Title: %s\n", agentTitle); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(stdout, "Protocol: %d\n", inspectOutput.Initialize.ProtocolVersion); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Capabilities: %s\n", formatACPAgentCapabilities(inspectOutput.Initialize.AgentCapabilities)); err != nil {
		return err
	}
	if err := writeACPAuthMethodSummary(stdout, inspectOutput.Initialize.AuthMethods); err != nil {
		return err
	}
	return writeACPSessionSummary(stdout, inspectOutput.Session)
}

func formatACPAgentCapabilities(caps acp.AgentCapabilities) string {
	return fmt.Sprintf(
		"load_session=%t, mcp(http=%t,sse=%t), prompt(audio=%t,image=%t,embedded_context=%t)",
		caps.LoadSession,
		caps.McpCapabilities.Http,
		caps.McpCapabilities.Sse,
		caps.PromptCapabilities.Audio,
		caps.PromptCapabilities.Image,
		caps.PromptCapabilities.EmbeddedContext,
	)
}

func writeACPAuthMethodSummary(stdout io.Writer, methods []acp.AuthMethod) error {
	if _, err := fmt.Fprintf(stdout, "Auth methods (%d):\n", len(methods)); err != nil {
		return err
	}
	for _, method := range methods {
		line := fmt.Sprintf("- %s: %s", method.Id, strings.TrimSpace(method.Name))
		if method.Description != nil && strings.TrimSpace(*method.Description) != "" {
			line += ": " + strings.TrimSpace(*method.Description)
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func writeACPSessionSummary(stdout io.Writer, session *acp.NewSessionResponse) error {
	if session == nil {
		_, err := fmt.Fprintln(stdout, "Session: unavailable")
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Session: %s\n", session.SessionId); err != nil {
		return err
	}
	if err := writeACPSessionModeSummary(stdout, session.Modes); err != nil {
		return err
	}
	return writeACPSessionModelSummary(stdout, session.Models)
}

func writeACPSessionModeSummary(stdout io.Writer, modes *acp.SessionModeState) error {
	if modes == nil {
		_, err := fmt.Fprintln(stdout, "Session modes: unavailable")
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Session modes: current=%s, available (%d):\n", modes.CurrentModeId, len(modes.AvailableModes)); err != nil {
		return err
	}
	for _, mode := range modes.AvailableModes {
		line := fmt.Sprintf("- %s: %s", mode.Id, strings.TrimSpace(mode.Name))
		if mode.Description != nil && strings.TrimSpace(*mode.Description) != "" {
			line += ": " + strings.TrimSpace(*mode.Description)
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}

func writeACPSessionModelSummary(stdout io.Writer, models *acp.SessionModelState) error {
	if models == nil {
		_, err := fmt.Fprintln(stdout, "Session models: unavailable")
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Session models: current=%s, available (%d):\n", models.CurrentModelId, len(models.AvailableModels)); err != nil {
		return err
	}
	for _, model := range models.AvailableModels {
		line := fmt.Sprintf("- %s: %s", model.ModelId, strings.TrimSpace(model.Name))
		if model.Description != nil && strings.TrimSpace(*model.Description) != "" {
			line += ": " + strings.TrimSpace(*model.Description)
		}
		if _, err := fmt.Fprintln(stdout, line); err != nil {
			return err
		}
	}
	return nil
}
