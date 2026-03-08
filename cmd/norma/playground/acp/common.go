package acpcmd

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
	"google.golang.org/adk/agent"
	runnerpkg "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	replCommandExit = "exit"
	replCommandQuit = "quit"
)

func newACPPlaygroundCommand(
	use string,
	short string,
	bindFlags func(*cobra.Command),
	runFunc func(context.Context, string, io.Reader, io.Writer, io.Writer) error,
) *cobra.Command {
	cmd := &cobra.Command{
		Use:          use,
		Short:        short,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repoRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			return runFunc(cmd.Context(), repoRoot, cmd.InOrStdin(), cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	bindFlags(cmd)
	return cmd
}

func newACPInfoCommand(
	use string,
	short string,
	bindFlags func(*cobra.Command),
	runFunc func(context.Context, string, bool, io.Writer, io.Writer) error,
) *cobra.Command {
	jsonOutput := false
	cmd := &cobra.Command{
		Use:          use,
		Short:        short,
		SilenceUsage: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			repoRoot, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("get working directory: %w", err)
			}
			return runFunc(cmd.Context(), repoRoot, jsonOutput, cmd.OutOrStdout(), cmd.ErrOrStderr())
		},
	}
	if bindFlags != nil {
		bindFlags(cmd)
	}
	cmd.Flags().BoolVar(&jsonOutput, "json", false, "print output as JSON")
	return cmd
}

func newPlaygroundRunner(ctx context.Context, a agent.Agent) (*runnerpkg.Runner, session.Session, error) {
	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        "norma-playground",
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create playground runner: %w", err)
	}
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma-playground",
		UserID:  "norma-playground-user",
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create playground session: %w", err)
	}
	return r, sess.Session, nil
}

func runPlaygroundTurn(
	ctx context.Context,
	r *runnerpkg.Runner,
	sess session.Session,
	ui *playgroundTerminal,
	logger zerolog.Logger,
	prompt string,
) error {
	trimmedPrompt := strings.TrimSpace(prompt)
	logger.Info().
		Str("session_id", sess.ID()).
		Int("prompt_len", len(trimmedPrompt)).
		Msg("starting playground turn")

	events := r.Run(ctx, "norma-playground-user", sess.ID(), genai.NewContentFromText(trimmedPrompt, genai.RoleUser), agent.RunConfig{})
	var partialResponse strings.Builder
	finalResponse := ""
	printedFinal := false
	eventCount := 0
	partialCount := 0
	for ev, err := range events {
		if err != nil {
			logger.Error().Err(err).Str("session_id", sess.ID()).Msg("playground turn failed")
			return err
		}
		eventCount++
		text := extractEventText(ev)
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
	logger.Info().
		Str("session_id", sess.ID()).
		Int("event_count", eventCount).
		Int("partial_count", partialCount).
		Int("response_len", len(finalResponse)).
		Bool("printed_final", printedFinal).
		Msg("completed playground turn")
	return nil
}

func extractEventText(ev *session.Event) string {
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

func handleREPLReadErr(err error, ui *playgroundTerminal, logger zerolog.Logger) error {
	if errors.Is(err, io.EOF) {
		ui.Println()
		logger.Info().Msg("received EOF, exiting REPL")
		return nil
	}
	logger.Error().Err(err).Msg("failed to read REPL input")
	return err
}

type playgroundTerminal struct {
	reader *bufio.Reader
	stdout io.Writer
	stderr io.Writer
	logger zerolog.Logger
	mu     sync.Mutex
}

type runtimeSpec struct {
	component   string
	name        string
	description string
	startMsg    string
}

type modelACPCommandConfig struct {
	Use       string
	Short     string
	InfoShort string

	Prompt *string
	Model  *string
	Binary *string
	Args   *[]string

	ModelHelp  string
	BinaryFlag string
	BinaryHelp string
	ArgsFlag   string
	ArgsHelp   string

	Run     func(context.Context, string, io.Reader, io.Writer, io.Writer) error
	RunInfo func(context.Context, string, bool, io.Writer, io.Writer) error
}

func newModelACPRunCommand(cfg modelACPCommandConfig) *cobra.Command {
	return newACPPlaygroundCommand(
		cfg.Use,
		cfg.Short,
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(cfg.Prompt, "prompt", "", "single prompt to run; if empty starts a REPL")
			cmd.Flags().StringVar(cfg.Model, "model", "", cfg.ModelHelp)
			cmd.Flags().StringVar(cfg.Binary, cfg.BinaryFlag, *cfg.Binary, cfg.BinaryHelp)
			cmd.Flags().StringArrayVar(cfg.Args, cfg.ArgsFlag, nil, cfg.ArgsHelp)
		},
		cfg.Run,
	)
}

func newModelACPInfoCommand(cfg modelACPCommandConfig) *cobra.Command {
	return newACPInfoCommand(
		cfg.Use,
		cfg.InfoShort,
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(cfg.Model, "model", "", cfg.ModelHelp)
			cmd.Flags().StringVar(cfg.Binary, cfg.BinaryFlag, *cfg.Binary, cfg.BinaryHelp)
			cmd.Flags().StringArrayVar(cfg.Args, cfg.ArgsFlag, nil, cfg.ArgsHelp)
		},
		cfg.RunInfo,
	)
}

func runStandardACP(
	ctx context.Context,
	repoRoot string,
	prompt string,
	command []string,
	spec runtimeSpec,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	lockedStderr := &syncWriter{writer: stderr}
	logger := zerolog.New(zerolog.ConsoleWriter{Out: lockedStderr, TimeFormat: time.RFC3339}).
		With().Timestamp().Str("component", spec.component).Logger()
	ui := newPlaygroundTerminal(stdin, stdout, lockedStderr, logger)

	logger.Info().
		Str("repo_root", repoRoot).
		Strs("command", command).
		Msg(spec.startMsg)

	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              spec.name,
		Description:       spec.description,
		Command:           command,
		WorkingDir:        repoRoot,
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

	runner, sess, err := newPlaygroundRunner(ctx, agentRuntime)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create ADK runner/session")
		return err
	}
	logger.Info().Str("session_id", sess.ID()).Msg("created ADK session")

	if strings.TrimSpace(prompt) != "" {
		logger.Info().Int("prompt_len", len(strings.TrimSpace(prompt))).Msg("running one-shot prompt")
		return runPlaygroundTurn(ctx, runner, sess, ui, logger, prompt)
	}
	logger.Info().Msg("starting interactive REPL")

	for {
		line, err := ui.ReadLine("> ")
		if err != nil {
			return handleREPLReadErr(err, ui, logger)
		}
		trimmedPrompt := strings.TrimSpace(line)
		if trimmedPrompt == "" {
			continue
		}
		switch trimmedPrompt {
		case replCommandExit, replCommandQuit:
			logger.Info().Msg("received exit command, exiting REPL")
			return nil
		}
		if err := runPlaygroundTurn(ctx, runner, sess, ui, logger, trimmedPrompt); err != nil {
			return err
		}
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

func newPlaygroundTerminal(stdin io.Reader, stdout, stderr io.Writer, logger zerolog.Logger) *playgroundTerminal {
	return &playgroundTerminal{
		reader: bufio.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
		logger: logger,
	}
}

func (t *playgroundTerminal) ReadLine(prompt string) (string, error) {
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

func (t *playgroundTerminal) Println(args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = fmt.Fprintln(t.stdout, args...)
}

func (t *playgroundTerminal) RequestPermission(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	title := ""
	if req.ToolCall.Title != nil {
		title = *req.ToolCall.Title
	}
	t.logger.Info().
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
