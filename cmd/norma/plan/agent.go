package plancmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	acp "github.com/coder/acp-go-sdk"
	"github.com/normahq/norma/internal/agents/planner"
	"github.com/normahq/norma/internal/apps/tasksmcp"
	"github.com/normahq/norma/internal/config"
	"github.com/normahq/norma/internal/task"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
	"github.com/normahq/norma/pkg/runtime/agentfactory"
	"github.com/normahq/norma/pkg/runtime/mcpregistry"
	"github.com/spf13/cobra"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

const (
	plannerAppName       = "norma-planner"
	plannerUserID        = "norma-planner-user"
	plannerFollowupInput = "Your response? Ctrl+C to exit."
	plannerTasksMCPName  = "norma_tasks"
	plannerStateMCPName  = "norma_state"
)

func runAgentPlanner(
	cmd *cobra.Command,
	workingDir string,
	cfg plannerRuntimeConfig,
) error {
	plannerAgent, closePlannerAgent, err := createPlannerAgent(cmd.Context(), workingDir, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = closePlannerAgent() }()

	sess, err := startPlannerInteractive(cmd.Context(), plannerAgent, workingDir)
	if err != nil {
		return err
	}

	tuiModel, err := newPlannerModel(sess.Events, sess.Questions, sess.Responses, sess.Cancel)
	if err != nil {
		sess.Cancel()
		_ = sess.Wait()
		return fmt.Errorf("create TUI model: %w", err)
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

	runErr := sess.Wait()
	if runErr != nil {
		if errors.Is(runErr, context.Canceled) {
			if tuiErr := waitTUI(); tuiErr != nil {
				return fmt.Errorf("TUI error: %w", tuiErr)
			}
			return nil
		}
		prog.Send(planFailedMsg(formatPlannerRunError(runErr)))
		if tuiErr := waitTUI(); tuiErr != nil {
			return fmt.Errorf("TUI error: %w", tuiErr)
		}
		return nil
	}

	prog.Send(planCompletedMsg("Planner session ended by user."))
	if tuiErr := waitTUI(); tuiErr != nil {
		return fmt.Errorf("TUI error: %w", tuiErr)
	}

	fmt.Printf("\nPlanner session complete.\n")
	fmt.Printf("Planning run directory: %s\n", sess.RunDir)
	return nil
}

type plannerSession struct {
	RunDir    string
	Events    <-chan *session.Event
	Questions <-chan string
	Responses chan<- string
	Cancel    func()
	waitFn    func() error
}

func (s *plannerSession) Wait() error {
	if s == nil || s.waitFn == nil {
		return nil
	}
	return s.waitFn()
}

func startPlannerInteractive(ctx context.Context, ag adkagent.Agent, runDir string) (*plannerSession, error) {
	if ag == nil {
		return nil, fmt.Errorf("planner agent is required")
	}

	sessionService := session.InMemoryService()
	adkRunner, err := runner.New(runner.Config{
		AppName:        plannerAppName,
		Agent:          ag,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("create planner runner: %w", err)
	}

	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: plannerAppName,
		UserID:  plannerUserID,
	})
	if err != nil {
		return nil, fmt.Errorf("create planner session: %w", err)
	}

	runCtx, cancel := context.WithCancel(ctx)
	eventCh := make(chan *session.Event, 64)
	questionCh := make(chan string, 16)
	responseCh := make(chan string)
	doneCh := make(chan error, 1)

	go func() {
		defer close(eventCh)
		defer close(questionCh)
		defer close(doneCh)
		doneCh <- runPlannerLoop(runCtx, adkRunner, created.Session.ID(), eventCh, questionCh, responseCh)
	}()

	return &plannerSession{
		RunDir:    runDir,
		Events:    eventCh,
		Questions: questionCh,
		Responses: responseCh,
		Cancel:    cancel,
		waitFn: func() error {
			return <-doneCh
		},
	}, nil
}

