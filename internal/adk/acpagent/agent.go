package acpagent

import (
	"context"
	"fmt"
	"io"
	"iter"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

type Config struct {
	Context           context.Context
	Name              string
	Description       string
	Command           []string
	WorkingDir        string
	Stderr            io.Writer
	PermissionHandler PermissionHandler
	Logger            *zerolog.Logger
}

type Agent struct {
	adkagent.Agent

	client      *Client
	workingDir  string
	logger      zerolog.Logger
	sessionMu   sync.Mutex
	remoteByADK map[string]string
}

func New(cfg Config) (*Agent, error) {
	ctx := cfg.Context
	if ctx == nil {
		ctx = context.Background()
	}
	if strings.TrimSpace(cfg.Name) == "" {
		cfg.Name = "GeminiACP"
	}
	if strings.TrimSpace(cfg.Description) == "" {
		cfg.Description = "Gemini CLI exposed through ACP"
	}

	l := log.With().Str("component", "acpagent.agent").Logger()
	if cfg.Logger != nil {
		l = cfg.Logger.With().Str("subcomponent", "acpagent.agent").Logger()
	}

	client, err := NewClient(ctx, ClientConfig{
		Command:           cfg.Command,
		WorkingDir:        cfg.WorkingDir,
		Stderr:            cfg.Stderr,
		PermissionHandler: cfg.PermissionHandler,
		Logger:            &l,
	})
	if err != nil {
		return nil, err
	}
	if _, err := client.Initialize(ctx); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("initialize acp client: %w", err)
	}

	a := &Agent{
		client:      client,
		workingDir:  cfg.WorkingDir,
		logger:      l,
		remoteByADK: make(map[string]string),
	}
	base, err := adkagent.New(adkagent.Config{
		Name:        cfg.Name,
		Description: cfg.Description,
		Run:         a.run,
	})
	if err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("create adk acp agent: %w", err)
	}
	a.Agent = base
	return a, nil
}

func (a *Agent) Close() error {
	return a.client.Close()
}

func (a *Agent) run(ctx adkagent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		remoteSessionID, err := a.ensureRemoteSession(ctx, ctx.Session().ID())
		if err != nil {
			yield(nil, err)
			return
		}

		prompt := extractPromptText(ctx.UserContent())
		if strings.TrimSpace(prompt) == "" {
			yield(nil, fmt.Errorf("playground prompt is empty"))
			return
		}

		a.logger.Info().
			Str("adk_session_id", ctx.Session().ID()).
			Str("acp_session_id", remoteSessionID).
			Int("prompt_len", len(prompt)).
			Msg("starting adk invocation")

		updates, resultCh, err := a.client.Prompt(ctx, remoteSessionID, prompt)
		if err != nil {
			yield(nil, err)
			return
		}

		var finalText strings.Builder
		var promptResult *PromptResult
		for updates != nil || resultCh != nil {
			select {
			case note, ok := <-updates:
				if !ok {
					updates = nil
					continue
				}
				chunk := updateText(note.Update)
				if chunk == "" {
					continue
				}
				finalText.WriteString(chunk)
				ev := session.NewEvent(ctx.InvocationID())
				ev.Content = genai.NewContentFromText(chunk, genai.RoleModel)
				ev.Partial = true
				if !yield(ev, nil) {
					return
				}
			case result, ok := <-resultCh:
				if !ok {
					resultCh = nil
					continue
				}
				promptResult = &result
				resultCh = nil
			}
		}
		if promptResult != nil && promptResult.Err != nil {
			yield(nil, promptResult.Err)
			return
		}
		if finalText.Len() == 0 {
			return
		}

		a.logger.Info().
			Str("adk_session_id", ctx.Session().ID()).
			Str("acp_session_id", remoteSessionID).
			Int("response_len", finalText.Len()).
			Msg("completed adk invocation")

		ev := session.NewEvent(ctx.InvocationID())
		ev.Content = genai.NewContentFromText(finalText.String(), genai.RoleModel)
		ev.TurnComplete = true
		if !yield(ev, nil) {
			return
		}
	}
}

func (a *Agent) ensureRemoteSession(ctx context.Context, adkSessionID string) (string, error) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	if sessionID := a.remoteByADK[adkSessionID]; sessionID != "" {
		a.logger.Info().Str("adk_session_id", adkSessionID).Str("acp_session_id", sessionID).Msg("reusing acp session for adk session")
		return sessionID, nil
	}
	resp, err := a.client.NewSession(ctx, a.workingDir)
	if err != nil {
		return "", err
	}
	sessionID := string(resp.SessionId)
	a.remoteByADK[adkSessionID] = sessionID
	a.logger.Info().Str("adk_session_id", adkSessionID).Str("acp_session_id", sessionID).Msg("created new acp session for adk session")
	return sessionID, nil
}

func extractPromptText(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var builder strings.Builder
	for _, part := range content.Parts {
		if part == nil || part.Text == "" {
			continue
		}
		builder.WriteString(part.Text)
	}
	return strings.TrimSpace(builder.String())
}

func updateText(update acp.SessionUpdate) string {
	if update.AgentMessageChunk == nil {
		return ""
	}
	content := update.AgentMessageChunk.Content
	if content.Text == nil {
		return ""
	}
	return content.Text.Text
}
