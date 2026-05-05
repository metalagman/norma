package goalkeeper

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

	acp "github.com/coder/acp-go-sdk"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/normahq/runtime/acpagent"
	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	defaultMaxToolCalls = 8
	defaultAgentType    = "codex_acp"
	defaultModel        = "gpt-5.3-codex"
	schedulerName       = "GoalkeeperScheduler"
)

var pdcaRoles = map[string]string{
	"plan":  "Create a concise plan for the assigned JOB. Return only the useful planning result.",
	"do":    "Execute the assigned JOB as far as possible. Return only the useful implementation result.",
	"check": "Check the assigned JOB result against the goal. Return PASS or FAIL with concise evidence.",
	"act":   "Decide the next action for the assigned JOB. Return close, continue, or replan with a concise reason.",
}

// Config configures a Goalkeeper playground run.
type Config struct {
	Goal         string
	WorkingDir   string
	BridgeBin    string
	MaxToolCalls int
	Stdout       io.Writer
	Stderr       io.Writer
	Logger       *zerolog.Logger
}

// Run executes one Goalkeeper playground run.
func Run(ctx context.Context, cfg Config) error {
	goal := strings.TrimSpace(cfg.Goal)
	if goal == "" {
		return errors.New("goal is required")
	}
	maxToolCalls := cfg.MaxToolCalls
	if maxToolCalls == 0 {
		maxToolCalls = defaultMaxToolCalls
	}
	if maxToolCalls < 0 {
		return fmt.Errorf("max tool calls must be >= 0")
	}

	workingDir := strings.TrimSpace(cfg.WorkingDir)
	if workingDir == "" {
		workingDir = "."
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
		Str("component", "playground.goalkeeper").
		Str("agent_type", defaultAgentType).
		Str("model", defaultModel).
		Logger()

	command := BuildCodexACPCommand(cfg.BridgeBin)
	roleSet, err := newRoleSet(ctx, roleSetConfig{
		Command:    command,
		WorkingDir: workingDir,
		Stderr:     stderr,
		Logger:     logger,
	})
	if err != nil {
		return err
	}
	defer roleSet.close()

	service := newService(roleSet, logger, maxToolCalls)
	server, err := startHTTPServer(ctx, service, "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer func() { _ = server.Close() }()

	scheduler, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              schedulerName,
		Description:       "Goalkeeper root scheduler agent",
		Model:             defaultModel,
		Command:           command,
		WorkingDir:        workingDir,
		Stderr:            stderr,
		PermissionHandler: autoAllowPermission,
		Logger:            &logger,
		Instruction:       schedulerInstruction(),
		MCPServers: map[string]acpagent.MCPServerConfig{
			"goalkeeper": {
				Type: acpagent.MCPServerTypeHTTP,
				URL:  "http://" + server.Addr,
			},
		},
	})
	if err != nil {
		return fmt.Errorf("create scheduler agent: %w", err)
	}
	defer func() { _ = scheduler.Close() }()

	logger.Info().Str("goal", goal).Msg("scheduler started")
	_, last, err := runOneTurn(ctx, runTurnInput{
		AppName:   "goalkeeper-scheduler",
		UserID:    "goalkeeper",
		SessionID: "goalkeeper-scheduler",
		Agent:     scheduler,
		Prompt:    "GOAL JOB:\n" + goal,
	})
	if err != nil {
		return fmt.Errorf("run scheduler: %w", err)
	}
	final := strings.TrimSpace(last)
	logger.Info().Bool("has_result", final != "").Str("result", final).Msg("scheduler completed")
	if final != "" {
		if _, err := fmt.Fprintln(stdout, final); err != nil {
			return err
		}
	}
	return nil
}

// BuildCodexACPCommand returns the Codex ACP command used by Goalkeeper agents.
func BuildCodexACPCommand(bridgeBin string) []string {
	if trimmed := strings.TrimSpace(bridgeBin); trimmed != "" {
		return []string{trimmed}
	}
	return []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
}

func schedulerInstruction() string {
	return strings.Join([]string{
		"You are the Goalkeeper root scheduler.",
		"You receive one GOAL JOB from the user.",
		"Use only the goalkeeper.run_job tool to run PDCA role JOBS on subagents.",
		"Available roles are plan, do, check, and act.",
		"Choose the calls yourself, but keep the MVP path simple: plan first, then do, then check, then act.",
		"Each tool call must include a stable job_id, the target role, and the task text for that role.",
		"Use previous role results when writing the next role task.",
		"After act returns, provide a concise final summary and stop.",
	}, "\n")
}

type roleSetConfig struct {
	Command    []string
	WorkingDir string
	Stderr     io.Writer
	Logger     zerolog.Logger
}

