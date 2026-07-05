package acprepl

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"github.com/charmbracelet/glamour"
	acp "github.com/coder/acp-go-sdk"
	"github.com/normahq/go-adk-acpagent"
	"github.com/normahq/norma/internal/apps/appio"
	"github.com/normahq/norma/internal/logging"
	"github.com/rs/zerolog"
	adkagent "google.golang.org/adk/agent"
	runnerpkg "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	toolReplCommandExit  = "exit"
	toolReplCommandQuit  = "quit"
	acpToolCallEventName = "acp_tool_call"
	defaultREPLAppName   = "norma-tool-acp-repl"
	defaultREPLUserID    = "norma-tool-user"

	ansiGray  = "\x1b[90m"
	ansiReset = "\x1b[0m"
)

var (
	markdownRendererOnce sync.Once
	markdownRenderer     *glamour.TermRenderer
	markdownRendererErr  error
	markdownPattern      = regexp.MustCompile("(?m)(^#{1,6}\\s|^>\\s|^[-*+]\\s|^\\d+\\.\\s|```|`[^`]+`|\\*\\*[^*]+\\*\\*|_[^_]+_|\\[[^\\]]+\\]\\([^)]+\\))")

	replNewAgentRunner = newAgentRunner
	replRunACPToolTurn = runACPToolTurn
)

// PermissionHandler decides how ACP permission requests should be handled.
type PermissionHandler = acpagent.PermissionHandler

// AgentFactory builds an ADK agent and returns an optional close function.
type AgentFactory func(context.Context, PermissionHandler, io.Writer) (adkagent.Agent, func() error, error)

// AgentREPLConfig configures a generic ADK-backed terminal REPL.
type AgentREPLConfig struct {
	AppName             string
	UserID              string
	Stdin               io.Reader
	Stdout              io.Writer
	Stderr              io.Writer
	AgentFactory        AgentFactory
	StartupPrompt       string
	StartupPromptSilent bool
}

func RunREPL(
	ctx context.Context,
	workingDir string,
	command []string,
	sessionModel string,
	sessionMode string,
	stdin io.Reader,
	stdout io.Writer,
	stderr io.Writer,
) error {
	return RunAgentREPL(ctx, AgentREPLConfig{
		AppName: defaultREPLAppName,
		UserID:  defaultREPLUserID,
		Stdin:   stdin,
		Stdout:  stdout,
		Stderr:  stderr,
		AgentFactory: func(ctx context.Context, permissionHandler PermissionHandler, agentStderr io.Writer) (adkagent.Agent, func() error, error) {
			l := zerolog.Ctx(ctx)
			l.Debug().
				Str("working_dir", workingDir).
				Strs("command", command).
				Msg("starting ACP REPL tool")

			agentRuntime, err := acpagent.New(acpagent.Config{
				Context:           ctx,
				Name:              "acp_repl_agent",
				Description:       "Generic ACP REPL tool",
				Model:             strings.TrimSpace(sessionModel),
				Mode:              strings.TrimSpace(sessionMode),
				Command:           command,
				WorkingDir:        workingDir,
				Stderr:            agentStderr,
				PermissionHandler: permissionHandler,
				Logger:            logging.Slog().With("component", "tool.acp_repl"),
			})
			if err != nil {
				l.Error().Err(err).Msg("failed to create ACP runtime")
				return nil, nil, err
			}
			return agentRuntime, agentRuntime.Close, nil
		},
	})
}

// RunAgentREPL runs a line-based REPL for an ADK agent factory.
func RunAgentREPL(ctx context.Context, cfg AgentREPLConfig) error {
	if cfg.Stdin == nil {
		return errors.New("stdin is required")
	}
	if cfg.Stdout == nil {
		return errors.New("stdout is required")
	}
	if cfg.Stderr == nil {
		return errors.New("stderr is required")
	}
	if cfg.AgentFactory == nil {
		return errors.New("agent factory is required")
	}

	appName := strings.TrimSpace(cfg.AppName)
	if appName == "" {
		appName = defaultREPLAppName
	}
	userID := strings.TrimSpace(cfg.UserID)
	if userID == "" {
		userID = defaultREPLUserID
	}

	lockedStderr := appio.NewSyncWriter(cfg.Stderr)
	ui := newACPToolTerminal(cfg.Stdin, cfg.Stdout, lockedStderr)
	logger := zerolog.Ctx(ctx)

	agentRuntime, closeAgent, err := cfg.AgentFactory(ctx, ui.RequestPermission, lockedStderr)
	if err != nil {
		return err
	}
	if closeAgent != nil {
		defer func() {
			if closeErr := closeAgent(); closeErr != nil {
				logger.Warn().Err(closeErr).Msg("failed to close repl agent")
			}
		}()
	}

	replRunner, sess, err := replNewAgentRunner(ctx, agentRuntime, appName, userID)
	if err != nil {
		logger.Error().Err(err).Msg("failed to create ADK runner/session")
		return err
	}

	logger.Debug().Str("session_id", sess.ID()).Msg("created ADK session")
	logger.Debug().Str("app_name", appName).Str("user_id", userID).Msg("starting interactive REPL")
	startupPrompt := strings.TrimSpace(cfg.StartupPrompt)
	if startupPrompt != "" {
		startupUI := ui
		if cfg.StartupPromptSilent {
			startupUI = newACPToolTerminal(strings.NewReader(""), io.Discard, io.Discard)
		}
		if err := replRunACPToolTurn(ctx, replRunner, sess, userID, startupUI, startupPrompt); err != nil {
			return err
		}
	}

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
		if err := replRunACPToolTurn(ctx, replRunner, sess, userID, ui, trimmedPrompt); err != nil {
			return err
		}
	}
}

