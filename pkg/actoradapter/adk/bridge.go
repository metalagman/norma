package adk

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"github.com/normahq/norma/pkg/actorlayer"
	adkagent "google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
	"google.golang.org/genai"
)

// ReplyMode defines how an ADK actor replies to incoming envelopes.
type ReplyMode int

const (
	// NoReply does not send any reply envelope.
	NoReply ReplyMode = iota
	// ReplyFinal sends one reply from the final ADK response.
	ReplyFinal
	// ReplyEachEvent streams one reply per ADK event.
	ReplyEachEvent
	// ReplyFinalAndPublishEvents publishes events and sends the final reply.
	ReplyFinalAndPublishEvents
)

// TransferPolicy controls how ADK transfer/escalation actions are handled.
type TransferPolicy int

const (
	// TransferADKInternal leaves transfer/escalation handling inside ADK.
	TransferADKInternal TransferPolicy = iota
	// TransferToActorAsk maps transfer/escalation to actor Ask calls.
	TransferToActorAsk
	// TransferToActorTell maps transfer/escalation to actor Tell calls.
	TransferToActorTell
	// TransferReject rejects transfer/escalation actions.
	TransferReject
)

type runRunner interface {
	Run(ctx context.Context, userID, sessionID string, content *genai.Content, cfg adkagent.RunConfig, opts ...runner.RunOption) iter.Seq2[*session.Event, error]
}

// AgentBehavior hosts an ADK runner invocation behind actor behavior semantics.
type AgentBehavior struct {
	runner runRunner
	cfg    Config
}

// ActionMessage represents an ADK transfer or escalation action.
type ActionMessage struct {
	Type   string
	Agent  string
	Source actorlayer.ActorID
	Event  *session.Event
}

func newAgentBehavior(cfg Config, r runRunner) *AgentBehavior {
	return &AgentBehavior{cfg: cfg, runner: r}
}

// Receive runs the configured ADK runner and maps events to actor replies/actions.
func (b *AgentBehavior) Receive(ctx actorlayer.Context, env actorlayer.Envelope) error {
	if b.runner == nil {
		return b.fail(ctx, env, errors.New("adk: runner is nil"))
	}

	content, err := b.cfg.Codec.ToContent(env)
	if err != nil {
		return b.fail(ctx, env, err)
	}

	userID := b.cfg.UserPolicy.UserID(env)
	sessionID := b.cfg.SessionPolicy.SessionID(env, ctx.Self())

	var events []*session.Event
	for event, runErr := range b.runner.Run(ctx, userID, sessionID, content, b.cfg.RunConfig) {
		if runErr != nil {
			return b.fail(ctx, env, runErr)
		}
		if event != nil {
			events = append(events, event)
		}

		if b.cfg.ReplyMode == ReplyFinalAndPublishEvents {
			if pubErr := ctx.Publish(ctx, "adk.event", event); pubErr != nil {
				return b.fail(ctx, env, pubErr)
			}
		}

		if policyErr := b.handleActions(ctx, event); policyErr != nil {
			return b.fail(ctx, env, policyErr)
		}

		if env.ReplyTo == nil || b.cfg.ReplyMode != ReplyEachEvent {
			continue
		}
		payload, mapErr := b.cfg.Codec.FromEvent(event)
		if mapErr != nil {
			return b.fail(ctx, env, mapErr)
		}
		if tellErr := ctx.Tell(ctx, *env.ReplyTo, payload); tellErr != nil {
			return b.fail(ctx, env, tellErr)
		}
	}

	if env.ReplyTo == nil {
		return nil
	}

	mode := b.cfg.ReplyMode
	if mode == NoReply {
		mode = ReplyFinal
	}

	switch mode {
	case ReplyFinal, ReplyFinalAndPublishEvents:
		payload, ok, mapErr := b.cfg.Codec.FinalResponse(events)
		if mapErr != nil {
			return b.fail(ctx, env, mapErr)
		}
		if ok {
			if tellErr := ctx.Tell(ctx, *env.ReplyTo, payload); tellErr != nil {
				return b.fail(ctx, env, tellErr)
			}
			return nil
		}
	case NoReply, ReplyEachEvent:
	}

	return nil
}

func (b *AgentBehavior) fail(ctx actorlayer.Context, env actorlayer.Envelope, err error) error {
	if err == nil {
		return nil
	}
	if env.ReplyTo == nil {
		return err
	}
	if tellErr := ctx.Tell(ctx, *env.ReplyTo, err); tellErr != nil {
		return tellErr
	}
	return nil
}

func (b *AgentBehavior) handleActions(ctx actorlayer.Context, event *session.Event) error {
	if event == nil {
		return nil
	}
	actions := event.Actions
	if actions.TransferToAgent == "" && !actions.Escalate {
		return nil
	}

	switch b.cfg.TransferPolicy {
	case TransferADKInternal:
		return nil
	case TransferReject:
		return fmt.Errorf("adk: transfer/escalation rejected by policy")
	case TransferToActorTell, TransferToActorAsk:
		if actions.TransferToAgent != "" {
			target, ok := b.cfg.TransferTargets[actions.TransferToAgent]
			if !ok {
				return fmt.Errorf("adk: missing transfer target for agent %q", actions.TransferToAgent)
			}
			msg := ActionMessage{
				Type:   "transfer",
				Agent:  actions.TransferToAgent,
				Source: ctx.Self().ID(),
				Event:  event,
			}
			if err := b.dispatchAction(ctx, target, msg); err != nil {
				return err
			}
		}
		if actions.Escalate {
			if b.cfg.EscalationTarget == nil {
				return fmt.Errorf("adk: escalation target is not configured")
			}
			msg := ActionMessage{
				Type:   "escalate",
				Source: ctx.Self().ID(),
				Event:  event,
			}
			if err := b.dispatchAction(ctx, *b.cfg.EscalationTarget, msg); err != nil {
				return err
			}
		}
		return nil
	default:
		return nil
	}
}

func (b *AgentBehavior) dispatchAction(ctx actorlayer.Context, target actorlayer.Ref, msg ActionMessage) error {
	if b.cfg.TransferPolicy == TransferToActorAsk {
		_, err := ctx.Ask(ctx, target, msg, actorlayer.WithTimeout(b.cfg.TransferAskTimeout))
		return err
	}
	return ctx.Tell(ctx, target, msg)
}
