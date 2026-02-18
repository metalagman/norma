package adkrunner

import (
	"context"
	"fmt"

	"google.golang.org/adk/agent"
	adk "google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// RunInput defines shared execution parameters for running an ADK agent.
type RunInput struct {
	AppName        string
	UserID         string
	SessionID      string
	Agent          agent.Agent
	InitialState   map[string]any
	InitialContent *genai.Content
	OnEvent        func(*session.Event)
}

// Run executes an ADK agent and returns the final session state and the last content received.
func Run(ctx context.Context, input RunInput) (session.Session, *genai.Content, error) {
	if input.Agent == nil {
		return nil, nil, fmt.Errorf("agent is required")
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
	r, err := adk.New(adk.Config{
		AppName:        appName,
		Agent:          input.Agent,
		SessionService: sessionService,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create ADK runner: %w", err)
	}

	created, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName:   appName,
		UserID:    userID,
		SessionID: input.SessionID,
		State:     input.InitialState,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("create ADK session: %w", err)
	}

	var lastContent *genai.Content
	events := r.Run(ctx, userID, created.Session.ID(), input.InitialContent, agent.RunConfig{})
	for ev, runErr := range events {
		if runErr != nil {
			return nil, nil, runErr
		}
		if ev != nil && ev.Content != nil {
			lastContent = ev.Content
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
		return nil, nil, fmt.Errorf("get ADK session: %w", err)
	}

	return finalSess.Session, lastContent, nil
}