type roleSet struct {
	roles map[string]*roleSession
}

func newRoleSet(ctx context.Context, cfg roleSetConfig) (*roleSet, error) {
	roles := make(map[string]*roleSession, len(pdcaRoles))
	for role, instruction := range pdcaRoles {
		roleSession, err := newRoleSession(ctx, roleSessionConfig{
			Role:        role,
			Instruction: instruction,
			Command:     cfg.Command,
			WorkingDir:  cfg.WorkingDir,
			Stderr:      cfg.Stderr,
			Logger:      cfg.Logger,
		})
		if err != nil {
			for _, created := range roles {
				created.close()
			}
			return nil, err
		}
		roles[role] = roleSession
	}
	return &roleSet{roles: roles}, nil
}

func (r *roleSet) RunJob(ctx context.Context, jobID string, role string, task string) (string, error) {
	role = strings.ToLower(strings.TrimSpace(role))
	runner, ok := r.roles[role]
	if !ok {
		return "", fmt.Errorf("unknown role %q", role)
	}
	return runner.run(ctx, jobID, task)
}

func (r *roleSet) close() {
	for _, role := range r.roles {
		role.close()
	}
}

type roleSessionConfig struct {
	Role        string
	Instruction string
	Command     []string
	WorkingDir  string
	Stderr      io.Writer
	Logger      zerolog.Logger
}

type roleSession struct {
	mu             sync.Mutex
	agent          *acpagent.Agent
	runner         *adkrunner.Runner
	sessionService session.Service
	appName        string
	sessionID      string
	logger         zerolog.Logger
}

func newRoleSession(ctx context.Context, cfg roleSessionConfig) (*roleSession, error) {
	name := "Goalkeeper" + strings.ToUpper(cfg.Role[:1]) + cfg.Role[1:]
	logger := cfg.Logger.With().Str("role", cfg.Role).Logger()
	agentRuntime, err := acpagent.New(acpagent.Config{
		Context:           ctx,
		Name:              name,
		Description:       "Goalkeeper " + cfg.Role + " role agent",
		Model:             defaultModel,
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		Stderr:            cfg.Stderr,
		PermissionHandler: autoAllowPermission,
		Logger:            &logger,
		Instruction:       cfg.Instruction,
	})
	if err != nil {
		return nil, fmt.Errorf("create %s role agent: %w", cfg.Role, err)
	}
	appName := "goalkeeper-" + cfg.Role
	sessionService := session.InMemoryService()
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          agentRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create %s role runner: %w", cfg.Role, err)
	}
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    "goalkeeper",
		SessionID: appName,
	})
	if err != nil {
		_ = agentRuntime.Close()
		return nil, fmt.Errorf("create %s role session: %w", cfg.Role, err)
	}
	return &roleSession{
		agent:          agentRuntime,
		runner:         runner,
		sessionService: sessionService,
		appName:        appName,
		sessionID:      created.Session.ID(),
		logger:         logger,
	}, nil
}

func (r *roleSession) run(ctx context.Context, jobID string, task string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	jobLogger := r.logger.With().Str("job_id", jobID).Logger()
	_, last, err := runWithRunner(ctx, r.runner, r.sessionService, r.appName, "goalkeeper", r.sessionID, task, func(output string) {
		jobLogger.Debug().Str("output", output).Msg("job output")
	})
	return last, err
}

func (r *roleSession) close() {
	if r.agent != nil {
		_ = r.agent.Close()
	}
}

type runTurnInput struct {
	AppName   string
	UserID    string
	SessionID string
	Agent     agent.Agent
	Prompt    string
}

func runOneTurn(ctx context.Context, input runTurnInput) (session.Session, string, error) {
	sessionService := session.InMemoryService()
	runner, err := adkrunner.New(adkrunner.Config{
		AppName:        input.AppName,
		Agent:          input.Agent,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, "", err
	}
	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   input.AppName,
		UserID:    input.UserID,
		SessionID: input.SessionID,
	})
	if err != nil {
		return nil, "", err
	}
	finalSession, text, err := runWithRunner(ctx, runner, sessionService, input.AppName, input.UserID, created.Session.ID(), input.Prompt, nil)
	if err != nil {
		return nil, "", err
	}
	return finalSession, text, nil
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

type httpServerResult struct {
	Addr  string
	Close func() error
}

func startHTTPServer(ctx context.Context, service *service, addr string) (*httpServerResult, error) {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %q: %w", addr, err)
	}
	handler := mcp.NewStreamableHTTPHandler(func(_ *http.Request) *mcp.Server {
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

type syncWriter struct {
	mu     sync.Mutex
	writer io.Writer
}

func (w *syncWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}
