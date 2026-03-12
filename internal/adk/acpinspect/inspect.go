package acpinspect

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/metalagman/norma/internal/adk/acpagent"
	"github.com/rs/zerolog"
)

// RunConfig describes how ACP inspection should run.
type RunConfig struct {
	Command      []string
	WorkingDir   string
	SessionModel string
	Component    string
	StartMessage string
	JSONOutput   bool
	Stdout       io.Writer
	Stderr       io.Writer
}

type inspectOutput struct {
	Command    []string                `json:"command"`
	Initialize *acp.InitializeResponse `json:"initialize,omitempty"`
	Session    *acp.NewSessionResponse `json:"session,omitempty"`
}

// Run connects to an ACP server command and prints initialize/session details.
func Run(ctx context.Context, cfg RunConfig) error {
	if len(cfg.Command) == 0 {
		return fmt.Errorf("acp server command is required")
	}
	if cfg.Stdout == nil {
		cfg.Stdout = io.Discard
	}
	if cfg.Stderr == nil {
		cfg.Stderr = io.Discard
	}

	restoreLogLevel := forceGlobalDebugLogging()
	defer restoreLogLevel()

	component := strings.TrimSpace(cfg.Component)
	if component == "" {
		component = "acp.inspect"
	}
	startMessage := strings.TrimSpace(cfg.StartMessage)
	if startMessage == "" {
		startMessage = "inspecting ACP agent"
	}

	lockedStderr := &syncWriter{writer: cfg.Stderr}
	logger := zerolog.New(zerolog.ConsoleWriter{Out: lockedStderr, TimeFormat: time.RFC3339}).
		Level(zerolog.DebugLevel).
		With().Timestamp().Str("component", component).Logger()

	logger.Info().
		Str("repo_root", cfg.WorkingDir).
		Strs("command", cfg.Command).
		Msg(startMessage)

	client, err := acpagent.NewClient(ctx, acpagent.ClientConfig{
		Command:    cfg.Command,
		WorkingDir: cfg.WorkingDir,
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
	sessionResp, err := client.CreateSession(ctx, cfg.WorkingDir, cfg.SessionModel)
	if err != nil {
		return fmt.Errorf("create acp session: %w", err)
	}

	output := &inspectOutput{
		Command:    append([]string(nil), cfg.Command...),
		Initialize: &initResp,
		Session:    &sessionResp,
	}
	if cfg.JSONOutput {
		return writeInspectJSON(cfg.Stdout, output)
	}
	return writeInspectHuman(cfg.Stdout, output)
}

func writeInspectJSON(stdout io.Writer, output *inspectOutput) error {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(output)
}

func writeInspectHuman(stdout io.Writer, output *inspectOutput) error {
	if output.Initialize == nil {
		_, err := fmt.Fprintln(stdout, "Connected to ACP agent, but initialize result is empty.")
		return err
	}

	agentName := "unknown"
	agentVersion := "unknown"
	agentTitle := ""
	if output.Initialize.AgentInfo != nil {
		if strings.TrimSpace(output.Initialize.AgentInfo.Name) != "" {
			agentName = strings.TrimSpace(output.Initialize.AgentInfo.Name)
		}
		if strings.TrimSpace(output.Initialize.AgentInfo.Version) != "" {
			agentVersion = strings.TrimSpace(output.Initialize.AgentInfo.Version)
		}
		if output.Initialize.AgentInfo.Title != nil {
			agentTitle = strings.TrimSpace(*output.Initialize.AgentInfo.Title)
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
	if _, err := fmt.Fprintf(stdout, "Protocol: %d\n", output.Initialize.ProtocolVersion); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Capabilities: %s\n", formatAgentCapabilities(output.Initialize.AgentCapabilities)); err != nil {
		return err
	}
	if err := writeAuthMethodSummary(stdout, output.Initialize.AuthMethods); err != nil {
		return err
	}
	return writeSessionSummary(stdout, output.Session)
}

func formatAgentCapabilities(caps acp.AgentCapabilities) string {
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

func writeAuthMethodSummary(stdout io.Writer, methods []acp.AuthMethod) error {
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

func writeSessionSummary(stdout io.Writer, session *acp.NewSessionResponse) error {
	if session == nil {
		_, err := fmt.Fprintln(stdout, "Session: unavailable")
		return err
	}
	if _, err := fmt.Fprintf(stdout, "Session: %s\n", session.SessionId); err != nil {
		return err
	}
	if err := writeSessionModeSummary(stdout, session.Modes); err != nil {
		return err
	}
	return writeSessionModelSummary(stdout, session.Models)
}

func writeSessionModeSummary(stdout io.Writer, modes *acp.SessionModeState) error {
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

func writeSessionModelSummary(stdout io.Writer, models *acp.SessionModelState) error {
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

func forceGlobalDebugLogging() func() {
	prev := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	return func() {
		zerolog.SetGlobalLevel(prev)
	}
}

type syncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
