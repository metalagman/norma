package taskmasterchat

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"
	generictaskmaster "github.com/normahq/norma/v2/internal/apps/taskmaster"
	"github.com/normahq/runtime/v2/agentconfig"
	"github.com/normahq/runtime/v2/agentfactory"
	taskmasterrt "github.com/normahq/runtime/v2/taskmaster"
	taskmasteradk "github.com/normahq/runtime/v2/taskmaster/adk"
	"github.com/rs/zerolog"
)

const (
	taskmasterAgentID  = "taskmaster"
	defaultAgentName   = "Taskmaster"
	defaultDescription = "Workflow-agnostic async task harness"
	fakeChatID         = "local"
	fakeChatSessionID  = "fakechat-local"
)

type Config struct {
	WorkingDir string
	BridgeBin  string
	Stdin      io.Reader
	Stdout     io.Writer
	Stderr     io.Writer
	Logger     *zerolog.Logger
}

type taskEnqueuer interface {
	Enqueue(msg taskmasterrt.Message) error
}

func Run(ctx context.Context, cfg Config) error {
	runCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	shuttingDown := &atomic.Bool{}
	go func() {
		<-runCtx.Done()
		shuttingDown.Store(true)
	}()

	baseLogger := *zerolog.Ctx(runCtx)
	if cfg.Logger != nil {
		baseLogger = *cfg.Logger
	}

	stdin := cfg.Stdin
	if stdin == nil {
		stdin = os.Stdin
	}
	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	filteredStderr := &shutdownAwareStderr{writer: stderr, shuttingDown: shuttingDown}
	console := &chatConsole{writer: stdout}

	command := generictaskmaster.BuildCodexACPCommand(cfg.BridgeBin)
	agentRegistry := map[string]agentconfig.Config{
		taskmasterAgentID: newCodexACPConfig(command),
	}
	factoryOpts := []agentfactory.Option{
		agentfactory.WithPermissionHandler(autoAllowPermission),
		agentfactory.WithStderrWriter(filteredStderr),
	}
	factory := agentfactory.New(agentRegistry, nil, factoryOpts...)

	rootRunner, err := buildLocalRunner(runCtx, factory, localRunnerConfig{
		AgentID:     taskmasterAgentID,
		AppName:     "taskmaster-chat-" + taskmasterAgentID,
		Name:        defaultAgentName,
		Description: defaultDescription,
		Instruction: rootInstruction(),
		WorkingDir:  cfg.WorkingDir,
		UserID:      taskmasterAgentID,
		Logger: baseLogger.With().
			Str("agent_id", taskmasterAgentID).
			Logger(),
	})
	if err != nil {
		return err
	}

	runtime, err := taskmasterrt.New(taskmasterrt.Config{
		Logger:        &baseLogger,
		RootNodeID:    taskmasterAgentID,
		Nodes:         map[string]taskmasterrt.Node{taskmasterAgentID: rootRunner},
		Targets:       []taskmasterrt.Target{newFakeChatTarget(console)},
		OutcomeRouter: routeRootOutcomeTo(taskmasterrt.NewFakeChatHumanLocator(fakeChatID)),
	})
	if err != nil {
		closeNode(rootRunner)
		return err
	}
	if err := runtime.Start(runCtx); err != nil {
		closeNode(rootRunner)
		return err
	}

	startedAt := time.Now()
	inputErrCh := make(chan error, 1)
	go func() {
		inputErrCh <- enqueueChatInputs(runCtx, stdin, console, runtime)
	}()

	select {
	case err := <-inputErrCh:
		stopErr := runtime.Stop(context.Background())
		if err != nil {
			return err
		}
		if stopErr != nil {
			return stopErr
		}
		_, err = fmt.Fprintf(stdout, "\nTotal run time: %s\n", time.Since(startedAt).Round(time.Millisecond))
		return err
	case <-runCtx.Done():
		if err := runtime.Stop(context.Background()); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "\nRun stopped.\nTotal run time: %s\n", time.Since(startedAt).Round(time.Millisecond))
		return err
	case <-runtime.Done():
		if err := runtime.Err(); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "\nTotal run time: %s\n", time.Since(startedAt).Round(time.Millisecond))
		return err
	}
}

type localRunnerConfig struct {
	AgentID     string
	AppName     string
	Name        string
	Description string
	Instruction string
	WorkingDir  string
	UserID      string
	Logger      zerolog.Logger
}

func buildLocalRunner(ctx context.Context, factory *agentfactory.Factory, cfg localRunnerConfig) (taskmasterrt.Node, error) {
	sessionState, err := factory.BuildSessionState(cfg.AgentID, cfg.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("build session state for %s: %w", cfg.AgentID, err)
	}
	innerAgent, err := factory.Build(ctx, agentfactory.BuildRequest{
		AgentID:          cfg.AgentID,
		Name:             cfg.Name,
		Description:      cfg.Description,
		Instruction:      cfg.Instruction,
		WorkingDirectory: cfg.WorkingDir,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s agent: %w", cfg.AgentID, err)
	}
	localRunner, err := taskmasteradk.Wrap(innerAgent, taskmasteradk.Config{
		AppName:      cfg.AppName,
		UserID:       cfg.UserID,
		SessionState: sessionState,
		Logger:       cfg.Logger,
	})
	if err != nil {
		if closer, ok := innerAgent.(io.Closer); ok {
			_ = closer.Close()
		}
		return nil, err
	}
	return localRunner, nil
}

func newCodexACPConfig(command []string) agentconfig.Config {
	return agentconfig.Config{
		Type: agentconfig.AgentTypeCodexACP,
		CodexACP: &agentconfig.ACPConfig{
			Cmd:   append([]string(nil), command...),
			Model: "gpt-5.3-codex",
		},
	}
}

func autoAllowPermission(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	for _, option := range req.Options {
		if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
		}
	}
	for _, option := range req.Options {
		if option.Kind == acp.PermissionOptionKindRejectOnce || option.Kind == acp.PermissionOptionKindRejectAlways {
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId)}, nil
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}

