package pdcasync

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/normahq/go-adk-acpagent"
	"github.com/normahq/norma/internal/logging"
	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	defaultAgentType   = "codex_acp"
	defaultModel       = "gpt-5.3-codex"
	defaultIterations  = 5
	coordinatorAgentID = "coordinator"
	rootAgentName      = "PDCASyncCoordinator"

	mcpServerName      = "norma-pdca-sync"
	mcpServerVersion   = "1.0.0"
	promptSubagentTool = "pdca.prompt_subagent"
)

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
		"Your semantic output is the PDCA verdict for the current iteration: pass or fail.",
		"Return a concise plain-text assessment of whether the iteration passed or failed, with brief evidence.",
		"You may start with `verdict: pass` or `verdict: fail`, but this is optional.",
		"Do not act, replan, or execute more work.",
		"Do not use JSON, schemas, field names, or code fences.",
	}, "\n"),
	"act": strings.Join([]string{
		"You are the act phase of a strict PDCA flow.",
		"Consume only the check result for the current iteration.",
		"Your semantic output is the PDCA decision for the current iteration: close, continue, or replan.",
		"If the verdict is pass, the only legal decision is close.",
		"If the verdict is fail, the legal decisions are continue or replan.",
		"Never return rollback.",
		"Return a concise plain-text recommendation for whether the run should close, continue, or replan, with a brief reason.",
		"You may start with `decision: close`, `decision: continue`, or `decision: replan`, but this is optional.",
		"Do not use JSON, schemas, field names, or code fences.",
	}, "\n"),
}

type Config struct {
	Goal          string
	WorkingDir    string
	BridgeBin     string
	MaxIterations int
	Stdout        io.Writer
	Stderr        io.Writer
	Logger        *zerolog.Logger
}

type promptSubagentInput struct {
	AgentName string `json:"agent_name"`
	Prompt    string `json:"prompt"`
}

type promptSubagentOutput struct {
	AgentName string `json:"agent_name"`
	Iteration int    `json:"iteration"`
	Status    string `json:"status"`
	Result    string `json:"result,omitempty"`
	Message   string `json:"message,omitempty"`
}

type childInvoker interface {
	RunTask(ctx context.Context, callID string, prompt string) (string, error)
}

type closableRunner interface {
	childInvoker
	Close() error
}

type runnerSet interface {
	Runner(agentID string) childInvoker
	Close() error
}

type acpSession struct {
	mu             sync.Mutex
	agent          *acpagent.Agent
	runner         *adkrunner.Runner
	sessionService session.Service
	appName        string
	sessionID      string
	userID         string
	logger         zerolog.Logger
}

type acpSessionConfig struct {
	AgentID     string
	Name        string
	Description string
	Instruction string
	Command     []string
	WorkingDir  string
	Stderr      io.Writer
	Logger      zerolog.Logger
	MCPServers  map[string]acpagent.MCPServerConfig
}

type childSessions struct {
	agents map[string]closableRunner
}

type runtimeDeps struct {
	newRootSession func(context.Context, acpSessionConfig) (closableRunner, error)
	newChildSet    func(context.Context, childSessionSetConfig) (runnerSet, error)
	startServer    func(context.Context, *service, string) (*httpServerResult, error)
}

type childSessionSetConfig struct {
	Command    []string
	WorkingDir string
	Stderr     io.Writer
	Logger     zerolog.Logger
}

type service struct {
	logger        zerolog.Logger
	maxIterations int
	agents        map[string]childInvoker

	mu                sync.Mutex
	currentIteration  int
	actSeenThisIter   bool
	invocationCounter int
}

type httpServerResult struct {
	Addr  string
	Close func() error
}

func defaultDeps() runtimeDeps {
	return runtimeDeps{
		newRootSession: func(ctx context.Context, cfg acpSessionConfig) (closableRunner, error) {
			return newACPSession(ctx, cfg)
		},
		newChildSet: newChildSessions,
		startServer: startHTTPServer,
	}
}

func Run(ctx context.Context, cfg Config) error {
	return run(ctx, cfg, defaultDeps())
}

