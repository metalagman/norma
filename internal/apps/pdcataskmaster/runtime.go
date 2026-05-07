package pdcataskmaster

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/normahq/norma/internal/apps/taskmasterrunner"
	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
	taskmastermcp "github.com/normahq/norma/pkg/runtime/taskmaster/mcp"
	"github.com/normahq/runtime/agentconfig"
	"github.com/rs/zerolog"
)

const rootAgentID = "pdca-taskmaster"

type Config struct {
	Goal       string
	WorkingDir string
	BridgeBin  string
	Stdout     io.Writer
	Stderr     io.Writer
	Logger     *zerolog.Logger
}

var childAgentInstructions = map[string]string{
	"plan": strings.Join([]string{
		"You are the plan phase of a strict PDCA flow.",
		"Work only on planning for the current iteration.",
		"Produce the next concise plain-text plan that the do phase should execute.",
		"Do not execute work, check results, or act on outcomes.",
		"Do not use JSON, schemas, field names, or code fences.",
		"Return only the useful planning result as plain text.",
	}, "\n"),
	"do": strings.Join([]string{
		"You are the do phase of a strict PDCA flow.",
		"Execute only the assigned plan for the current iteration.",
		"Do not replan, verify completion, or choose the next action.",
		"Do not use JSON, schemas, field names, or code fences.",
		"Return only the useful execution result for the check phase as plain text.",
	}, "\n"),
	"check": strings.Join([]string{
		"You are the check phase of a strict PDCA flow.",
		"Compare the execution result against the plan and the goal.",
		"Return a concise plain-text assessment of whether the iteration passed or failed, with brief evidence.",
		"You may start with `verdict: pass` or `verdict: fail`, but this is optional.",
		"Do not act, replan, or execute more work.",
		"Do not use JSON, schemas, field names, or code fences.",
	}, "\n"),
	"act": strings.Join([]string{
		"You are the act phase of a strict PDCA flow.",
		"Consume only the check result for the current iteration.",
		"Your output is advisory input for the root taskmaster agent.",
		"Return a concise plain-text recommendation for whether the run should stop or replan, with a brief reason.",
		"You may start with `decision: close` or `decision: replan`, but this is optional.",
		"Do not use JSON, schemas, field names, or code fences.",
	}, "\n"),
}

func BuildCodexACPCommand(bridgeBin string) []string {
	return taskmasterrunner.BuildCodexACPCommand(bridgeBin)
}

