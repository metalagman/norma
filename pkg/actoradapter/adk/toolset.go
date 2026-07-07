package adk

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/normahq/norma/pkg/actorlayer"
	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
)

const (
	defaultAskTimeout    = 30 * time.Second
	defaultMaxPayload    = 256 * 1024
	operationSend        = "send"
	operationAsk         = "ask"
	operationPublish     = "publish"
	toolNameActorSend    = "actor_send"
	toolNameActorAsk     = "actor_ask"
	toolNameActorPublish = "actor_publish"
)

// AddressPolicy decides whether an operation can target an actor ID.
type AddressPolicy interface {
	Allowed(ctx agent.ReadonlyContext, target actorlayer.ActorID, operation string) bool
}

// ToolsetConfig configures actor tool exposure and safety limits.
type ToolsetConfig struct {
	Allowed            AddressPolicy
	DefaultAskTimeout  time.Duration
	MaxAskTimeout      time.Duration
	MaxPayloadBytes    int
	NamedRefs          map[string]actorlayer.Ref
	AllowDirectActorID bool
	EnablePublish      bool
}

// ToolsetOption mutates ToolsetConfig.
type ToolsetOption interface{ applyToolset(cfg *ToolsetConfig) }

type toolsetOptionFunc func(cfg *ToolsetConfig)

func (f toolsetOptionFunc) applyToolset(cfg *ToolsetConfig) { f(cfg) }

// WithAddressPolicy sets the allow/deny policy for actor targets.
func WithAddressPolicy(policy AddressPolicy) ToolsetOption {
	return toolsetOptionFunc(func(cfg *ToolsetConfig) {
		cfg.Allowed = policy
	})
}

// WithNamedRef registers a single symbolic target for tools.
func WithNamedRef(name string, ref actorlayer.Ref) ToolsetOption {
	return toolsetOptionFunc(func(cfg *ToolsetConfig) {
		if cfg.NamedRefs == nil {
			cfg.NamedRefs = make(map[string]actorlayer.Ref)
		}
		cfg.NamedRefs[name] = ref
	})
}

// WithNamedRefs registers multiple symbolic targets for tools.
func WithNamedRefs(refs map[string]actorlayer.Ref) ToolsetOption {
	return toolsetOptionFunc(func(cfg *ToolsetConfig) {
		if len(refs) == 0 {
			return
		}
		if cfg.NamedRefs == nil {
			cfg.NamedRefs = make(map[string]actorlayer.Ref, len(refs))
		}
		for name, ref := range refs {
			cfg.NamedRefs[name] = ref
		}
	})
}

// WithDefaultAskTimeout sets the default timeout for actor_ask.
func WithDefaultAskTimeout(timeout time.Duration) ToolsetOption {
	return toolsetOptionFunc(func(cfg *ToolsetConfig) {
		cfg.DefaultAskTimeout = timeout
	})
}

// WithMaxAskTimeout sets the maximum allowed actor_ask timeout.
func WithMaxAskTimeout(timeout time.Duration) ToolsetOption {
	return toolsetOptionFunc(func(cfg *ToolsetConfig) {
		cfg.MaxAskTimeout = timeout
	})
}

// WithMaxPayloadBytes sets the maximum JSON-encoded payload size.
func WithMaxPayloadBytes(maxBytes int) ToolsetOption {
	return toolsetOptionFunc(func(cfg *ToolsetConfig) {
		cfg.MaxPayloadBytes = maxBytes
	})
}

// WithDirectActorIDTargets enables raw actor ID addressing in tools.
func WithDirectActorIDTargets(enabled bool) ToolsetOption {
	return toolsetOptionFunc(func(cfg *ToolsetConfig) {
		cfg.AllowDirectActorID = enabled
	})
}

// WithPublishEnabled enables actor_publish tool registration.
func WithPublishEnabled(enabled bool) ToolsetOption {
	return toolsetOptionFunc(func(cfg *ToolsetConfig) {
		cfg.EnablePublish = enabled
	})
}