func runPlannerLoop(
	ctx context.Context,
	adkRunner *runner.Runner,
	sessionID string,
	eventCh chan<- *session.Event,
	questionCh chan<- string,
	responseCh <-chan string,
) error {
	if !sendPlannerQuestion(ctx, questionCh, plannerIntroPrompt) {
		return ctx.Err()
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case prompt, ok := <-responseCh:
			if !ok {
				return nil
			}
			prompt = strings.TrimSpace(prompt)
			if prompt == "" {
				if !sendPlannerQuestion(ctx, questionCh, plannerFollowupInput) {
					return ctx.Err()
				}
				continue
			}

			events := adkRunner.Run(ctx, plannerUserID, sessionID, genai.NewContentFromText(prompt, genai.RoleUser), adkagent.RunConfig{})
			askedHuman := false
			seenQuestions := make(map[string]struct{})
			for ev, runErr := range events {
				if runErr != nil {
					return runErr
				}
				if ev != nil && !sendPlannerEvent(ctx, eventCh, ev) {
					return ctx.Err()
				}

				question := plannerQuestionFromEvent(ev)
				if question == "" {
					continue
				}
				if _, exists := seenQuestions[question]; exists {
					continue
				}
				seenQuestions[question] = struct{}{}
				askedHuman = true
				if !sendPlannerQuestion(ctx, questionCh, question) {
					return ctx.Err()
				}
			}
			if !askedHuman {
				if !sendPlannerQuestion(ctx, questionCh, plannerFollowupInput) {
					return ctx.Err()
				}
			}
		}
	}
}

func sendPlannerEvent(ctx context.Context, eventCh chan<- *session.Event, ev *session.Event) bool {
	select {
	case <-ctx.Done():
		return false
	case eventCh <- ev:
		return true
	}
}

func sendPlannerQuestion(ctx context.Context, questionCh chan<- string, question string) bool {
	question = strings.TrimSpace(question)
	if question == "" {
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case questionCh <- question:
		return true
	}
}

func plannerQuestionFromEvent(ev *session.Event) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	for _, part := range ev.Content.Parts {
		if part == nil || part.FunctionCall == nil {
			continue
		}
		if !isHumanToolCall(part.FunctionCall.Name, part.FunctionCall.Args) {
			continue
		}
		if q := lookupQuestion(part.FunctionCall.Args); q != "" {
			return q
		}
	}
	return ""
}

func isHumanToolCall(name string, args map[string]any) bool {
	if strings.Contains(strings.ToLower(strings.TrimSpace(name)), "human") {
		return true
	}
	kind, _ := args["kind"].(string)
	kind = strings.ToLower(strings.TrimSpace(kind))
	return strings.Contains(kind, "human") || strings.Contains(kind, "ask")
}

func lookupQuestion(payload map[string]any) string {
	for _, key := range []string{"question", "prompt", "text", "message", "title"} {
		if text := toTrimmedString(payload[key]); text != "" {
			return text
		}
	}
	raw, ok := payload["rawInput"].(map[string]any)
	if !ok {
		return ""
	}
	for _, key := range []string{"question", "prompt", "text", "message", "input"} {
		if text := toTrimmedString(raw[key]); text != "" {
			return text
		}
	}
	return ""
}

