package taskmaster

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
	taskmasteradk "github.com/normahq/norma/pkg/runtime/taskmaster/adk"
	taskmastermcp "github.com/normahq/norma/pkg/runtime/taskmaster/mcp"
	"github.com/normahq/runtime/agentconfig"
	"github.com/normahq/runtime/agentfactory"
	"github.com/normahq/runtime/mcpregistry"
	"github.com/rs/zerolog"
)

const (
	taskmasterAgentID    = "taskmaster"
	workerAgentID        = "worker"
	timerContentMessage  = "hello world"
	timerContentInterval = 30 * time.Second
	defaultAgentName     = "Taskmaster"
	defaultWorkerName    = "TaskmasterWorker"
	defaultDescription   = "Workflow-agnostic async task harness"
	initialSessionID     = "content-session"
)

type Config struct {
	Content    string
	WorkingDir string
	BridgeBin  string
	Stdout     io.Writer
	Stderr     io.Writer
	Logger     *zerolog.Logger
}

var workerInstruction = strings.Join([]string{
	"You are a generic plain-text worker in an async task harness.",
	"Execute the assigned prompt as directly as you can.",
	"Return only the useful result as plain text.",
	"Do not use JSON, schemas, field names, or code fences unless the prompt explicitly requires them.",
}, "\n")

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

	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	filteredStderr := &shutdownAwareStderr{writer: stderr, shuttingDown: shuttingDown}

	serviceLogger := baseLogger.With().Str("surface", "taskmaster").Logger()
	service := taskmastermcp.NewService(serviceLogger, taskmasterrt.NewAgentLocator(taskmasterAgentID))
	server, err := startTaskmasterHTTPServer(runCtx, service)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()

	command := BuildCodexACPCommand(cfg.BridgeBin)
	agentRegistry := map[string]agentconfig.Config{
		taskmasterAgentID: newCodexACPConfig(command),
		workerAgentID:     newCodexACPConfig(command),
	}
	mcpServers := map[string]agentconfig.MCPServerConfig{
		"taskmaster": {
			Type: agentconfig.MCPServerTypeHTTP,
			URL:  "http://" + server.addr,
		},
	}
	factoryOpts := []agentfactory.Option{
		agentfactory.WithPermissionHandler(autoAllowPermission),
		agentfactory.WithStderrWriter(filteredStderr),
	}
	factory := agentfactory.New(agentRegistry, mcpregistry.New(mcpServers), factoryOpts...)

	childRunner, err := buildLocalRunner(runCtx, factory, localRunnerConfig{
		AgentID:     workerAgentID,
		AppName:     "taskmaster-" + workerAgentID,
		Name:        defaultWorkerName,
		Description: "Generic async worker child agent",
		Instruction: workerInstruction,
		WorkingDir:  cfg.WorkingDir,
		UserID:      taskmasterAgentID,
		Logger: baseLogger.With().
			Str("agent_id", workerAgentID).
			Logger(),
	})
	if err != nil {
		return err
	}
	rootRunner, err := buildLocalRunner(runCtx, factory, localRunnerConfig{
		AgentID:      taskmasterAgentID,
		AppName:      "taskmaster-" + taskmasterAgentID,
		Name:         defaultAgentName,
		Description:  defaultDescription,
		Instruction:  rootInstruction(),
		WorkingDir:   cfg.WorkingDir,
		UserID:       taskmasterAgentID,
		MCPServerIDs: sortedMCPServerIDs(mcpServers),
		Logger: baseLogger.With().
			Str("agent_id", taskmasterAgentID).
			Logger(),
	})
	if err != nil {
		_ = childRunner.Close()
		return err
	}

	localRunners := map[string]taskmasterrt.LocalRunner{
		taskmasterAgentID: rootRunner,
		workerAgentID:     childRunner,
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
	service.SetController(runtime)
	if err := runtime.Start(runCtx); err != nil {
		for _, runner := range localRunners {
			_ = runner.Close()
		}
		return err
	}

	if err := runtime.Enqueue(taskmasterrt.Task{
		SessionID: initialSessionID,
		Locator:   taskmasterrt.NewAgentLocator(taskmasterAgentID),
		Content:   formatIngressContent(initialSessionID, taskmasterrt.NewCLIInputLocator(), cfg.Content),
	}); err != nil {
		_ = runtime.Stop(context.Background())
		return err
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
	AgentID      string
	AppName      string
	Name         string
	Description  string
	Instruction  string
	WorkingDir   string
	UserID       string
	MCPServerIDs []string
	Logger       zerolog.Logger
}

type taskmasterHTTPServer struct {
	addr       string
	httpServer *http.Server
}

func startTaskmasterHTTPServer(ctx context.Context, service *taskmastermcp.Service) (*taskmasterHTTPServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	handler := sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server {
		server := sdkmcp.NewServer(
			&sdkmcp.Implementation{Name: "norma-taskmaster", Version: "1.0.0"},
			&sdkmcp.ServerOptions{Instructions: "Use taskmaster.schedule_task to enqueue one task in the async run. Every scheduled task must include session_id, locator, optional report_to, and content."},
		)
		taskmastermcp.RegisterTools(server, service)
		return server
	}, &sdkmcp.StreamableHTTPOptions{})
	httpServer := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()
	go func() {
		_ = httpServer.Serve(listener)
	}()
	return &taskmasterHTTPServer{
		addr:       listener.Addr().String(),
		httpServer: httpServer,
	}, nil
}