// Toolset builds ADK function tools for actor_send, actor_ask, and optionally
// actor_publish.
func Toolset(sys *actorlayer.System, opts ...ToolsetOption) (tool.Toolset, error) {
	cfg := ToolsetConfig{
		DefaultAskTimeout: defaultAskTimeout,
		MaxAskTimeout:     defaultAskTimeout,
		MaxPayloadBytes:   defaultMaxPayload,
		NamedRefs:         make(map[string]actorlayer.Ref),
	}
	for _, opt := range opts {
		opt.applyToolset(&cfg)
	}
	if cfg.DefaultAskTimeout <= 0 {
		cfg.DefaultAskTimeout = defaultAskTimeout
	}
	if cfg.MaxAskTimeout <= 0 {
		cfg.MaxAskTimeout = cfg.DefaultAskTimeout
	}
	if cfg.MaxPayloadBytes <= 0 {
		cfg.MaxPayloadBytes = defaultMaxPayload
	}

	namedIDs := make(map[actorlayer.ActorID]struct{}, len(cfg.NamedRefs))
	for _, ref := range cfg.NamedRefs {
		namedIDs[ref.ID()] = struct{}{}
	}
	if cfg.Allowed == nil {
		cfg.Allowed = namedOnlyAddressPolicy{allowed: namedIDs}
	}

	svc := &toolsetService{sys: sys, cfg: cfg}

	sendTool, sendErr := functiontool.New(functiontool.Config{
		Name:        toolNameActorSend,
		Description: "Send an asynchronous message to an allowed actor",
	}, svc.actorSend)
	if sendErr != nil {
		return nil, fmt.Errorf("actor tools: create %s tool: %w", toolNameActorSend, sendErr)
	}

	askTool, askErr := functiontool.New(functiontool.Config{
		Name:        toolNameActorAsk,
		Description: "Send a request to an allowed actor and wait for a reply",
	}, svc.actorAsk)
	if askErr != nil {
		return nil, fmt.Errorf("actor tools: create %s tool: %w", toolNameActorAsk, askErr)
	}

	tools := make([]tool.Tool, 0, 3)
	tools = append(tools, sendTool, askTool)
	if cfg.EnablePublish {
		publishTool, publishErr := functiontool.New(functiontool.Config{
			Name:        toolNameActorPublish,
			Description: "Publish an event payload to actorlayer topic subscribers",
		}, svc.actorPublish)
		if publishErr != nil {
			return nil, fmt.Errorf("actor tools: create %s tool: %w", toolNameActorPublish, publishErr)
		}
		tools = append(tools, publishTool)
	}

	return &staticToolset{name: "actor_tools", tools: tools}, nil
}

// Message wraps tool payload with an optional topic hint.
type Message struct {
	Topic   string `json:"topic,omitempty"`
	Payload any    `json:"payload,omitempty"`
}

type actorSendInput struct {
	To      string `json:"to"`
	Topic   string `json:"topic,omitempty"`
	Payload any    `json:"payload"`
}

type actorSendOutput struct {
	OK bool `json:"ok"`
}

