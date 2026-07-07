package adk

import (
	"fmt"
	"time"

	"github.com/normahq/norma/v2/pkg/actorlayer"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/artifact"
	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
)

// Config defines how an ADK agent is hosted as an actor behavior.
type Config struct {
	AppName string
	Agent   adkagent.Agent

	SessionService  session.Service
	ArtifactService artifact.Service
	MemoryService   memory.Service

	RunConfig adkagent.RunConfig

	SessionPolicy      SessionPolicy
	UserPolicy         UserPolicy
	Codec              Codec
	ReplyMode          ReplyMode
	TransferPolicy     TransferPolicy
	TransferTargets    map[string]actorlayer.Ref
	EscalationTarget   *actorlayer.Ref
	TransferAskTimeout time.Duration
	AutoCreateSession  bool
}

// Props builds actorlayer props that host an ADK runner-backed behavior.
func Props(cfg Config) actorlayer.Props {
	return actorlayer.Props{
		Kind: "adk",
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			resolved, err := withDefaults(cfg)
			if err != nil {
				return nil, err
			}

			r, err := runner.New(runner.Config{
				AppName:           resolved.AppName,
				Agent:             resolved.Agent,
				SessionService:    resolved.SessionService,
				ArtifactService:   resolved.ArtifactService,
				MemoryService:     resolved.MemoryService,
				AutoCreateSession: resolved.AutoCreateSession,
			})
			if err != nil {
				return nil, fmt.Errorf("create adk runner: %w", err)
			}

			return newAgentBehavior(resolved, r), nil
		},
	}
}

func withDefaults(cfg Config) (Config, error) {
	if cfg.Agent == nil {
		return Config{}, fmt.Errorf("adk: agent is required")
	}
	if cfg.AppName == "" {
		cfg.AppName = "actorlayer"
	}
	if cfg.SessionService == nil {
		cfg.SessionService = session.InMemoryService()
	}
	if cfg.SessionPolicy == nil {
		cfg.SessionPolicy = ConversationSession("conversation_id")
	}
	if cfg.UserPolicy == nil {
		cfg.UserPolicy = HeaderUser("user_id", "system")
	}
	if cfg.Codec == nil {
		cfg.Codec = TextCodec()
	}
	if cfg.TransferAskTimeout <= 0 {
		cfg.TransferAskTimeout = 30 * time.Second
	}
	return cfg, nil
}
