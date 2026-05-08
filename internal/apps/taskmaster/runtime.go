package taskmaster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"
	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
	taskmasteradk "github.com/normahq/norma/pkg/runtime/taskmaster/adk"
	"github.com/normahq/runtime/agentconfig"
	"github.com/normahq/runtime/agentfactory"
	"github.com/rs/zerolog"
)

const (
	taskmasterAgentID    = "taskmaster"
	timerContentMessage  = "hello world"
	timerContentInterval = 30 * time.Second
	defaultAgentName     = "Taskmaster"
	defaultDescription   = "Workflow-agnostic async task harness"
	bootstrapSessionID   = "cli-bootstrap"
)

type Config struct {
	WorkingDir string
	BridgeBin  string
	Stdout     io.Writer
	Stderr     io.Writer
	Logger     *zerolog.Logger
}

type ticker interface {
	Chan() <-chan time.Time
	Stop()
}

type realTicker struct {
	*time.Ticker
}

func (t realTicker) Chan() <-chan time.Time { return t.C }

var newTicker = func(d time.Duration) ticker {
	return realTicker{Ticker: time.NewTicker(d)}
}

func BuildCodexACPCommand(bridgeBin string) []string {
	if trimmed := strings.TrimSpace(bridgeBin); trimmed != "" {
		return []string{trimmed}
	}
	return []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
}

func Run(ctx context.Context, cfg Config, initialContent string) error {
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

	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	filteredStderr := &shutdownAwareStderr{writer: stderr, shuttingDown: shuttingDown}

	command := BuildCodexACPCommand(cfg.BridgeBin)
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
		AppName:     "taskmaster-" + taskmasterAgentID,
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

	localRunners := map[string]taskmasterrt.LocalRunner{
		taskmasterAgentID: rootRunner,
	}

	runtime, err := taskmasterrt.New(taskmasterrt.Config{
		Logger:       &baseLogger,
		RootAgentID:  taskmasterAgentID,
		LocalRunners: localRunners,
		Targets:      []taskmasterrt.Target{taskmasterrt.NewCLILogTarget(baseLogger)},
	})
	if err != nil {
		for _, runner := range localRunners {
			_ = runner.Close()
		}
		return err
	}
	if err := runtime.Start(runCtx); err != nil {
		for _, runner := range localRunners {
			_ = runner.Close()
		}
		return err
	}
	if bootstrapTask := buildBootstrapTask(initialContent); bootstrapTask != nil {
		if err := runtime.Enqueue(*bootstrapTask); err != nil {
			_ = runtime.Stop(context.Background())
			return err
		}
	}
	go backgroundTaskSource(runCtx, runtime, timerContentInterval, newTicker)

	startedAt := time.Now()
	select {
	case <-runCtx.Done():
		if err := runtime.Stop(context.Background()); err != nil {
			return err
		}
		if _, err := fmt.Fprintln(stdout, "Run stopped."); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "Total run time: %s\n", time.Since(startedAt).Round(time.Millisecond))
		return err
	case <-runtime.Done():
		if err := runtime.Err(); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "Total run time: %s\n", time.Since(startedAt).Round(time.Millisecond))
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

func buildLocalRunner(ctx context.Context, factory *agentfactory.Factory, cfg localRunnerConfig) (taskmasterrt.LocalRunner, error) {
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
		"The host may enqueue optional CLI bootstrap tasks and periodic timer tasks while the run is active.",
		"The host may also route your plain-text result to the current log sink.",
		"This generic run does not finish on your turn completion.",
		"It ends only when the host context is canceled, typically by a process signal such as SIGINT or SIGTERM.",
		"Do not impose a fixed workflow or phase order on the work.",
		"Do not invent extra routing protocol, schemas, or structured envelopes in prompts.",
		"Return only the useful result as plain text.",
		"Do not use JSON, field names, or code fences unless the task itself calls for them.",
	}, "\n")
}

func buildBootstrapTask(content string) *taskmasterrt.Task {
	if content == "" {
		return nil
	}
	reportTo := taskmasterrt.NewCLILogLocator()
	task := &taskmasterrt.Task{
		SessionID: bootstrapSessionID,
		Locator:   taskmasterrt.NewAgentLocator(taskmasterAgentID),
		ReportTo:  &reportTo,
		Content:   content,
	}
	return task
}

func backgroundTaskSource(ctx context.Context, runtime *taskmasterrt.Runtime, interval time.Duration, makeTicker func(time.Duration) ticker) {
	t := makeTicker(interval)
	defer t.Stop()
	counter := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.Chan():
			counter++
			sessionID := "timer-" + strconv.Itoa(counter)
			reportTo := taskmasterrt.NewCLILogLocator()
			_ = runtime.Enqueue(taskmasterrt.Task{
				SessionID: sessionID,
				Locator:   taskmasterrt.NewAgentLocator(taskmasterAgentID),
				ReportTo:  &reportTo,
				Content:   timerContentMessage,
			})
		}
	}
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
	lines := bytes.SplitAfter(combined, []byte("\n"))
	if len(lines) == 0 {
		return len(p), nil
	}

	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		if line[len(line)-1] != '\n' {
			w.pending = append(w.pending, line...)
			continue
		}
		if strings.TrimSpace(string(line)) == "Error: context canceled" {
			continue
		}
		if _, err := w.writer.Write(line); err != nil {
			return len(p), err
		}
	}
	return len(p), nil
}