type actorAskInput struct {
	To        string `json:"to"`
	Topic     string `json:"topic,omitempty"`
	Payload   any    `json:"payload"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type actorAskOutput struct {
	OK      bool   `json:"ok"`
	Reply   any    `json:"reply,omitempty"`
	ReplyID string `json:"reply_id,omitempty"`
}

type actorPublishInput struct {
	Topic   string `json:"topic"`
	Payload any    `json:"payload"`
}

type actorPublishOutput struct {
	OK bool `json:"ok"`
}

type toolsetService struct {
	sys *actorlayer.System
	cfg ToolsetConfig
}

func (s *toolsetService) actorSend(ctx agent.Context, in actorSendInput) (actorSendOutput, error) {
	err := s.send(ctx, in)
	if err != nil {
		return actorSendOutput{}, err
	}
	return actorSendOutput{OK: true}, nil
}

func (s *toolsetService) actorAsk(ctx agent.Context, in actorAskInput) (actorAskOutput, error) {
	reply, err := s.ask(ctx, in)
	if err != nil {
		return actorAskOutput{}, err
	}
	return actorAskOutput{OK: true, Reply: reply.Payload, ReplyID: string(reply.ID)}, nil
}

func (s *toolsetService) actorPublish(ctx agent.Context, in actorPublishInput) (actorPublishOutput, error) {
	err := s.publish(ctx, in)
	if err != nil {
		return actorPublishOutput{}, err
	}
	return actorPublishOutput{OK: true}, nil
}

func (s *toolsetService) send(ctx agent.ReadonlyContext, in actorSendInput) error {
	if s.sys == nil {
		return fmt.Errorf("actor tools: system is nil")
	}
	if err := s.validatePayload(in.Payload); err != nil {
		return err
	}

	target, err := s.resolveTarget(in.To)
	if err != nil {
		return err
	}
	if !s.cfg.Allowed.Allowed(ctx, target.ID(), operationSend) {
		return fmt.Errorf("actor tools: target %q is not allowed for %s", target.ID(), operationSend)
	}

	payload := packPayload(in.Topic, in.Payload)
	return s.sys.Tell(ctx, target, payload)
}

func (s *toolsetService) ask(ctx agent.ReadonlyContext, in actorAskInput) (actorlayer.Envelope, error) {
	if s.sys == nil {
		return actorlayer.Envelope{}, fmt.Errorf("actor tools: system is nil")
	}
	if err := s.validatePayload(in.Payload); err != nil {
		return actorlayer.Envelope{}, err
	}

	target, err := s.resolveTarget(in.To)
	if err != nil {
		return actorlayer.Envelope{}, err
	}
	if !s.cfg.Allowed.Allowed(ctx, target.ID(), operationAsk) {
		return actorlayer.Envelope{}, fmt.Errorf("actor tools: target %q is not allowed for %s", target.ID(), operationAsk)
	}

	timeout := s.cfg.DefaultAskTimeout
	if in.TimeoutMS > 0 {
		timeout = time.Duration(in.TimeoutMS) * time.Millisecond
	}
	if timeout > s.cfg.MaxAskTimeout {
		return actorlayer.Envelope{}, fmt.Errorf("actor tools: timeout %s exceeds maximum %s", timeout, s.cfg.MaxAskTimeout)
	}
	payload := packPayload(in.Topic, in.Payload)
	return actorlayer.Ask(ctx, s.sys, target, payload, actorlayer.WithTimeout(timeout))
}

func (s *toolsetService) publish(ctx agent.ReadonlyContext, in actorPublishInput) error {
	if s.sys == nil {
		return fmt.Errorf("actor tools: system is nil")
	}
	if !s.cfg.EnablePublish {
		return fmt.Errorf("actor tools: publish is disabled")
	}
	if in.Topic == "" {
		return fmt.Errorf("actor tools: topic is required")
	}
	if err := s.validatePayload(in.Payload); err != nil {
		return err
	}
	if s.cfg.Allowed != nil && !s.cfg.Allowed.Allowed(ctx, actorlayer.ActorID(in.Topic), operationPublish) {
		return fmt.Errorf("actor tools: topic %q is not allowed for %s", in.Topic, operationPublish)
	}
	return s.sys.Publish(ctx, in.Topic, in.Payload)
}

func (s *toolsetService) validatePayload(payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("actor tools: encode payload: %w", err)
	}
	if len(raw) > s.cfg.MaxPayloadBytes {
		return fmt.Errorf("actor tools: payload size %d exceeds maximum %d", len(raw), s.cfg.MaxPayloadBytes)
	}
	return nil
}

func (s *toolsetService) resolveTarget(to string) (actorlayer.Ref, error) {
	if to == "" {
		return actorlayer.Ref{}, fmt.Errorf("actor tools: target is required")
	}
	if ref, ok := s.cfg.NamedRefs[to]; ok {
		return ref, nil
	}
	if s.cfg.AllowDirectActorID {
		return s.sys.Ref(actorlayer.ActorID(to)), nil
	}
	return actorlayer.Ref{}, fmt.Errorf("actor tools: unknown target %q", to)
}

func packPayload(topic string, payload any) any {
	if topic == "" {
		return payload
	}
	return Message{Topic: topic, Payload: payload}
}

type namedOnlyAddressPolicy struct {
	allowed map[actorlayer.ActorID]struct{}
}

func (p namedOnlyAddressPolicy) Allowed(_ agent.ReadonlyContext, target actorlayer.ActorID, _ string) bool {
	_, ok := p.allowed[target]
	return ok
}

type staticToolset struct {
	name  string
	tools []tool.Tool
}

func (s *staticToolset) Name() string {
	return s.name
}

func (s *staticToolset) Tools(_ agent.ReadonlyContext) ([]tool.Tool, error) {
	out := make([]tool.Tool, len(s.tools))
	copy(out, s.tools)
	return out, nil
}
