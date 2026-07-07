package actorlayer_test

import (
	"context"
	"fmt"
	"time"

	"github.com/normahq/norma/v2/pkg/actorlayer"
)

func ExampleAsk() {
	ctx := context.Background()
	sys, err := actorlayer.NewSystem(actorlayer.Config{})
	if err != nil {
		panic(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sys.Shutdown(shutdownCtx)
	}()

	echoRef, err := sys.Spawn(ctx, "echo", actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(c actorlayer.Context, env actorlayer.Envelope) error {
				if env.ReplyTo == nil {
					return nil
				}
				return c.Tell(c, *env.ReplyTo, fmt.Sprintf("echo:%v", env.Payload))
			}), nil
		},
	})
	if err != nil {
		panic(err)
	}

	reply, err := actorlayer.Ask(ctx, sys, echoRef, "ping", actorlayer.WithTimeout(time.Second))
	if err != nil {
		panic(err)
	}

	fmt.Println(reply.Payload)
	// Output: echo:ping
}

type addRequest struct {
	A int
	B int
}

type addResponse struct {
	Sum int
}

func ExampleSpawnTyped() {
	ctx := context.Background()
	sys, err := actorlayer.NewSystem(actorlayer.Config{})
	if err != nil {
		panic(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = sys.Shutdown(shutdownCtx)
	}()

	ref, err := actorlayer.SpawnTyped[addRequest](ctx, sys, "adder", actorlayer.TypedProps[addRequest]{
		NewActor: func(actorlayer.SpawnContext) (actorlayer.Actor[addRequest], error) {
			return actorlayer.AdaptRequestActor[addRequest, addResponse](
				actorlayer.RequestActorFunc[addRequest, addResponse](func(_ actorlayer.Context, env actorlayer.TypedEnvelope[addRequest]) (addResponse, error) {
					return addResponse{Sum: env.Message.A + env.Message.B}, nil
				}),
			), nil
		},
	})
	if err != nil {
		panic(err)
	}

	resp, err := actorlayer.AskTyped[addRequest, addResponse](
		ctx,
		sys,
		ref,
		addRequest{A: 2, B: 3},
		actorlayer.WithTimeout(time.Second),
	)
	if err != nil {
		panic(err)
	}

	fmt.Println(resp.Message.Sum)
	// Output: 5
}
