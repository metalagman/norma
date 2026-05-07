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

	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
	taskmasteradk "github.com/normahq/norma/pkg/runtime/taskmaster/adk"
	taskmastermcp "github.com/normahq/norma/pkg/runtime/taskmaster/mcp"
	"github.com/normahq/runtime/acpagent"
	"github.com/rs/zerolog"
)

const (
	taskmasterAgentID  = "taskmaster"
	workerAgentID      = "worker"
	timerGoalMessage   = "hello world"
	timerGoalInterval  = 30 * time.Second
	defaultAgentName   = "Taskmaster"
	defaultWorkerName  = "TaskmasterWorker"
	defaultDescription = "Workflow-agnostic async task harness"
	initialTaskID      = "goal-task"
)

type Config struct {
	Goal       string
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
	return taskmasteradk.BuildCodexACPCommand(bridgeBin)
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

	command := taskmasteradk.BuildCodexACPCommand(cfg.BridgeBin)
	childRunners, err := taskmasteradk.NewRunnerSet(runCtx, taskmasteradk.RunnerSetConfig{
		RootAgentID: taskmasterAgentID,
		Command:     command,
		WorkingDir:  cfg.WorkingDir,
		Stderr:      filteredStderr,
		Logger:      baseLogger,
		ChildAgents: map[string]taskmasterrt.AgentConfig{
			workerAgentID: {
				Name:        defaultWorkerName,
				Description: "Generic async worker child agent",
				Instruction: workerInstruction,
			},
		},
	})
	if err != nil {
		return err
	}

	serviceLogger := baseLogger.With().Str("surface", "taskmaster").Logger()
	service := taskmastermcp.NewService(serviceLogger, taskmasterrt.NewAgentLocator(taskmasterAgentID))
	server, err := taskmastermcp.StartHTTPServer(runCtx, service, "127.0.0.1:0")
	if err != nil {
		for _, runner := range childRunners {
			_ = runner.Close()
		}
		return err
	}
	defer func() { _ = server.Close() }()

	rootRunner, err := taskmasteradk.NewRunner(runCtx, taskmasteradk.RunnerConfig{
		AgentID:     taskmasterAgentID,
		AppName:     "taskmaster-" + taskmasterAgentID,
		Name:        defaultAgentName,
		Description: defaultDescription,
		Instruction: rootInstruction(),
		Command:     command,
		WorkingDir:  cfg.WorkingDir,
		Stderr:      filteredStderr,
		Logger:      baseLogger,
		UserID:      taskmasterAgentID,
		MCPServers: map[string]acpagent.MCPServerConfig{
			"taskmaster": {
				Type: acpagent.MCPServerTypeHTTP,
				URL:  "http://" + server.Addr,
			},
		},
	})
	if err != nil {
		return err
	}

	localRunners := map[string]taskmasterrt.LocalRunner{
		taskmasterAgentID: rootRunner,
	}
	for id, runner := range childRunners {
		localRunners[id] = runner
	}

	runtime, err := taskmasterrt.New(taskmasterrt.Config{
		Logger:       &baseLogger,
		RootAgentID:  taskmasterAgentID,
		LocalRunners: localRunners,
		Targets:      []taskmasterrt.Target{taskmasterrt.NewCLILogTarget(baseLogger)},
	})
	if err != nil {
		return err
	}
	service.SetController(runtime)
	if err := runtime.Start(runCtx); err != nil {
		return err
	}

	if err := runtime.Enqueue(taskmasterrt.Task{
		ID:        initialTaskID,
		SessionID: initialTaskID,
		Locator:   taskmasterrt.NewAgentLocator(taskmasterAgentID),
		Content:   formatIngressContent(initialTaskID, taskmasterrt.NewCLIInputLocator(), cfg.Goal),
	}); err != nil {
		_ = runtime.Stop(context.Background())
		return err
	}
	go backgroundTaskSource(runCtx, runtime, timerGoalInterval, newTicker)

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

func rootInstruction() string {
	return strings.Join([]string{
		"You are the generic Taskmaster async root agent named taskmaster.",
		"You receive only plain-text task content as your turn input.",
		"Runtime task routing and bookkeeping are internal and are not shown to you directly.",
		"You coordinate one plain-text child agent named worker.",
		"Use only the taskmaster.schedule_task tool to enqueue worker tasks.",
		"Each scheduled task must include a stable task_id, the current session_id, a locator, an optional report_to, and content.",
		"Keep the same session_id when scheduling follow-up work for the same conversation.",
		"The only local child locator in this wrapper is {class: agent, transport: local, key: worker}.",
		"The report_to field uses the same locator schema as the target.",
		"The local root agent is {class: agent, transport: local, key: taskmaster}.",
		"The current log sink is {class: integration, transport: cli, key: log}.",
		"If you want async results to come back somewhere, set report_to to a registered target locator.",
		"The CLI ingress source is {class: integration, transport: cli, key: input}.",
		"The background timer source is {class: integration, transport: timer, key: default}.",
		"A background timer may also deliver simple hello world goals to you while the run is active.",
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
		"Prompt:",
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
			id := "timer-" + strconv.Itoa(counter)
			source := taskmasterrt.NewTimerSourceLocator()
			_ = runtime.Enqueue(taskmasterrt.Task{
				ID:        id,
				SessionID: id,
				Locator:   taskmasterrt.NewAgentLocator(taskmasterAgentID),
				Content:   formatIngressContent(id, source, timerGoalMessage),
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
