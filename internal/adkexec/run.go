package adkexec

import (
	"context"
	"fmt"

	"google.golang.org/adk/agent"
	adkrunner "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
)

// RunInput defines shared execution parameters for running an ADK agent.
type RunInput struct {
	AppName      string
	UserID       string
	SessionID    string
	Agent        agent.Agent
	InitialState map[string]any
	OnEvent      func(*session.Event)
}

// Run executes an ADK agent and returns the final session state.
func Run(ctx context.Context, input RunInput) (session.Session, error) {
	if input.Agent == nil {
		return nil, fmt.Errorf("agent is required")
	}

	appName := input.AppName
	if appName == "" {
		appName = "norma"
	}
	userID := input.UserID
	if userID == "" {
		userID = "norma-user"
	}

	sessionService := session.InMemoryService()
	r, err := adkrunner.New(adkrunner.Config{
		AppName:        appName,
		Agent:          input.Agent,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK runner: %w", err)
	}

	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: input.SessionID,
		State:     input.InitialState,
	})
	if err != nil {
		return nil, fmt.Errorf("create ADK session: %w", err)
	}

	events := r.Run(ctx, userID, created.Session.ID(), nil, agent.RunConfig{})
	for ev, runErr := range events {
		if runErr != nil {
			return nil, runErr
		}
		if input.OnEvent != nil && ev != nil {
			input.OnEvent(ev)
		}
	}

	finalSess, err := sessionService.Get(ctx, &session.GetRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: created.Session.ID(),
	})
	if err != nil {
		return nil, fmt.Errorf("get ADK session: %w", err)
	}

	return finalSess.Session, nil
}
