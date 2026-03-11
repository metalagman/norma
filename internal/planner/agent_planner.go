package planner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	acp "github.com/coder/acp-go-sdk"
	"github.com/metalagman/norma/internal/adk/agentfactory"
	"github.com/metalagman/norma/internal/config"
	"github.com/metalagman/norma/internal/planner/llmtools"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// AgentPlanner runs interactive planner conversations using configured agent runtimes.
type AgentPlanner struct {
	repoRoot  string
	registry  map[string]config.AgentConfig
	plannerID string
}

// NewAgentPlanner constructs a new planner runtime.
func NewAgentPlanner(repoRoot string, registry map[string]config.AgentConfig, plannerID string) *AgentPlanner {
	return &AgentPlanner{
		repoRoot:  repoRoot,
		registry:  registry,
		plannerID: plannerID,
	}
}

// RunInteractive starts a planner session in TUI mode.
func (p *AgentPlanner) RunInteractive(ctx context.Context, req Request) (string, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	planRunDir, err := newPlanRunDir(p.repoRoot)
	if err != nil {
		return "", err
	}

	eventChan := make(chan *session.Event, 100)
	questionChan := make(chan string)
	responseChan := make(chan string)

	tuiModel, err := newPlannerModel(eventChan, questionChan, responseChan, cancel)
	if err != nil {
		return "", fmt.Errorf("create TUI model: %w", err)
	}
	prog := tea.NewProgram(tuiModel, tea.WithAltScreen())

	tuiErrChan := make(chan error, 1)
	go func() {
		if runErr := RunTUI(prog); runErr != nil {
			tuiErrChan <- runErr
		}
		close(tuiErrChan)
	}()

	var waitTUIOnce sync.Once
	var waitTUIErr error
	waitTUI := func() error {
		waitTUIOnce.Do(func() {
			if runErr, ok := <-tuiErrChan; ok {
				waitTUIErr = runErr
			}
		})
		return waitTUIErr
	}

	var closeEventOnce sync.Once
	closeEvents := func() {
		closeEventOnce.Do(func() {
			close(eventChan)
			close(questionChan)
		})
	}

	factory := agentfactory.NewFactory(p.registry)

	beadsTool, err := llmtools.NewBeadsCommandTool(p.repoRoot)
	if err != nil {
		closeEvents()
		_ = waitTUI()
		return "", fmt.Errorf("create beads tool: %w", err)
	}

	creationReq := agentfactory.CreationRequest{
		Name:              "NormaPlannerAgent",
		Description:       "Norma planner via generic agent runtime",
		SystemInstruction: PlannerInstruction(),
		WorkingDirectory:  p.repoRoot,
		Stderr:            io.Discard,
		PermissionHandler: PlannerPermissionHandler,
		Tools:             []tool.Tool{beadsTool},
	}

	agentRuntime, err := factory.CreateAgent(runCtx, p.plannerID, creationReq)
	if err != nil {
		closeEvents()
		_ = waitTUI()
		return "", fmt.Errorf("create planner runtime: %w", err)
	}
	if closer, ok := agentRuntime.(interface{ Close() error }); ok {
		defer func() { _ = closer.Close() }()
	}

	sessionService := session.InMemoryService()
	adkRunner, err := runner.New(runner.Config{
		AppName:        "norma-plan-agent",
		Agent:          agentRuntime,
		SessionService: sessionService,
	})
	if err != nil {
		closeEvents()
		_ = waitTUI()
		return "", fmt.Errorf("create planner runner: %w", err)
	}

	sess, err := sessionService.Create(runCtx, &session.CreateRequest{
		AppName: "norma-plan-agent",
		UserID:  "norma-planner-user",
	})
	if err != nil {
		closeEvents()
		_ = waitTUI()
		return "", fmt.Errorf("create planner session: %w", err)
	}

	runTurn := func(prompt string) error {
		events := adkRunner.Run(
			runCtx,
			"norma-planner-user",
			sess.Session.ID(),
			genai.NewContentFromText(prompt, genai.RoleUser),
			adkagent.RunConfig{},
		)
		for ev, runErr := range events {
			if runErr != nil {
				return fmt.Errorf("planner turn failed: %w", runErr)
			}
			if ev == nil {
				continue
			}
			select {
			case eventChan <- ev:
			case <-runCtx.Done():
				return runCtx.Err()
			}
		}
		return nil
	}

	if goal := strings.TrimSpace(req.EpicDescription); goal != "" {
		if turnErr := runTurn(goal); turnErr != nil {
			if errors.Is(turnErr, context.Canceled) {
				closeEvents()
				_ = waitTUI()
				return "", context.Canceled
			}
			closeEvents()
			prog.Send(planFailedMsg(formatPlannerRunError(turnErr)))
			if tuiErr := waitTUI(); tuiErr != nil {
				return "", fmt.Errorf("TUI error: %w", tuiErr)
			}
			return "", ErrHandledInTUI
		}
	}

	for {
		select {
		case questionChan <- "What do you want to build? Ctrl+C to exit.":
		case <-runCtx.Done():
			closeEvents()
			_ = waitTUI()
			return "", context.Canceled
		}

		var input string
		select {
		case input = <-responseChan:
		case <-runCtx.Done():
			closeEvents()
			_ = waitTUI()
			return "", context.Canceled
		}

		message := strings.TrimSpace(input)
		if message == "" {
			continue
		}
		switch strings.ToLower(message) {
		case "exit", "quit":
			closeEvents()
			prog.Send(planCompletedMsg("Planner session ended by user."))
			if tuiErr := waitTUI(); tuiErr != nil {
				return "", fmt.Errorf("TUI error: %w", tuiErr)
			}
			return planRunDir, nil
		}

		if turnErr := runTurn(message); turnErr != nil {
			if errors.Is(turnErr, context.Canceled) {
				closeEvents()
				_ = waitTUI()
				return "", context.Canceled
			}
			closeEvents()
			prog.Send(planFailedMsg(formatPlannerRunError(turnErr)))
			if tuiErr := waitTUI(); tuiErr != nil {
				return "", fmt.Errorf("TUI error: %w", tuiErr)
			}
			return "", ErrHandledInTUI
		}
	}
}