func run(ctx context.Context, cfg Config, deps runtimeDeps) error {
	startedAt := time.Now()
	goal := strings.TrimSpace(cfg.Goal)
	if goal == "" {
		return errors.New("goal is required")
	}
	workingDir := strings.TrimSpace(cfg.WorkingDir)
	if workingDir == "" {
		workingDir = "."
	}
	maxIterations := cfg.MaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultIterations
	}

	stdout := cfg.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stdout = &syncWriter{writer: stdout}

	stderr := cfg.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	stderr = &syncWriter{writer: stderr}

	baseLogger := cfg.Logger
	if baseLogger == nil {
		baseLogger = zerolog.Ctx(ctx)
	}
	logger := baseLogger.With().
		Str("component", "playground.pdca_sync").
		Str("agent_type", defaultAgentType).
		Str("model", defaultModel).
		Logger()

	command := BuildCodexACPCommand(cfg.BridgeBin)
	children, err := deps.newChildSet(ctx, childSessionSetConfig{
		Command:    command,
		WorkingDir: workingDir,
		Stderr:     stderr,
		Logger:     logger,
	})
	if err != nil {
		return err
	}
	defer func() { _ = children.Close() }()

	serviceLogger := logger.With().Str("surface", "pdca-sync").Logger()
	svc := newService(serviceLogger, maxIterations)
	for agentID := range childAgentInstructions {
		svc.agents[agentID] = children.Runner(agentID)
	}

	server, err := deps.startServer(ctx, svc, "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()

	root, err := deps.newRootSession(ctx, acpSessionConfig{
		AgentID:     coordinatorAgentID,
		Name:        rootAgentName,
		Description: "Synchronous PDCA coordinator root agent",
		Instruction: rootInstruction(maxIterations),
		Command:     command,
		WorkingDir:  workingDir,
		Stderr:      stderr,
		Logger:      logger,
		MCPServers: map[string]acpagent.MCPServerConfig{
			"pdca": {
				Type: acpagent.MCPServerTypeHTTP,
				URL:  "http://" + server.Addr,
			},
		},
	})
	if err != nil {
		return err
	}
	defer func() { _ = root.Close() }()

	initialPrompt := formatInitialGoalPrompt(goal)
	logger.Info().Str("goal", goal).Int("max_iterations", maxIterations).Msg("pdca-sync started")
	logger.Info().
		Str("agent_id", coordinatorAgentID).
		Str("call_id", "goal").
		Str("prompt", initialPrompt).
		Msg("agent received prompt")

	result, err := root.RunTask(ctx, "goal", initialPrompt)
	if err != nil {
		logger.Info().
			Str("agent_id", coordinatorAgentID).
			Str("call_id", "goal").
			Str("error", err.Error()).
			Msg("agent finished prompt")
		return err
	}
	logger.Info().
		Str("agent_id", coordinatorAgentID).
		Str("call_id", "goal").
		Str("result", strings.TrimSpace(result)).
		Msg("agent finished prompt")
	elapsed := formatElapsed(time.Since(startedAt))

	logger.Info().
		Bool("has_result", strings.TrimSpace(result) != "").
		Str("elapsed", elapsed).
		Str("result", strings.TrimSpace(result)).
		Msg("pdca-sync completed")
	return writeRunOutput(stdout, strings.TrimSpace(result), elapsed)
}

func newService(logger zerolog.Logger, maxIterations int) *service {
	if maxIterations <= 0 {
		maxIterations = defaultIterations
	}
	return &service{
		logger:           logger,
		maxIterations:    maxIterations,
		agents:           make(map[string]childInvoker, len(childAgentInstructions)),
		currentIteration: 1,
	}
}

func newChildSessions(ctx context.Context, cfg childSessionSetConfig) (runnerSet, error) {
	agents := make(map[string]closableRunner, len(childAgentInstructions))
	for agentID, instruction := range childAgentInstructions {
		session, err := newACPSession(ctx, acpSessionConfig{
			AgentID:     agentID,
			Name:        "PDCASync" + strings.ToUpper(agentID[:1]) + agentID[1:],
			Description: "PDCA sync " + agentID + " child agent",
			Instruction: instruction,
			Command:     cfg.Command,
			WorkingDir:  cfg.WorkingDir,
			Stderr:      cfg.Stderr,
			Logger:      cfg.Logger,
		})
		if err != nil {
			for _, created := range agents {
				_ = created.Close()
			}
			return nil, err
		}
		agents[agentID] = session
	}
	return &childSessions{agents: agents}, nil
}

func (s *childSessions) Runner(agentID string) childInvoker {
	return s.agents[agentID]
}