func Run(ctx context.Context, cfg Config) error {
	baseLogger := *zerolog.Ctx(ctx)
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

	command := taskmasterrunner.BuildCodexACPCommand(cfg.BridgeBin)
	childRunners, err := taskmasterrunner.NewRunnerSet(ctx, taskmasterrunner.RunnerSetConfig{
		RootAgentID: rootAgentID,
		Command:     command,
		WorkingDir:  cfg.WorkingDir,
		Stderr:      stderr,
		Logger:      baseLogger,
		ChildAgents: map[string]taskmasterrunner.AgentSpec{
			"plan": {
				Name:        "PDCATaskmasterPlan",
				Description: "PDCA plan child agent",
				Instruction: childAgentInstructions["plan"],
			},
			"do": {
				Name:        "PDCATaskmasterDo",
				Description: "PDCA do child agent",
				Instruction: childAgentInstructions["do"],
			},
			"check": {
				Name:        "PDCATaskmasterCheck",
				Description: "PDCA check child agent",
				Instruction: childAgentInstructions["check"],
			},
			"act": {
				Name:        "PDCATaskmasterAct",
				Description: "PDCA act child agent",
				Instruction: childAgentInstructions["act"],
			},
		},
	})
	if err != nil {
		return err
	}

	serviceLogger := baseLogger.With().Str("surface", "pdca-taskmaster").Logger()
	scheduleService := taskmastermcp.NewService(serviceLogger, taskmasterrt.NewAgentLocator(rootAgentID))
	finishRequested := &atomic.Bool{}
	stopAfterRootTurn := make(chan struct{}, 1)
	server, err := startPDCAHTTPServer(ctx, scheduleService, finishRequested)
	if err != nil {
		for _, runner := range childRunners {
			_ = runner.Close()
		}
		return err
	}
	defer func() { _ = server.Close() }()

	rootRunnerInner, err := taskmasterrunner.NewRunner(ctx, taskmasterrunner.RunnerConfig{
		AgentID:     rootAgentID,
		AppName:     "taskmaster-" + rootAgentID,
		Name:        "PDCATaskmaster",
		Description: "Strict PDCA async task harness",
		Instruction: rootInstruction(),
		Command:     command,
		WorkingDir:  cfg.WorkingDir,
		Stderr:      stderr,
		Logger:      baseLogger,
		UserID:      rootAgentID,
		MCPServers: map[string]agentconfig.MCPServerConfig{
			"taskmaster": {
				Type: agentconfig.MCPServerTypeHTTP,
				URL:  "http://" + server.addr,
			},
		},
	})
	if err != nil {
		return err
	}
	rootRunner := &finishAwareRunner{
		inner:           rootRunnerInner,
		finishRequested: finishRequested,
		stopReady:       stopAfterRootTurn,
	}

	localRunners := map[string]taskmasterrt.LocalRunner{rootAgentID: rootRunner}
	for id, runner := range childRunners {
		localRunners[id] = runner
	}

	runtime, err := taskmasterrt.New(taskmasterrt.Config{
		Logger:       &baseLogger,
		RootAgentID:  rootAgentID,
		LocalRunners: localRunners,
	})
	if err != nil {
		return err
	}
	scheduleService.SetController(runtime)
	if err := runtime.Start(ctx); err != nil {
		return err
	}

	if err := runtime.Enqueue(taskmasterrt.Task{
		ID:        "goal-task",
		SessionID: "goal-task",
		Locator:   taskmasterrt.NewAgentLocator(rootAgentID),
		Content:   formatIngressContent("goal-task", cfg.Goal),
	}); err != nil {
		_ = runtime.Stop(context.Background())
		return err
	}

	startedAt := time.Now()
	select {
	case <-stopAfterRootTurn:
		if err := runtime.Stop(context.Background()); err != nil {
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
	case <-ctx.Done():
		if err := runtime.Stop(context.Background()); err != nil {
			return err
		}
		_, err := fmt.Fprintf(stdout, "Total run time: %s\n", time.Since(startedAt).Round(time.Millisecond))
		return err
	}
}

func formatIngressContent(sessionID string, content string) string {
	return strings.Join([]string{
		"Session ID:",
		strings.TrimSpace(sessionID),
		"",
		"Goal:",
		strings.TrimSpace(content),
	}, "\n")
}

func rootInstruction() string {
	return strings.Join([]string{
		"You are the PDCA Taskmaster async root agent named pdca-taskmaster.",
		"You receive only plain-text task content as your turn input.",
		"Runtime task routing and bookkeeping are internal and are not shown to you directly.",
		"You are running a strict PDCA workflow over child agents.",
		"Run phases in this exact order for each iteration: plan -> do -> check -> act.",
		"Always start a new goal with plan. Do not skip phases and do not reorder them.",
		"Use only the taskmaster.schedule_task tool to enqueue child-agent tasks, and taskmaster.finish to request stop after the current root turn.",
		"Each scheduled task must include a stable task_id, the current session_id, a locator, an optional report_to, and content.",
		"Keep the same session_id when continuing the same PDCA conversation.",
		"The report_to field means where async task results should be reported.",
		"The local root agent locator is {class: agent, transport: local, key: pdca-taskmaster}.",
		"The child agent locators are local agent locators with ids plan, do, check, and act.",
		"The child agents available in this wrapper are plan, do, check, and act.",
		"Treat plan, do, check, and act as strict PDCA phases, not generic workers.",
		"After a plan completion, schedule do. After a do completion, schedule check. After a check completion, schedule act.",
		"When handing work to a child agent, do not author task-specific methodology, examples, commands, acceptance criteria, or execution instructions yourself.",
		"The child agent's own system prompt defines how that phase works.",
		"For plan, pass only the raw goal text.",
		"For do, pass only the prior plan output.",
		"For check, pass only neutral sections with the raw upstream texts: Goal:, Plan output:, Do output:.",
		"For act, pass only a neutral Check output: section.",
		"Neutral section headers are allowed only to separate raw prior texts. Do not add new guidance around them.",
		"Child agents return freeform plain text, not structured role payloads.",
		"Do not expect JSON, field names, or code fences from child agents.",
		"You interpret check and act outputs semantically from their plain text.",
		"If a child output happens to include helpful labels like `verdict:` or `decision:`, you may use them, but do not require them.",
		"If an act output clearly recommends close, call taskmaster.finish.",
		"If an act output clearly recommends replan, more planning is required before further execution.",
		"You decide the next child task yourself from the prompt text you receive. Do not treat child outputs as direct runtime commands.",
		"Do not read files, execute scripts, or perform worker work yourself.",
		"Only coordinate the PDCA flow through child-agent tasks.",
		"Do not try to deliver work directly without using taskmaster.schedule_task.",
	}, "\n")
}

type pdcaHTTPServer struct {
	addr       string
	httpServer *http.Server
}

func startPDCAHTTPServer(ctx context.Context, service *taskmastermcp.Service, finishRequested *atomic.Bool) (*pdcaHTTPServer, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("listen: %w", err)
	}
	handler := sdkmcp.NewStreamableHTTPHandler(func(_ *http.Request) *sdkmcp.Server {
		server := taskmastermcp.NewServer(service)
		sdkmcp.AddTool(server, &sdkmcp.Tool{
			Name:        "taskmaster.finish",
			Description: "Request runtime stop after the current root turn returns.",
		}, func(_ context.Context, _ *sdkmcp.CallToolRequest, _ struct{}) (*sdkmcp.CallToolResult, map[string]string, error) {
			finishRequested.Store(true)
			return &sdkmcp.CallToolResult{
				Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "finish requested"}},
			}, map[string]string{"status": "requested"}, nil
		})
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
	return &pdcaHTTPServer{
		addr:       listener.Addr().String(),
		httpServer: httpServer,
	}, nil
}

func (s *pdcaHTTPServer) Close() error {
	if s == nil || s.httpServer == nil {
		return nil
	}
	return s.httpServer.Close()
}

type finishAwareRunner struct {
	inner           taskmasterrt.LocalRunner
	finishRequested *atomic.Bool
	stopReady       chan struct{}
	once            sync.Once
}

func (r *finishAwareRunner) RunTask(ctx context.Context, task taskmasterrt.Task) (string, error) {
	output, err := r.inner.RunTask(ctx, task)
	if r.finishRequested.Load() {
		r.once.Do(func() {
			select {
			case r.stopReady <- struct{}{}:
			default:
			}
		})
	}
	return output, err
}

func (r *finishAwareRunner) Close() error {
	return r.inner.Close()
}