func newAgentRunner(ctx context.Context, a adkagent.Agent, appName, userID string) (*runnerpkg.Runner, session.Session, error) {
	sessionService := session.InMemoryService()
	r, err := runnerpkg.New(runnerpkg.Config{
		AppName:        appName,
		Agent:          a,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create ACP REPL runner: %w", err)
	}
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: appName,
		UserID:  userID,
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
	userID string,
	ui *acpToolTerminal,
	prompt string,
) error {
	logger := zerolog.Ctx(ctx)
	trimmedPrompt := strings.TrimSpace(prompt)
	logger.Debug().
		Str("session_id", sess.ID()).
		Int("prompt_len", len(trimmedPrompt)).
		Msg("starting tool REPL turn")

	events := r.Run(ctx, userID, sess.ID(), genai.NewContentFromText(trimmedPrompt, genai.RoleUser), adkagent.RunConfig{})
	accumulator := newACPToolTurnAccumulator(ui)
	eventCount := 0
	partialCount := 0
	for ev, err := range events {
		if err != nil {
			logger.Error().Err(err).Str("session_id", sess.ID()).Msg("tool REPL turn failed")
			return err
		}
		eventCount++

		partialCount += renderACPToolEvent(accumulator, ev)
	}
	accumulator.flushAll()
	logger.Debug().
		Str("session_id", sess.ID()).
		Int("event_count", eventCount).
		Int("partial_count", partialCount).
		Int("response_len", accumulator.textOutputLen).
		Msg("completed tool REPL turn")
	return nil
}

type acpToolTurnAccumulator struct {
	ui *acpToolTerminal

	thoughtBuf strings.Builder
	textBuf    strings.Builder

	textOutputLen int
	auxOutputSeen bool
}

func newACPToolTurnAccumulator(ui *acpToolTerminal) *acpToolTurnAccumulator {
	return &acpToolTurnAccumulator{
		ui: ui,
	}
}

func (a *acpToolTurnAccumulator) appendThought(text string) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return
	}
	a.flushText()
	a.thoughtBuf.WriteString(text)
}

func (a *acpToolTurnAccumulator) appendText(text string) {
	if text == "" {
		return
	}
	a.flushThought()
	a.textBuf.WriteString(text)
}

func (a *acpToolTurnAccumulator) flushThought() {
	if a.thoughtBuf.Len() == 0 {
		return
	}
	normalized := normalizeThoughtText(a.thoughtBuf.String())
	if normalized != "" {
		a.ui.Printf("Thought: %s%s%s\n", ansiGray, normalized, ansiReset)
		a.auxOutputSeen = true
	}
	a.thoughtBuf.Reset()
}

func (a *acpToolTurnAccumulator) flushText() {
	if a.textBuf.Len() == 0 {
		return
	}
	rendered := renderMarkdownOrPlain(a.textBuf.String())
	if rendered == "" {
		a.textBuf.Reset()
		return
	}
	if a.auxOutputSeen && a.textOutputLen == 0 {
		a.ui.Println()
	}
	a.ui.Println(rendered)
	a.textOutputLen += len(rendered)
	a.textBuf.Reset()
}

func (a *acpToolTurnAccumulator) flushAll() {
	a.flushThought()
	a.flushText()
}

func (a *acpToolTurnAccumulator) printToolCallStart(title string, params any) {
	// Parameter payloads are intentionally hidden to keep transcripts readable.
	// Only the tool name/title is displayed here, providing clear identification
	// of which tool ran without introducing large request bodies into the output.
	toolTitle := strings.TrimSpace(title)
	if toolTitle == "" {
		toolTitle = acpToolCallEventName
	}
	a.ui.Printf("ToolCall: %s\n", toolTitle)
	a.auxOutputSeen = true
}