func rootInstruction() string {
	return strings.Join([]string{
		"You are the generic Taskmaster inbox agent named taskmaster.",
		"You receive only plain-text task content as your turn input.",
		"Runtime task routing and bookkeeping are internal and are not shown to you directly.",
		"The host feeds you local fake-chat conversation turns while the run is active.",
		"The host may route your plain-text result back to the fake chat outbox.",
		"This generic run does not finish on your turn completion.",
		"It ends only when the host context is canceled, the input stream ends, or the operator quits the session.",
		"Do not impose a fixed workflow or phase order on the work.",
		"Do not invent extra routing protocol, schemas, or structured envelopes in prompts.",
		"Return only the useful result as plain text.",
		"Do not use JSON, field names, or code fences unless the task itself calls for them.",
	}, "\n")
}

func buildChatMessage(content string) *taskmasterrt.Message {
	if content == "" {
		return nil
	}
	message := &taskmasterrt.Message{
		SessionID: fakeChatSessionID,
		Kind:      taskmasterrt.MessageKindJob,
		From:      taskmasterrt.NewFakeChatHumanLocator(fakeChatID),
		To:        taskmasterrt.NewAgentLocator(taskmasterAgentID),
		Content:   content,
	}
	return message
}

func enqueueChatInputs(ctx context.Context, input io.Reader, console *chatConsole, runtime taskEnqueuer) error {
	if err := console.writePrompt(); err != nil {
		return err
	}

	scanner := bufio.NewScanner(input)
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line := scanner.Text()
		if strings.TrimSpace(line) == "/quit" {
			return nil
		}
		message := buildChatMessage(line)
		if message == nil {
			if err := console.writePrompt(); err != nil {
				return err
			}
			continue
		}
		if err := runtime.Enqueue(*message); err != nil {
			if writeErr := console.writeSystemLine("enqueue error: " + err.Error()); writeErr != nil {
				return writeErr
			}
			if writeErr := console.writePrompt(); writeErr != nil {
				return writeErr
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read fake chat input: %w", err)
	}
	return nil
}

type fakeChatTarget struct {
	console *chatConsole
}

func newFakeChatTarget(console *chatConsole) taskmasterrt.Target {
	return fakeChatTarget{console: console}
}

func (t fakeChatTarget) Supports(locator taskmasterrt.Locator) bool {
	return locator.Class == taskmasterrt.LocatorClassHuman &&
		locator.Transport == taskmasterrt.LocatorTransportFakeChat
}

func (t fakeChatTarget) DispatchMessage(_ context.Context, msg taskmasterrt.Message) error {
	return t.console.writeReply(msg.Content)
}

func routeRootOutcomeTo(target taskmasterrt.Locator) taskmasterrt.OutcomeRouter {
	return func(msg taskmasterrt.Message, outcome taskmasterrt.Outcome) []taskmasterrt.Message {
		if !isTaskmasterAgent(msg.To) {
			return nil
		}
		kind := taskmasterrt.MessageKindNotification
		content := outcome.Content
		if outcome.Err != nil || outcome.Status == taskmasterrt.OutcomeStatusFailed {
			kind = taskmasterrt.MessageKindError
			if outcome.Err != nil {
				content = outcome.Err.Error()
			}
		}
		return []taskmasterrt.Message{{
			Kind:    kind,
			From:    msg.To,
			To:      target,
			Content: content,
		}}
	}
}

func isTaskmasterAgent(locator taskmasterrt.Locator) bool {
	return locator.Class == taskmasterrt.LocatorClassAgent &&
		locator.Transport == taskmasterrt.LocatorTransportLocal &&
		locator.Key == taskmasterAgentID
}

type closeableNode interface {
	Close() error
}

func closeNode(node taskmasterrt.Node) {
	closer, ok := node.(closeableNode)
	if ok {
		_ = closer.Close()
	}
}

type chatConsole struct {
	writer io.Writer
	mu     sync.Mutex
}

func (c *chatConsole) writePrompt() error {
	return c.write("you> ")
}

func (c *chatConsole) writeReply(message string) error {
	if message == "" {
		message = "(empty task content)"
	}
	return c.write("taskmaster> " + message + "\nyou> ")
}

func (c *chatConsole) writeSystemLine(message string) error {
	return c.write(message + "\n")
}

func (c *chatConsole) write(message string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := io.WriteString(c.writer, message)
	return err
}

type shutdownAwareStderr struct {
	writer       io.Writer
	shuttingDown *atomic.Bool
	mu           sync.Mutex
	pending      []byte
}

func (w *shutdownAwareStderr) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.shuttingDown.Load() {
		if len(w.pending) == 0 {
			return w.writer.Write(p)
		}
		combined := append(append([]byte{}, w.pending...), p...)
		w.pending = nil
		_, err := w.writer.Write(combined)
		return len(p), err
	}

	combined := append(append([]byte{}, w.pending...), p...)
	w.pending = nil
	lines := strings.SplitAfter(string(combined), "\n")
	if len(lines) == 0 {
		return len(p), nil
	}

	for _, line := range lines {
		if line == "" {
			continue
		}
		if line[len(line)-1] != '\n' {
			w.pending = append(w.pending, []byte(line)...)
			continue
		}
		if strings.TrimSpace(line) == "Error: context canceled" {
			continue
		}
		if _, err := io.WriteString(w.writer, line); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}
