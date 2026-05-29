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

type AddressResolver func(envelope any) (string, error)

type LaneKeyResolver func(envelope any) string

type Config struct {
	Registry    dispatch.Registry
	AddressOf   AddressResolver
	LaneKey     LaneKeyResolver
	Retry       actorengine.RetryPolicy
	Sink        actorengine.EventSink
	LaneIdleTTL time.Duration
}

type Runtime struct {
	engine    *actorengine.Runtime
	registry  dispatch.Registry
	addressOf AddressResolver
}

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

func (r *Runtime) Handle(ctx context.Context, delivery actorengine.Delivery) error {
	if delivery == nil {
		return nil
	}
	if r == nil || r.engine == nil {
		return fmt.Errorf("runtime engine is required")
	}
	return r.engine.Handle(ctx, delivery, r.handleDelivery)
}

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