func renderACPToolEvent(accumulator *acpToolTurnAccumulator, ev *session.Event) int {
	if accumulator == nil || ev == nil {
		return 0
	}
	partialCount := 0
	if ev.Content != nil {
		for _, part := range ev.Content.Parts {
			if part == nil {
				continue
			}
			if part.FunctionCall != nil && part.FunctionCall.Name == acpToolCallEventName {
				accumulator.flushAll()
				args := mapFromAny(part.FunctionCall.Args)
				title := mapFieldString(args, "title")
				if title == "" {
					title = part.FunctionCall.Name
				}
				accumulator.printToolCallStart(title, args["rawInput"])
				continue
			}
			if part.FunctionResponse != nil && part.FunctionResponse.Name == "acp_tool_call_update" {
				// Tool call updates are intentionally hidden to avoid noisy repeated statuses.
				continue
			}
			text := extractACPToolEventPartText(part)
			if text == "" {
				continue
			}
			if ev.TurnComplete && !ev.Partial && accumulator.hasVisibleOutput() {
				continue
			}
			if part.Thought {
				accumulator.appendThought(text)
				continue
			}
			accumulator.appendText(text)
			if ev.Partial {
				partialCount++
			}
		}
	}
	if ev.TurnComplete {
		accumulator.flushAll()
	}
	return partialCount
}

func (a *acpToolTurnAccumulator) hasVisibleOutput() bool {
	if a == nil {
		return false
	}
	return a.textOutputLen > 0 || a.textBuf.Len() > 0
}

func extractACPToolEventPartText(part *genai.Part) string {
	if part == nil {
		return ""
	}
	return part.Text
}

func renderMarkdownOrPlain(text string) string {
	if !looksLikeMarkdown(text) {
		return normalizePlainText(text)
	}
	rendered, err := renderMarkdown(text)
	if err != nil {
		return normalizePlainText(text)
	}
	trimmed := trimOuterBlankLines(rendered)
	if trimmed == "" {
		return normalizePlainText(text)
	}
	return trimmed
}

func renderMarkdown(text string) (string, error) {
	markdownRendererOnce.Do(func() {
		markdownRenderer, markdownRendererErr = glamour.NewTermRenderer(
			glamour.WithStandardStyle("dark"),
			glamour.WithWordWrap(0),
		)
	})
	if markdownRendererErr != nil {
		return "", markdownRendererErr
	}
	return markdownRenderer.Render(text)
}

func looksLikeMarkdown(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	return markdownPattern.MatchString(trimmed)
}

func normalizePlainText(text string) string {
	trimmed := trimOuterBlankLines(text)
	if trimmed == "" {
		return ""
	}
	lines := strings.Split(trimmed, "\n")
	for idx, line := range lines {
		lines[idx] = strings.TrimLeft(line, " \t")
	}
	return strings.Join(lines, "\n")
}

func normalizeThoughtText(text string) string {
	trimmed := trimOuterBlankLines(text)
	if trimmed == "" {
		return ""
	}
	parts := strings.Fields(trimmed)
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}

func trimOuterBlankLines(text string) string {
	if text == "" {
		return ""
	}
	lines := strings.Split(text, "\n")
	start := 0
	for start < len(lines) && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	end := len(lines)
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	if start >= end {
		return ""
	}
	return strings.Join(lines[start:end], "\n")
}

func mapFromAny(value any) map[string]any {
	if value == nil {
		return nil
	}
	m, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	return m
}

func mapFieldString(values map[string]any, key string) string {
	if len(values) == 0 {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

type acpToolTerminal struct {
	reader *bufio.Reader
	stdout io.Writer
	stderr io.Writer
	mu     sync.Mutex
}

func newACPToolTerminal(stdin io.Reader, stdout, stderr io.Writer) *acpToolTerminal {
	return &acpToolTerminal{
		reader: bufio.NewReader(stdin),
		stdout: stdout,
		stderr: stderr,
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

func (t *acpToolTerminal) Printf(format string, args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = fmt.Fprintf(t.stdout, format, args...)
}

func (t *acpToolTerminal) Println(args ...any) {
	t.mu.Lock()
	defer t.mu.Unlock()
	_, _ = fmt.Fprintln(t.stdout, args...)
}

func (t *acpToolTerminal) RequestPermission(ctx context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	logger := zerolog.Ctx(ctx)
	title := ""
	if req.ToolCall.Title != nil {
		title = *req.ToolCall.Title
	}
	logger.Debug().
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
