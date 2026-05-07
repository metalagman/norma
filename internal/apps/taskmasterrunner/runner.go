package taskmasterrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	taskmaster "github.com/normahq/norma/pkg/runtime/taskmaster"
	"github.com/normahq/runtime/agentconfig"
	"github.com/normahq/runtime/agentfactory"
	"github.com/normahq/runtime/mcpregistry"
	"github.com/rs/zerolog"
	"google.golang.org/adk/agent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const defaultModel = "gpt-5.3-codex"

type AgentSpec struct {
	Name        string
	Description string
	Instruction string
}

type RunnerConfig struct {
	AgentID     string
	AppName     string
	Name        string
	Description string
	Instruction string
	Command     []string
	WorkingDir  string
	Stderr      io.Writer
	Logger      zerolog.Logger
	UserID      string
	MCPServers  map[string]agentconfig.MCPServerConfig
}

type RunnerSetConfig struct {
	RootAgentID string
	Command     []string
	WorkingDir  string
	Stderr      io.Writer
	Logger      zerolog.Logger
	ChildAgents map[string]AgentSpec
}

type runner struct {
	mu             sync.Mutex
	inner          agent.Agent
	closer         io.Closer
	runner         *adkrunner.Runner
	sessionService session.Service
	appName        string
	userID         string
	logger         zerolog.Logger
	sessions       map[string]string
	sessionState   map[string]any
}

func BuildCodexACPCommand(bridgeBin string) []string {
	if trimmed := strings.TrimSpace(bridgeBin); trimmed != "" {
		return []string{trimmed}
	}
	return []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
}

func NewRunner(ctx context.Context, cfg RunnerConfig) (taskmaster.LocalRunner, error) {
	logger := cfg.Logger.With().
		Str("agent_id", cfg.AgentID).
		Str("agent_type", agentconfig.AgentTypeCodexACP).
		Str("model", defaultModel).
		Logger()

	registry := map[string]agentconfig.Config{
		cfg.AgentID: {
			Type: agentconfig.AgentTypeCodexACP,
			CodexACP: &agentconfig.ACPConfig{
				Cmd:   append([]string(nil), cfg.Command...),
				Model: defaultModel,
			},
		},
	}
	factoryOpts := []agentfactory.Option{
		agentfactory.WithPermissionHandler(autoAllowPermission),
	}
	if cfg.Stderr != nil {
		factoryOpts = append(factoryOpts, agentfactory.WithStderrWriter(cfg.Stderr))
	}
	factory := agentfactory.New(registry, mcpregistry.New(cfg.MCPServers), factoryOpts...)
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
		MCPServerIDs:     sortedMCPServerIDs(cfg.MCPServers),
	})
	if err != nil {
		return nil, fmt.Errorf("create %s agent: %w", cfg.AgentID, err)
	}

	sessionService := session.InMemoryService()
	adkRuntime, err := adkrunner.New(adkrunner.Config{
		AppName:        cfg.AppName,
		Agent:          innerAgent,
		SessionService: sessionService,
	})
	if err != nil {
		if closer, ok := innerAgent.(io.Closer); ok {
			_ = closer.Close()
		}
		return nil, fmt.Errorf("create %s runner: %w", cfg.AgentID, err)
	}

	result := &runner{
		inner:          innerAgent,
		runner:         adkRuntime,
		sessionService: sessionService,
		appName:        cfg.AppName,
		userID:         cfg.UserID,
		logger:         logger,
		sessions:       make(map[string]string),
		sessionState:   cloneSessionState(sessionState),
	}
	if closer, ok := innerAgent.(io.Closer); ok {
		result.closer = closer
	}
	return result, nil
}

func NewRunnerSet(ctx context.Context, cfg RunnerSetConfig) (map[string]taskmaster.LocalRunner, error) {
	runners := make(map[string]taskmaster.LocalRunner, len(cfg.ChildAgents))
	childIDs := make([]string, 0, len(cfg.ChildAgents))
	for agentID := range cfg.ChildAgents {
		childIDs = append(childIDs, agentID)
	}
	sort.Strings(childIDs)
	for _, agentID := range childIDs {
		child := cfg.ChildAgents[agentID]
		sessionRunner, err := NewRunner(ctx, RunnerConfig{
			AgentID:     agentID,
			AppName:     "taskmaster-" + agentID,
			Name:        child.Name,
			Description: child.Description,
			Instruction: child.Instruction,
			Command:     cfg.Command,
			WorkingDir:  cfg.WorkingDir,
			Stderr:      cfg.Stderr,
			Logger:      cfg.Logger,
			UserID:      cfg.RootAgentID,
		})
		if err != nil {
			for _, created := range runners {
				_ = created.Close()
			}
			return nil, err
		}
		runners[agentID] = sessionRunner
	}
	return runners, nil
}

func (r *runner) RunTask(ctx context.Context, task taskmaster.Task) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	resolvedSessionID, err := r.ensureSessionLocked(ctx, task.SessionID)
	if err != nil {
		return "", err
	}
	callLogger := r.logger.With().Str("call_id", task.ID).Logger()
	_, last, err := runWithRunner(ctx, r.runner, r.sessionService, r.appName, r.userID, resolvedSessionID, task.Content, func(output string) {
		callLogger.Debug().Str("output", output).Msg("task output")
	})
	return last, err
}

func (r *runner) ensureSessionLocked(ctx context.Context, sessionID string) (string, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return "", errors.New("session_id is required")
	}
	if resolved, ok := r.sessions[sessionID]; ok {
		return resolved, nil
	}
	if _, err := r.sessionService.Get(ctx, &session.GetRequest{
		AppName:   r.appName,
		UserID:    r.userID,
		SessionID: sessionID,
	}); err == nil {
		r.sessions[sessionID] = sessionID
		return sessionID, nil
	}
	created, err := r.sessionService.Create(ctx, &session.CreateRequest{
		AppName:   r.appName,
		UserID:    r.userID,
		SessionID: sessionID,
		State:     cloneSessionState(r.sessionState),
	})
	if err != nil {
		return "", err
	}
	r.sessions[sessionID] = created.Session.ID()
	return created.Session.ID(), nil
}

func (r *runner) Close() error {
	if r.closer == nil {
		return nil
	}
	return r.closer.Close()
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

func cloneSessionState(state map[string]any) map[string]any {
	if len(state) == 0 {
		return nil
	}
	cloned := make(map[string]any, len(state))
	for key, value := range state {
		cloned[key] = value
	}
	return cloned
}

func sortedMCPServerIDs(mcpServers map[string]agentconfig.MCPServerConfig) []string {
	if len(mcpServers) == 0 {
		return nil
	}
	ids := make([]string, 0, len(mcpServers))
	for id := range mcpServers {
		ids = append(ids, id)
	}
	sort.Strings(ids)
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