func (s *childSessions) Close() error {
	var errs []string
	for _, runner := range s.agents {
		if err := runner.Close(); err != nil {
			errs = append(errs, err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func newACPSession(ctx context.Context, cfg acpSessionConfig) (*acpSession, error) {
	logger := cfg.Logger.With().Str("agent_id", cfg.AgentID).Logger()
	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              cfg.Name,
		Description:       cfg.Description,
		Model:             defaultModel,
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		Stderr:            cfg.Stderr,
		PermissionHandler: autoAllowPermission,
		Logger:            logging.Slog().With("component", "app.pdcasync.acp", "agent_id", cfg.AgentID),
		Instruction:       cfg.Instruction,
		MCPServers:        cfg.MCPServers,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s agent: %w", cfg.AgentID, err)
	}

	appName := "pdca-sync-" + cfg.AgentID
	sessionService := session.InMemoryService()
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          agentRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create %s runner: %w", cfg.AgentID, err)
	}
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    coordinatorAgentID,
		SessionID: appName,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create %s session: %w", cfg.AgentID, err)
	}
	return &acpSession{
		agent:          agentRuntime,
		runner:         runner,
		sessionService: sessionService,
		appName:        appName,
		sessionID:      created.Session.ID(),
		userID:         coordinatorAgentID,
		logger:         logger,
	}, nil
}

func (s *acpSession) RunTask(ctx context.Context, callID string, prompt string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	callLogger := s.logger.With().Str("call_id", callID).Logger()
	_, last, err := runWithRunner(ctx, s.runner, s.sessionService, s.appName, s.userID, s.sessionID, prompt, func(output string) {
		callLogger.Debug().Str("output", output).Msg("prompt output")
	})
	return last, err
}

func (s *acpSession) Close() error {
	if s.agent == nil {
		return nil
	}
	return s.agent.Close()
}

func startHTTPServer(ctx context.Context, service *service, addr string) (*httpServerResult, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", addr, err)
	}
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return newMCPServer(service)
	}, &mcp.StreamableHTTPOptions{})
	httpServer := &http.Server{Handler: handler}
	go func() {
		<-ctx.Done()
		_ = httpServer.Close()
	}()
	go func() {
		_ = httpServer.Serve(listener)
	}()
	return &httpServerResult{
		Addr: listener.Addr().String(),
		Close: func() error {
			return httpServer.Close()
		},
	}, nil
}

func newMCPServer(service *service) *mcp.Server {
	server := mcp.NewServer(
		&mcp.Implementation{Name: mcpServerName, Version: mcpServerVersion},
		&mcp.ServerOptions{Instructions: "Use pdca.prompt_subagent to synchronously prompt one plain-text PDCA child agent."},
	)
	mcp.AddTool(server, &mcp.Tool{
		Name:        promptSubagentTool,
		Description: "Synchronously prompt one PDCA child agent by name and return its plain-text result.",
	}, service.promptSubagent)
	return server
}

func (s *service) promptSubagent(ctx context.Context, _ *mcp.CallToolRequest, input promptSubagentInput) (*mcp.CallToolResult, promptSubagentOutput, error) {
	agentName := strings.TrimSpace(input.AgentName)
	prompt := strings.TrimSpace(input.Prompt)
	if prompt == "" {
		out := promptSubagentOutput{AgentName: agentName, Iteration: s.currentIteration, Status: "error", Message: "prompt is required"}
		return toolError(out.Message), out, nil
	}

	runner, callID, iteration, err := s.prepareInvocation(agentName)
	if err != nil {
		out := promptSubagentOutput{AgentName: agentName, Iteration: iteration, Status: "error", Message: err.Error()}
		return toolError(out.Message), out, nil
	}

	s.logger.Info().
		Str("agent_id", agentName).
		Str("call_id", callID).
		Str("prompt", prompt).
		Int("iteration", iteration).
		Msg("agent received prompt")
	result, runErr := runner.RunTask(ctx, callID, prompt)
	if runErr != nil {
		s.logger.Info().
			Str("agent_id", agentName).
			Str("call_id", callID).
			Str("error", runErr.Error()).
			Int("iteration", iteration).
			Msg("agent finished prompt")
		out := promptSubagentOutput{
			AgentName: agentName,
			Iteration: iteration,
			Status:    "error",
			Message:   fmt.Sprintf("child agent %q failed: %v", agentName, runErr),
		}
		return toolError(out.Message), out, nil
	}

	s.logger.Info().
		Str("agent_id", agentName).
		Str("call_id", callID).
		Str("result", strings.TrimSpace(result)).
		Int("iteration", iteration).
		Msg("agent finished prompt")
	s.recordSuccessfulInvocation(agentName)

	out := promptSubagentOutput{
		AgentName: agentName,
		Iteration: iteration,
		Status:    "ok",
		Result:    strings.TrimSpace(result),
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: out.Result}},
	}, out, nil
}

