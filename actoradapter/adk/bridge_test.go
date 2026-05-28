package adk

import (
	"context"
	"errors"
	"iter"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/normahq/norma/actorlayer"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const testTransferAgent = "worker"

func TestActorMessageInvokesRunnerOnce(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{called: make(chan struct{}, 1)}
	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	ref := spawnADKActor(t, sys, testConfig(), r)
	if err := sys.Tell(context.Background(), ref, "ping"); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}

	select {
	case <-r.called:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner was not called")
	}

	if got := r.CallCount(); got != 1 {
		t.Fatalf("runner calls = %d, want 1", got)
	}
}

func TestFinalEventBecomesAskReply(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{}
	r.run = func(_ int, _ string, _ string, _ *genai.Content, _ adkagent.RunConfig) iter.Seq2[*session.Event, error] {
		final := session.NewEvent("inv")
		final.Content = genai.NewContentFromText("done", genai.RoleModel)
		final.TurnComplete = true
		return seqEvents([]*session.Event{final}, nil)
	}

	sys := newSystem(t)
	defer shutdownSystem(t, sys)
	ref := spawnADKActor(t, sys, testConfig(), r)

	reply, err := actorlayer.Ask(context.Background(), sys, ref, "work", actorlayer.WithTimeout(500*time.Millisecond))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	got, ok := reply.Payload.(string)
	if !ok {
		t.Fatalf("reply payload type = %T, want string", reply.Payload)
	}
	if got != "done" {
		t.Fatalf("reply payload = %q, want %q", got, "done")
	}
}

func TestADKErrorsBecomeAskErrors(t *testing.T) {
	t.Parallel()

	boom := errors.New("boom")
	r := &fakeRunner{}
	r.run = func(_ int, _ string, _ string, _ *genai.Content, _ adkagent.RunConfig) iter.Seq2[*session.Event, error] {
		return seqEvents(nil, boom)
	}

	sys := newSystem(t)
	defer shutdownSystem(t, sys)
	ref := spawnADKActor(t, sys, testConfig(), r)

	_, err := actorlayer.Ask(context.Background(), sys, ref, "work", actorlayer.WithTimeout(500*time.Millisecond))
	if !errors.Is(err, boom) {
		t.Fatalf("Ask() error = %v, want %v", err, boom)
	}
}

func TestSessionPolicyIsUsed(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{called: make(chan struct{}, 1)}
	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	cfg := testConfig()
	cfg.SessionPolicy = ConversationSession("conversation_id")

	ref := spawnADKActor(t, sys, cfg, r)
	if err := sys.Tell(context.Background(), ref, "work", actorlayer.WithHeader("conversation_id", "conv-42")); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}

	select {
	case <-r.called:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("runner was not called")
	}

	calls := r.Calls()
	if len(calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(calls))
	}
	if calls[0].sessionID != "conv-42" {
		t.Fatalf("sessionID = %q, want %q", calls[0].sessionID, "conv-42")
	}
}

func TestMessagesToSameADKActorDoNotOverlap(t *testing.T) {
	t.Parallel()

	var inFlight atomic.Int32
	var overlap atomic.Bool
	started := make(chan struct{}, 2)
	done := make(chan struct{}, 2)

	r := &fakeRunner{}
	r.run = func(_ int, _ string, _ string, _ *genai.Content, _ adkagent.RunConfig) iter.Seq2[*session.Event, error] {
		if inFlight.Add(1) != 1 {
			overlap.Store(true)
		}
		started <- struct{}{}
		time.Sleep(60 * time.Millisecond)
		inFlight.Add(-1)
		done <- struct{}{}
		return seqEvents(nil, nil)
	}

	sys := newSystem(t)
	defer shutdownSystem(t, sys)
	ref := spawnADKActor(t, sys, testConfig(), r)

	if err := sys.Tell(context.Background(), ref, "one"); err != nil {
		t.Fatalf("Tell(one) error = %v", err)
	}
	if err := sys.Tell(context.Background(), ref, "two"); err != nil {
		t.Fatalf("Tell(two) error = %v", err)
	}

	select {
	case <-started:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("first run did not start")
	}

	for i := 0; i < 2; i++ {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("runner executions did not finish")
		}
	}

	if overlap.Load() {
		t.Fatal("runner executions overlapped for one actor")
	}
}