func (s *taskmasterHTTPServer) Close() error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
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
		MCPServerIDs:     append([]string(nil), cfg.MCPServerIDs...),
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

func sortedMCPServerIDs(mcpServers map[string]agentconfig.MCPServerConfig) []string {
	if len(mcpServers) == 0 {
		return nil
	}
	ids := make([]string, 0, len(mcpServers))
	for id := range mcpServers {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	return ids
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
		"You are the generic Taskmaster async root agent named taskmaster.",
		"You receive only plain-text task content as your turn input.",
		"Runtime task routing and bookkeeping are internal and are not shown to you directly.",
		"You coordinate one plain-text child agent named worker.",
		"Use only the taskmaster.schedule_task tool to enqueue worker tasks.",
		"Each scheduled task must include the current session_id, a locator, an optional report_to, and content.",
		"Keep the same session_id when scheduling follow-up work for the same conversation.",
		"The only local child locator in this wrapper is {class: agent, transport: local, key: worker}.",
		"The report_to field uses the same locator schema as the target.",
		"The local root agent is {class: agent, transport: local, key: taskmaster}.",
		"The current log sink is {class: integration, transport: cli, key: log}.",
		"If you want async results to come back somewhere, set report_to to a registered target locator.",
		"The CLI ingress source is {class: integration, transport: cli, key: input}.",
		"The background timer source is {class: integration, transport: timer, key: default}.",
		"A background timer may also deliver simple hello world task content to you while the run is active.",
		"This generic run does not finish on your turn completion.",
		"It ends only when the host context is canceled, typically by a process signal such as SIGINT or SIGTERM.",
		"Do not impose a fixed workflow or phase order on the work.",
		"Pass only the minimal plain-text context needed for the worker task.",
		"Do not invent extra routing protocol, schemas, or structured envelopes in prompts.",
		"The worker returns freeform plain text. Do not require JSON, field names, or code fences unless the task itself calls for them.",
		"Do not do worker work yourself. Only coordinate tasks through the worker.",
	}, "\n")
}

func formatIngressContent(sessionID string, source taskmasterrt.Locator, content string) string {
	return strings.Join([]string{
		"Session ID:",
		strings.TrimSpace(sessionID),
		"",
		"Source:",
		locatorText(source),
		"",
		"Content:",
		strings.TrimSpace(content),
	}, "\n")
}

func locatorText(locator taskmasterrt.Locator) string {
	return strings.Join([]string{locator.Class, locator.Transport, locator.Key}, "/")
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
			source := taskmasterrt.NewTimerSourceLocator()
			_ = runtime.Enqueue(taskmasterrt.Task{
				SessionID: sessionID,
				Locator:   taskmasterrt.NewAgentLocator(taskmasterAgentID),
				Content:   formatIngressContent(sessionID, source, timerContentMessage),
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
