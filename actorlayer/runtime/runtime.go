package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/normahq/norma/actorlayer/dispatch"
	actorengine "github.com/normahq/norma/actorlayer/engine"
)

const unknownLaneKey = "unknown"

// AddressResolver extracts a dispatch address from a delivery envelope.
type AddressResolver func(envelope any) (string, error)

// LaneKeyResolver derives the engine lane key for one delivery envelope.
type LaneKeyResolver func(envelope any) string

// Config configures the engine-backed runtime dispatcher.
type Config struct {
	Registry    dispatch.Registry
	AddressOf   AddressResolver
	LaneKey     LaneKeyResolver
	Retry       actorengine.RetryPolicy
	Sink        actorengine.EventSink
	LaneIdleTTL time.Duration
}

// Runtime dispatches engine deliveries to actors resolved from a registry.
type Runtime struct {
	engine    *actorengine.Runtime
	registry  dispatch.Registry
	addressOf AddressResolver
}

// New constructs a Runtime backed by actorlayer/engine lane serialization.
func New(cfg Config) (*Runtime, error) {
	if cfg.Registry == nil {
		return nil, fmt.Errorf("runtime registry is required")
	}
	if cfg.AddressOf == nil {
		return nil, fmt.Errorf("runtime address resolver is required")
	}
	engine, err := actorengine.New(actorengine.Config{
		Resolver:    deliveryResolver{addressOf: cfg.AddressOf, laneKey: cfg.LaneKey},
		Retry:       cfg.Retry,
		Sink:        cfg.Sink,
		LaneIdleTTL: cfg.LaneIdleTTL,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{engine: engine, registry: cfg.Registry, addressOf: cfg.AddressOf}, nil
}

// Handle executes one engine delivery through registry dispatch.
func (r *Runtime) Handle(ctx context.Context, delivery actorengine.Delivery) error {
	if delivery == nil {
		return nil
	}
	if r == nil || r.engine == nil {
		return fmt.Errorf("runtime engine is required")
	}
	return r.engine.Handle(ctx, delivery, r.handleDelivery)
}

// Run consumes deliveries from source and handles them with registry dispatch.
func (r *Runtime) Run(ctx context.Context, source actorengine.Source) error {
	if r == nil || r.engine == nil {
		return fmt.Errorf("runtime engine is required")
	}
	return r.engine.Run(ctx, source, r.handleDelivery)
}

func (r *Runtime) handleDelivery(ctx context.Context, delivery actorengine.Delivery) error {
	address, err := r.addressOf(delivery.Envelope())
	if err != nil {
		return err
	}
	actor, found := r.registry.Resolve(address)
	if !found {
		return fmt.Errorf("actor not found: %s", strings.TrimSpace(address))
	}
	return actor.Handle(ctx, delivery.Envelope())
}

type deliveryResolver struct {
	addressOf AddressResolver
	laneKey   LaneKeyResolver
}

func (r deliveryResolver) LaneKey(delivery actorengine.Delivery) string {
	if delivery == nil {
		return unknownLaneKey
	}
	envelope := delivery.Envelope()
	if r.laneKey != nil {
		if key := strings.TrimSpace(r.laneKey(envelope)); key != "" {
			return key
		}
	}
	if r.addressOf == nil {
		return unknownLaneKey
	}
	address, err := r.addressOf(envelope)
	if err != nil {
		return unknownLaneKey
	}
	if key := strings.TrimSpace(strings.ToLower(address)); key != "" {
		return key
	}
	return unknownLaneKey
}