func TestTransferPolicyRejectReturnsAskError(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{}
	r.run = func(_ int, _ string, _ string, _ *genai.Content, _ adkagent.RunConfig) iter.Seq2[*session.Event, error] {
		ev := session.NewEvent("inv")
		ev.Actions.TransferToAgent = testTransferAgent
		return seqEvents([]*session.Event{ev}, nil)
	}

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	cfg := testConfig()
	cfg.TransferPolicy = TransferReject

	ref := spawnADKActor(t, sys, cfg, r)
	_, err := actorlayer.Ask(context.Background(), sys, ref, "work", actorlayer.WithTimeout(500*time.Millisecond))
	if err == nil {
		t.Fatal("expected transfer reject error, got nil")
	}
}

func TestTransferPolicyTellDispatchesActionMessage(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{}
	r.run = func(_ int, _ string, _ string, _ *genai.Content, _ adkagent.RunConfig) iter.Seq2[*session.Event, error] {
		ev := session.NewEvent("inv")
		ev.Actions.TransferToAgent = testTransferAgent
		return seqEvents([]*session.Event{ev}, nil)
	}

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	transfers := make(chan ActionMessage, 1)
	target, err := sys.Spawn(context.Background(), testTransferAgent, actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(_ actorlayer.Context, env actorlayer.Envelope) error {
				msg, ok := env.Payload.(ActionMessage)
				if ok {
					transfers <- msg
				}
				return nil
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(%s) error = %v", testTransferAgent, err)
	}

	cfg := testConfig()
	cfg.TransferPolicy = TransferToActorTell
	cfg.TransferTargets = map[string]actorlayer.Ref{testTransferAgent: target}

	ref := spawnADKActor(t, sys, cfg, r)
	if err := sys.Tell(context.Background(), ref, "work"); err != nil {
		t.Fatalf("Tell() error = %v", err)
	}

	select {
	case msg := <-transfers:
		if msg.Type != "transfer" {
			t.Fatalf("message type = %q, want transfer", msg.Type)
		}
		if msg.Agent != testTransferAgent {
			t.Fatalf("message agent = %q, want %s", msg.Agent, testTransferAgent)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive transfer action message")
	}
}

func TestTransferPolicyAskDispatchesActionMessage(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{}
	r.run = func(_ int, _ string, _ string, _ *genai.Content, _ adkagent.RunConfig) iter.Seq2[*session.Event, error] {
		ev := session.NewEvent("inv")
		ev.Actions.TransferToAgent = testTransferAgent
		final := session.NewEvent("inv")
		final.Content = genai.NewContentFromText("done", genai.RoleModel)
		final.TurnComplete = true
		return seqEvents([]*session.Event{ev, final}, nil)
	}

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	transfers := make(chan ActionMessage, 1)
	target, err := sys.Spawn(context.Background(), testTransferAgent, actorlayer.Props{
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return actorlayer.ReceiveFunc(func(ctx actorlayer.Context, env actorlayer.Envelope) error {
				msg, ok := env.Payload.(ActionMessage)
				if ok {
					transfers <- msg
				}
				if env.ReplyTo == nil {
					return errors.New("expected reply actor for transfer ask")
				}
				return ctx.Tell(ctx, *env.ReplyTo, "ack")
			}), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn(%s) error = %v", testTransferAgent, err)
	}

	cfg := testConfig()
	cfg.TransferPolicy = TransferToActorAsk
	cfg.TransferTargets = map[string]actorlayer.Ref{testTransferAgent: target}
	cfg.TransferAskTimeout = 300 * time.Millisecond

	ref := spawnADKActor(t, sys, cfg, r)
	reply, err := actorlayer.Ask(context.Background(), sys, ref, "work", actorlayer.WithTimeout(800*time.Millisecond))
	if err != nil {
		t.Fatalf("Ask() error = %v", err)
	}

	if got, ok := reply.Payload.(string); !ok || got != "done" {
		t.Fatalf("reply payload = %#v, want %q", reply.Payload, "done")
	}

	select {
	case msg := <-transfers:
		if msg.Type != "transfer" {
			t.Fatalf("message type = %q, want transfer", msg.Type)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("did not receive transfer action message")
	}
}

func TestTransferPolicyAskMissingTargetReturnsAskError(t *testing.T) {
	t.Parallel()

	r := &fakeRunner{}
	r.run = func(_ int, _ string, _ string, _ *genai.Content, _ adkagent.RunConfig) iter.Seq2[*session.Event, error] {
		ev := session.NewEvent("inv")
		ev.Actions.TransferToAgent = testTransferAgent
		return seqEvents([]*session.Event{ev}, nil)
	}

	sys := newSystem(t)
	defer shutdownSystem(t, sys)

	cfg := testConfig()
	cfg.TransferPolicy = TransferToActorAsk
	cfg.TransferAskTimeout = 100 * time.Millisecond

	ref := spawnADKActor(t, sys, cfg, r)
	_, err := actorlayer.Ask(context.Background(), sys, ref, "work", actorlayer.WithTimeout(500*time.Millisecond))
	if err == nil {
		t.Fatal("expected transfer target error, got nil")
	}
}

func testConfig() Config {
	return Config{
		AppName:       "adk-test",
		SessionPolicy: ConversationSession("conversation_id"),
		UserPolicy:    HeaderUser("user_id", "system"),
		Codec:         TextCodec(),
		ReplyMode:     ReplyFinal,
	}
}

func spawnADKActor(t *testing.T, sys *actorlayer.System, cfg Config, r runRunner) actorlayer.Ref {
	t.Helper()

	ref, err := sys.Spawn(context.Background(), "adk", actorlayer.Props{
		Kind: "adk-test",
		NewBehavior: func(actorlayer.SpawnContext) (actorlayer.Behavior, error) {
			return newAgentBehavior(cfg, r), nil
		},
	})
	if err != nil {
		t.Fatalf("Spawn() error = %v", err)
	}
	return ref
}

func newSystem(t *testing.T) *actorlayer.System {
	t.Helper()
	sys, err := actorlayer.NewSystem(actorlayer.Config{})
	if err != nil {
		t.Fatalf("NewSystem() error = %v", err)
	}
	return sys
}

func shutdownSystem(t *testing.T, sys *actorlayer.System) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := sys.Shutdown(ctx); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Shutdown() error = %v", err)
	}
}

type runCall struct {
	userID    string
	sessionID string
	content   *genai.Content
}

type fakeRunner struct {
	mu     sync.Mutex
	calls  []runCall
	run    func(callIndex int, userID, sessionID string, content *genai.Content, cfg adkagent.RunConfig) iter.Seq2[*session.Event, error]
	called chan struct{}
}

func (f *fakeRunner) Run(
	_ context.Context,
	userID string,
	sessionID string,
	content *genai.Content,
	cfg adkagent.RunConfig,
	_ ...runner.RunOption,
) iter.Seq2[*session.Event, error] {
	f.mu.Lock()
	callIndex := len(f.calls)
	f.calls = append(f.calls, runCall{userID: userID, sessionID: sessionID, content: content})
	runFn := f.run
	called := f.called
	f.mu.Unlock()

	if called != nil {
		select {
		case called <- struct{}{}:
		default:
		}
	}

	if runFn == nil {
		return seqEvents(nil, nil)
	}
	return runFn(callIndex, userID, sessionID, content, cfg)
}

func (f *fakeRunner) CallCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeRunner) Calls() []runCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]runCall, len(f.calls))
	copy(out, f.calls)
	return out
}

func seqEvents(events []*session.Event, runErr error) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		for _, ev := range events {
			if !yield(ev, nil) {
				return
			}
		}
		if runErr != nil {
			yield(nil, runErr)
		}
	}
}
