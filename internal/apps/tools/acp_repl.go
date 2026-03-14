package tools

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/metalagman/norma/internal/adk/acpagent"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	adkagent "google.golang.org/adk/agent"
	runnerpkg "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	toolReplCommandExit = "exit"
	toolReplCommandQuit = "quit"
)

// ACPREPLRunFunc executes an ACP REPL session.
type ACPREPLRunFunc func(
	ctx context.Context,
	workingDir string,
	command []string,
	sessionModel string,
	sessionMode string,
	logLevel zerolog.Level,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error

// ACPREPLDeps customizes ACP REPL command runtime dependencies.
type ACPREPLDeps struct {
	RunREPL ACPREPLRunFunc
}

// RunACPToolREPL runs ACP REPL runtime with explicit log level.
func RunACPToolREPL(
	ctx context.Context,
	workingDir string,
	command []string,
	sessionModel string,
	sessionMode string,
	logLevel zerolog.Level,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	lockedStderr := &replSyncWriter{writer: stderr}
	logger := zerolog.New(zerolog.ConsoleWriter{Out: lockedStderr, TimeFormat: time.RFC3339}).
		Level(logLevel).
		With().Timestamp().Str("component", "tool.acp_repl").Logger()
	ui := newACPToolTerminal(stdin, stdout, lockedStderr, logger)

	logger.Debug().
		Str("working_dir", workingDir).
		Strs("command", command).
		Msg("starting ACP REPL tool")

	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              "ToolACPREPL",
		Description:       "Generic ACP REPL tool",
		Model:             strings.TrimSpace(sessionModel),
		Mode:              strings.TrimSpace(sessionMode),
		Command:           command,
		WorkingDir:        workingDir,
		Stderr:            lockedStderr,
		PermissionHandler: ui.RequestPermission,
		Logger:            &logger,
	})
	if err != nil {
		logger.Error().Err(err).Msg("failed to create ACP runtime")
		return err
	}
	defer func() {
		if closeErr := agentRuntime.Close(); closeErr != nil {
			logger.Warn().Err(closeErr).Msg("failed to close ACP runtime")
		}
	}()

	runner, sess, err := newACPToolRunner(ctx, agentRuntime)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create ADK runner/session")
		return err
	}
	logger.Debug().Str("session_id", sess.ID()).Msg("created ADK session")
	logger.Debug().Msg("starting interactive REPL")

	for {
		line, err := ui.ReadLine("> ")
		if err != nil {
			if errors.Is(err, io.EOF) {
				ui.Println()
				logger.Debug().Msg("received EOF, exiting REPL")
				return nil
			}
			logger.Error().Err(err).Msg("failed to read REPL input")
			return err
		}
		trimmedPrompt := strings.TrimSpace(line)
		if trimmedPrompt == "" {
			continue
		}
		switch trimmedPrompt {
		case toolReplCommandExit, toolReplCommandQuit:
			logger.Debug().Msg("received exit command, exiting REPL")
			return nil
		}
		if err := runACPToolTurn(ctx, runner, sess, ui, logger, trimmedPrompt); err != nil {
			return err
		}
	}
}

// NewACPReplCommand creates the acp-repl command.
func NewACPReplCommand(runtime RuntimeConfig, deps ACPREPLDeps) *cobra.Command {
	runREPL := deps.RunREPL
	if runREPL == nil {
		runREPL = RunACPToolREPL
	}

	sessionModel := ""
	sessionMode := ""
	debugLogs := false
	cmd := &cobra.Command{
		Use:          "acp-repl [--model <model>] [--mode <mode>] -- <acp-server-cmd> [args...]",
		Short:        "Run an interactive REPL against any stdio ACP server command",
		Long:         "Start a stdio ACP server command and run an interactive terminal REPL over ACP.",
		Example:      "  norma tool acp-repl -- opencode acp\n  norma tool acp-repl --model openai/gpt-5.4 --mode coding -- opencode acp\n  norma tool acp-repl -- gemini --experimental-acp",
		SilenceUsage: true,
		Args:         cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			acpCommand, err := requireACPCommandAfterDash(cmd, args)
			if err != nil {
				return err
			}

			workingDir, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			logLevel := runtime.resolveLogLevel(zerolog.InfoLevel, debugLogs)
			return runREPL(
				cmd.Context(),
				workingDir,
				acpCommand,
				sessionModel,
				sessionMode,
				logLevel,
				cmd.InOrStdin(),
				cmd.OutOrStdout(),
				cmd.ErrOrStderr(),
			)
		},
	}
	cmd.Flags().StringVar(&sessionModel, "model", "", "session model requested via ACP session/set_model")
	cmd.Flags().StringVar(&sessionMode, "mode", "", "session mode requested via ACP session/set_mode")
	if runtime.IncludeDebugFlag {
		cmd.Flags().BoolVar(&debugLogs, "debug", false, "enable debug logging")
	}
	return cmd
}