func (s *service) prepareInvocation(agentName string) (childInvoker, string, int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	runner, ok := s.agents[agentName]
	if !ok {
		return nil, "", s.currentIteration, fmt.Errorf("unknown agent_name %q", agentName)
	}
	if agentName == "plan" && s.actSeenThisIter {
		if s.currentIteration >= s.maxIterations {
			return nil, "", s.currentIteration, fmt.Errorf("max_iterations %d exceeded", s.maxIterations)
		}
		s.currentIteration++
		s.actSeenThisIter = false
	}
	s.invocationCounter++
	callID := fmt.Sprintf("iter-%d-%s-%d", s.currentIteration, agentName, s.invocationCounter)
	return runner, callID, s.currentIteration, nil
}

func (s *service) recordSuccessfulInvocation(agentName string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if agentName == "act" {
		s.actSeenThisIter = true
	}
}

func rootInstruction(maxIterations int) string {
	return strings.Join([]string{
		"You are the coordinator root agent of a synchronous PDCA playground.",
		"You receive only prompt text as your turn input.",
		"You coordinate four plain-text child agents named plan, do, check, and act.",
		"The canonical PDCA roles are: plan, do, check, act.",
		"The canonical PDCA check verdict literals are `pass` and `fail`.",
		"The canonical PDCA act decision literals are `close`, `continue`, and `replan`.",
		"The legal verdict/decision pairs are `pass + close`, `fail + continue`, and `fail + replan`.",
		"The invalid verdict/decision pairs are `pass + continue`, `pass + replan`, and `fail + close`.",
		"Any `rollback` decision is invalid PDCA output.",
		"The semantic meaning is: `close` means the work is done, `continue` means run another PDCA iteration, and `replan` means the work is not ready to close and needs replanning.",
		"Child invocation is a blackbox runtime action.",
		"Use only the pdca.prompt_subagent tool to synchronously prompt one child agent at a time.",
		"The tool takes an agent name and a plain-text prompt, and returns that child agent's plain-text result.",
		"There is no task, envelope, queue, report, or finish protocol in this playground.",
		"The runtime does not enforce phase order. You decide which child agent to prompt next.",
		"When you prompt plan after an act call, that starts the next iteration.",
		fmt.Sprintf("The run is limited to %d iterations.", maxIterations),
		"Do not invent worker methodology, examples, commands, acceptance criteria, or execution instructions for child agents.",
		"Pass only the minimal plain-text context needed for the chosen child agent.",
		"Child outputs are freeform plain text. Do not require JSON, schemas, field names, or code fences.",
		"If child outputs include labels like `verdict:` or `decision:`, you may use them, but do not require them.",
		"When you are done, return your final plain-text answer directly instead of calling any finish tool.",
		"Do not read files, execute scripts, or do worker work yourself.",
	}, "\n")
}

func formatElapsed(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}

func writeRunOutput(stdout io.Writer, summary string, elapsed string) error {
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		if _, err := fmt.Fprintln(stdout, trimmed); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(stdout, "Total run time: %s\n", elapsed)
	return err
}

func formatInitialGoalPrompt(goal string) string {
	return "Goal:\n" + strings.TrimSpace(goal)
}

func BuildCodexACPCommand(bridgeBin string) []string {
	if trimmed := strings.TrimSpace(bridgeBin); trimmed != "" {
		return []string{trimmed}
	}
	return []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
}

func runWithRunner(
	ctx context.Context,
	runner *adkrunner.Runner,
	sessionService session.Service,
	appName string,
	userID string,
	sessionID string,
	prompt string,
	onOutput func(string),
) (session.Session, string, error) {
	var lastContent *genai.Content
	events := runner.Run(ctx, userID, sessionID, genai.NewContentFromText(prompt, genai.RoleUser), agent.RunConfig{})
	for ev, runErr := range events {
		if runErr != nil {
			return nil, "", runErr
		}
		if ev != nil && ev.Content != nil {
			lastContent = ev.Content
			output := contentText(ev.Content)
			if onOutput != nil && output != "" {
				onOutput(output)
			}
		}
	}
	finalSession, err := sessionService.Get(ctx, &session.GetRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: sessionID,
	})
	if err != nil {
		return nil, "", err
	}
	return finalSession.Session, contentText(lastContent), nil
}

func contentText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var parts []string
	for _, part := range content.Parts {
		if part != nil && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "\n\n")
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

func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: message}},
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