func toTrimmedString(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

func createPlannerAgent(
	ctx context.Context,
	workingDir string,
	cfg plannerRuntimeConfig,
) (adkagent.Agent, func() error, error) {
	return createPlannerAgentWithOptions(ctx, workingDir, cfg, plannerAgentCreateOptions{})
}

type plannerAgentCreateOptions struct {
	Stderr            io.Writer
	PermissionHandler func(context.Context, acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error)
}

func createPlannerAgentWithOptions(
	ctx context.Context,
	workingDir string,
	cfg plannerRuntimeConfig,
	options plannerAgentCreateOptions,
) (adkagent.Agent, func() error, error) {
	plannerID := strings.TrimSpace(cfg.PlannerProviderID)
	if plannerID == "" {
		return nil, nil, fmt.Errorf("planner agent id is required")
	}
	coordinatorAssignee := strings.TrimSpace(cfg.CoordinatorAssignee)
	if coordinatorAssignee == "" {
		return nil, nil, fmt.Errorf("planner coordinator assignee is required")
	}
	stderr := options.Stderr

	// Start embedded tasks MCP server over HTTP
	taskServer, err := startEmbeddedTaskServer(ctx, workingDir, coordinatorAssignee)
	if err != nil {
		return nil, nil, fmt.Errorf("start embedded tasks MCP server: %w", err)
	}

	plannerMCP, err := plannerMCPServers(workingDir, cfg.Runtime.Runtime.MCPServers, taskServer.Addr)
	if err != nil {
		_ = taskServer.Close()
		return nil, nil, err
	}

	factoryOpts := make([]agentfactory.Option, 0, 2)
	factoryOpts = append(factoryOpts, agentfactory.WithPermissionHandler(options.PermissionHandler))
	if stderr != nil {
		factoryOpts = append(factoryOpts, agentfactory.WithStderrWriter(stderr))
	}
	factory := agentfactory.New(
		cfg.Runtime.Runtime.Providers,
		mcpregistry.New(plannerMCP),
		factoryOpts...,
	)
	baseAgent, err := factory.Build(ctx, agentfactory.BuildRequest{
		AgentID:          plannerID,
		Name:             plannerID,
		Description:      "Norma planner base runtime",
		WorkingDirectory: workingDir,
	})
	if err != nil {
		_ = taskServer.Close()
		return nil, nil, fmt.Errorf("create planner base agent %q: %w", plannerID, err)
	}

	plannerAgent, err := planner.New(baseAgent, planner.Options{CoordinatorAssignee: coordinatorAssignee})
	if err != nil {
		if closer, ok := baseAgent.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
		_ = taskServer.Close()
		return nil, nil, fmt.Errorf("decorate planner agent %q: %w", plannerID, err)
	}

	closeFn := func() error {
		var firstErr error
		if closer, ok := plannerAgent.(interface{ Close() error }); ok {
			if err := closer.Close(); err != nil && firstErr == nil {
				firstErr = err
			}
		}
		if err := taskServer.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
		return firstErr
	}
	return plannerAgent, closeFn, nil
}

// startEmbeddedTaskServer starts an embedded tasks MCP server over HTTP with a random port.
func startEmbeddedTaskServer(ctx context.Context, workingDir string, coordinatorAssignee string) (*tasksmcp.HTTPServerResult, error) {
	trimmedWorkingDir := strings.TrimSpace(workingDir)
	if trimmedWorkingDir == "" {
		return nil, fmt.Errorf("working directory is required")
	}
	absoluteWorkingDir, err := filepath.Abs(trimmedWorkingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve working directory path %q: %w", trimmedWorkingDir, err)
	}

	tracker := task.NewBeadsTracker("")
	tracker.WorkingDir = absoluteWorkingDir

	return tasksmcp.StartHTTPServer(ctx, tasksmcp.NewCoordinatorAssigningTracker(tracker, coordinatorAssignee), "127.0.0.1:0")
}

func plannerMCPServers(workingDir string, configured map[string]config.MCPServerConfig, tasksServerAddr string) (map[string]agentconfig.MCPServerConfig, error) {
	_ = strings.TrimSpace(workingDir)

	merged := make(map[string]agentconfig.MCPServerConfig, len(configured)+1)
	for name, cfg := range configured {
		merged[name] = cfg
	}
	merged[plannerTasksMCPName] = agentconfig.MCPServerConfig{
		Type: agentconfig.MCPServerTypeHTTP,
		URL:  fmt.Sprintf("http://%s", tasksServerAddr),
	}
	return merged, nil
}

func formatPlannerRunError(err error) string {
	if err == nil {
		return "Planner run failed due to an unexpected error."
	}

	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "Planner run failed due to an unexpected error."
	}

	upper := strings.ToUpper(message)
	if strings.Contains(upper, "RESOURCE_EXHAUSTED") || strings.Contains(message, "429") {
		return "Planner model quota/rate limit exceeded.\n\n" + message + "\n\nTry again later or switch planner model/provider in .norma/config.yaml or .norma/cli.yaml."
	}
	return "Planner run failed.\n\n" + message
}