func newPlanRunDir(repoRoot string) (string, error) {
	sfx, err := randomHex(3)
	if err != nil {
		return "", fmt.Errorf("generate planning run id: %w", err)
	}
	runID := fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102-150405"), sfx)
	runDir := filepath.Join(repoRoot, ".norma", "plans", runID)
	if err := os.MkdirAll(filepath.Join(runDir, "logs"), 0o700); err != nil {
		return "", fmt.Errorf("create planning logs dir: %w", err)
	}
	return runDir, nil
}

// PlannerPermissionHandler enforces planner-safe ACP permissions.
func PlannerPermissionHandler(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if req.ToolCall.Kind != nil {
		switch *req.ToolCall.Kind {
		case acp.ToolKindEdit, acp.ToolKindDelete, acp.ToolKindMove:
			if resp, ok := selectPermissionOption(req.Options, acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways); ok {
				return resp, nil
			}
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
		}
	}
	if resp, ok := selectPermissionOption(req.Options, acp.PermissionOptionKindAllowOnce, acp.PermissionOptionKindAllowAlways); ok {
		return resp, nil
	}
	if resp, ok := selectPermissionOption(req.Options, acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways); ok {
		return resp, nil
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
}

func selectPermissionOption(options []acp.PermissionOption, preferredKinds ...acp.PermissionOptionKind) (acp.RequestPermissionResponse, bool) {
	for _, kind := range preferredKinds {
		for _, option := range options {
			if option.Kind != kind {
				continue
			}
			return acp.RequestPermissionResponse{
				Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId),
			}, true
		}
	}
	return acp.RequestPermissionResponse{}, false
}