func newACPToolRunner(ctx context.Context, a adkagent.Agent) (*runnerpkg.Runner, session.Session, error) {
	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        "norma-tool-acp-repl",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create ACP REPL runner: %w", err)
	}
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma-tool-acp-repl",
		UserID:  "norma-tool-user",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create ACP REPL session: %w", err)
	}
	return r, sess.Session, nil
}

func runACPToolTurn(
	ctx context.Context,
	r *runnerpkg.Runner,
	sess session.Session,
	ui *acpToolTerminal,
	logger zerolog.Logger,
	prompt string,
) error {
	trimmedPrompt := strings.TrimSpace(prompt)
	logger.Debug().
		Str("session_id", sess.ID()).
		Int("prompt_len", len(trimmedPrompt)).
		Msg("starting tool REPL turn")

	events := r.Run(ctx, "norma-tool-user", sess.ID(), genai.NewContentFromText(trimmedPrompt, genai.RoleUser), adkagent.RunConfig{})
	var partialResponse strings.Builder
	finalResponse := ""
	printedFinal := false
	eventCount := 0
	partialCount := 0
	for ev, err := range events {
		if err != nil {
			logger.Error().Err(err).Str("session_id", sess.ID()).Msg("tool REPL turn failed")
			return err
		}
		eventCount++
		text := extractACPToolEventText(ev)
		if text == "" {
			continue
		}
		if ev.Partial {
			partialCount++
			partialResponse.WriteString(text)
			continue
		}
		finalResponse = text
	}
	if finalResponse == "" {
		finalResponse = partialResponse.String()
	}
	if finalResponse != "" {
		printedFinal = true
		ui.Println(finalResponse)
	}
	logger.Debug().
		Str("session_id", sess.ID()).
		Int("event_count", eventCount).
		Int("partial_count", partialCount).
		Int("response_len", len(finalResponse)).
		Bool("printed_final", printedFinal).
		Msg("completed tool REPL turn")
	return nil
}

func extractACPToolEventText(ev *session.Event) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range ev.Content.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

type acpToolTerminal struct {
	reader *bufio.Reader
	stdout io.Writer
	stderr io.Writer
	logger zerolog.Logger
	mu     sync.Mutex
}

func newACPToolTerminal(stdin io.Reader, stdout, stderr io.Writer, logger zerolog.Logger) *acpToolTerminal {
	return &acpToolTerminal{
		reader: bufio.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
		logger: logger,
	}
}

func (t *acpToolTerminal) ReadLine(prompt string) (string, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if prompt != "" {
		if _, err := fmt.Fprint(t.stdout, prompt); err != nil {
			return "", err
		}
	}
	line, err := t.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if errors.Is(err, io.EOF) && len(line) == 0 {
		return "", io.EOF
	}
	return line, nil
}

func (t *acpToolTerminal) Println(args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = fmt.Fprintln(t.stdout, args...)
}

func (t *acpToolTerminal) RequestPermission(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	title := ""
	if req.ToolCall.Title != nil {
		title = *req.ToolCall.Title
	}
	t.logger.Debug().
		Str("permission_title", title).
		Int("option_count", len(req.Options)).
		Msg("permission requested")

	if _, err := fmt.Fprintf(t.stdout, "\nPermission requested: %s\n", title); err != nil {
		return acp.RequestPermissionResponse{}, err
	}
	for idx, option := range req.Options {
		if _, err := fmt.Fprintf(t.stdout, "%d. %s (%s)\n", idx+1, option.Name, option.Kind); err != nil {
			return acp.RequestPermissionResponse{}, err
		}
	}
	if _, err := fmt.Fprint(t.stdout, "Choose an option: "); err != nil {
		return acp.RequestPermissionResponse{}, err
	}

	line, err := t.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return acp.RequestPermissionResponse{}, err
	}
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}

	choice, convErr := strconv.Atoi(trimmed)
	if convErr != nil || choice < 1 || choice > len(req.Options) {
		return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
	}
	selected := req.Options[choice-1]
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(selected.OptionId)}, nil
}

type replSyncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *replSyncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
