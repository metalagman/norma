package pdcataskmaster

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	if trimmed := strings.TrimSpace(bridgeBin); trimmed != "" {
		return []string{trimmed}
	}
	return []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
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

	serviceLogger := baseLogger.With().Str("surface", "pdca-taskmaster").Logger()
	scheduleService := taskmastermcp.NewService(serviceLogger, taskmasterrt.NewAgentLocator(rootAgentID))
	finishRequested := &atomic.Bool{}
	stopAfterRootTurn := make(chan struct{}, 1)
	server, err := startPDCAHTTPServer(ctx, scheduleService, finishRequested)
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()

	command := BuildCodexACPCommand(cfg.BridgeBin)
	agentRegistry := map[string]agentconfig.Config{
		rootAgentID: newCodexACPConfig(command),
		"plan":      newCodexACPConfig(command),
		"do":        newCodexACPConfig(command),
		"check":     newCodexACPConfig(command),
		"act":       newCodexACPConfig(command),
	}
	mcpServers := map[string]agentconfig.MCPServerConfig{
		"taskmaster": {
			Type: agentconfig.MCPServerTypeHTTP,
			URL:  "http://" + server.addr,
		},
	}
	factoryOpts := []agentfactory.Option{
		agentfactory.WithPermissionHandler(autoAllowPermission),
	}
	if stderr != nil {
		factoryOpts = append(factoryOpts, agentfactory.WithStderrWriter(stderr))
	}
	factory := agentfactory.New(agentRegistry, mcpregistry.New(mcpServers), factoryOpts...)

	childRunners := make(map[string]taskmasterrt.LocalRunner, len(childAgentInstructions))
	childIDs := []string{"act", "check", "do", "plan"}
	slices.Sort(childIDs)
	for _, agentID := range childIDs {
		childRunner, buildErr := buildLocalRunner(ctx, factory, localRunnerConfig{
			AgentID:     agentID,
			AppName:     "taskmaster-" + agentID,
			Name:        "PDCATaskmaster" + upperFirst(agentID),
			Description: "PDCA " + agentID + " child agent",
			Instruction: childAgentInstructions[agentID],
			WorkingDir:  cfg.WorkingDir,
			UserID:      rootAgentID,
			Logger: baseLogger.With().
				Str("agent_id", agentID).
				Logger(),
		})
		if buildErr != nil {
			for _, created := range childRunners {
				_ = created.Close()
			}
			return buildErr
		}
		childRunners[agentID] = childRunner
	}

	rootRunnerInner, err := buildLocalRunner(ctx, factory, localRunnerConfig{
		AgentID:      rootAgentID,
		AppName:      "taskmaster-" + rootAgentID,
		Name:         "PDCATaskmaster",
		Description:  "Strict PDCA async task harness",
		Instruction:  rootInstruction(),
		WorkingDir:   cfg.WorkingDir,
		UserID:       rootAgentID,
		MCPServerIDs: sortedMCPServerIDs(mcpServers),
		Logger: baseLogger.With().
			Str("agent_id", rootAgentID).
			Logger(),
	})
	if err != nil {
		for _, runner := range childRunners {
			_ = runner.Close()
		}
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
		for _, runner := range localRunners {
			_ = runner.Close()
		}
		return err
	}
	scheduleService.SetController(runtime)
	if err := runtime.Start(ctx); err != nil {
		for _, runner := range localRunners {
			_ = runner.Close()
		}
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
		server := sdkmcp.NewServer(
			&sdkmcp.Implementation{Name: "norma-taskmaster", Version: "1.0.0"},
			&sdkmcp.ServerOptions{Instructions: "Use taskmaster.schedule_task to enqueue one task in the async run. Every scheduled task must include task_id, session_id, locator, optional report_to, and content."},
		)
		taskmastermcp.RegisterTools(server, service)
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

func upperFirst(value string) string {
	if value == "" {
		return ""
	}
	return strings.ToUpper(value[:1]) + value[1:]
}
