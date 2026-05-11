package taskmaster

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

func TestLocatorStringUsesCanonicalFormat(t *testing.T) {
	t.Parallel()

	locator := NewTelegramHumanLocator(123456, 77)
	if got := locator.String(); got != "human:telegram:123456:77" {
		t.Fatalf("locator.String() = %q, want human:telegram:123456:77", got)
	}

	fakeChat := NewFakeChatHumanLocator("local")
	if got := fakeChat.String(); got != "human:fakechat:local" {
		t.Fatalf("locator.String() = %q, want human:fakechat:local", got)
	}
}

func TestRootOnlyConfigAllowed(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]Node{
		rootNodeID: &fakeNode{},
	})
	defer cleanup()

	if err := runtime.Enqueue(jobMessage(NewCLIInputLocator(), NewAgentLocator(rootNodeID), "hello")); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
}

func TestNodeReceivesAddressedMessage(t *testing.T) {
	t.Parallel()

	worker := &fakeNode{started: make(chan Message, 1)}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]Node{
		rootNodeID: &fakeNode{},
		"worker":   worker,
	})
	defer cleanup()

	if err := runtime.Enqueue(jobMessage(NewAgentLocator(rootNodeID), NewAgentLocator("worker"), "do work")); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	select {
	case got := <-worker.started:
		if got.Kind != MessageKindJob {
			t.Fatalf("Kind = %q, want job", got.Kind)
		}
		if got.Content != "do work" {
			t.Fatalf("Content = %q, want do work", got.Content)
		}
		if !reflect.DeepEqual(got.From, NewAgentLocator(rootNodeID)) {
			t.Fatalf("From = %+v, want root", got.From)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not receive message")
	}
}

func TestNodeCanEmitMultipleProgressMessagesBeforeCompletion(t *testing.T) {
	t.Parallel()

	target := &fakeTarget{
		supportedLocators: map[string]bool{locatorKey(NewCLILogLocator()): true},
		dispatchStarted:   make(chan Message, 2),
	}
	worker := &fakeNode{
		emits: []Message{
			{Kind: MessageKindProgress, To: NewCLILogLocator(), Content: "started"},
			{Kind: MessageKindProgress, To: NewCLILogLocator(), Content: "halfway"},
		},
		outcome: Outcome{Status: OutcomeStatusCompleted, Content: "done"},
	}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig([]Target{target}), map[string]Node{
		rootNodeID: &fakeNode{},
		"worker":   worker,
	})
	defer cleanup()

	if err := runtime.Enqueue(jobMessage(NewAgentLocator(rootNodeID), NewAgentLocator("worker"), "do work")); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	first := receiveMessage(t, target.dispatchStarted, "first progress")
	second := receiveMessage(t, target.dispatchStarted, "second progress")
	if first.Kind != MessageKindProgress || second.Kind != MessageKindProgress {
		t.Fatalf("kinds = %q, %q; want progress, progress", first.Kind, second.Kind)
	}
	if first.Content != "started" || second.Content != "halfway" {
		t.Fatalf("contents = %q, %q; want started, halfway", first.Content, second.Content)
	}
	if first.ParentID == "" || second.ParentID != first.ParentID {
		t.Fatalf("parent ids = %q, %q; want same parent", first.ParentID, second.ParentID)
	}
	if first.Metadata["sequence"] != uint64(1) || second.Metadata["sequence"] != uint64(2) {
		t.Fatalf("sequences = %#v, %#v; want 1, 2", first.Metadata["sequence"], second.Metadata["sequence"])
	}
}

func TestOutcomeRouterCreatesSingleResultMessage(t *testing.T) {
	t.Parallel()

	root := &fakeNode{started: make(chan Message, 1)}
	worker := &fakeNode{outcome: Outcome{Status: OutcomeStatusCompleted, Content: "worker result"}}
	cfg := testConfig(nil)
	cfg.OutcomeRouter = func(msg Message, outcome Outcome) []Message {
		if msg.To.Key != "worker" {
			return nil
		}
		return []Message{{
			Kind:    MessageKindResult,
			To:      NewAgentLocator(rootNodeID),
			Content: outcome.Content,
		}}
	}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), cfg, map[string]Node{
		rootNodeID: root,
		"worker":   worker,
	})
	defer cleanup()

	if err := runtime.Enqueue(jobMessage(NewAgentLocator(rootNodeID), NewAgentLocator("worker"), "do work")); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	got := receiveMessage(t, root.started, "root result")
	if got.Kind != MessageKindResult {
		t.Fatalf("Kind = %q, want result", got.Kind)
	}
	if got.Content != "worker result" {
		t.Fatalf("Content = %q, want worker result", got.Content)
	}
	if got.ParentID == "" {
		t.Fatal("ParentID is empty")
	}
	if !reflect.DeepEqual(got.From, NewAgentLocator("worker")) {
		t.Fatalf("From = %+v, want worker", got.From)
	}
}

func TestProgressDispatchFailureDoesNotFailParent(t *testing.T) {
	t.Parallel()

	target := &fakeTarget{
		supportedLocators: map[string]bool{locatorKey(NewCLILogLocator()): true},
		dispatchStarted:   make(chan Message, 1),
		err:               errors.New("log sink unavailable"),
	}
	worker := &fakeNode{
		emits: []Message{{Kind: MessageKindProgress, To: NewCLILogLocator(), Content: "started"}},
		done:  make(chan Outcome, 1),
	}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig([]Target{target}), map[string]Node{
		rootNodeID: &fakeNode{},
		"worker":   worker,
	})
	defer cleanup()

	if err := runtime.Enqueue(jobMessage(NewAgentLocator(rootNodeID), NewAgentLocator("worker"), "do work")); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	receiveMessage(t, target.dispatchStarted, "progress dispatch")
	select {
	case <-worker.done:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not finish")
	}
	if err := runtime.Err(); err != nil {
		t.Fatalf("Runtime.Err() = %v, want nil", err)
	}
}

func TestDispatchFailedJobReportsErrorToSender(t *testing.T) {
	t.Parallel()

	external := NewTelegramHumanLocator(42, 7)
	target := &fakeTarget{
		supportedLocators: map[string]bool{locatorKey(external): true},
		dispatchStarted:   make(chan Message, 1),
		err:               errors.New("telegram unavailable"),
	}
	root := &fakeNode{started: make(chan Message, 1)}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig([]Target{target}), map[string]Node{
		rootNodeID: root,
	})
	defer cleanup()

	msg := jobMessage(NewAgentLocator(rootNodeID), external, "send outbound")
	if err := runtime.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}
	receiveMessage(t, target.dispatchStarted, "external dispatch")
	got := receiveMessage(t, root.started, "dispatch error")
	if got.Kind != MessageKindError {
		t.Fatalf("Kind = %q, want error", got.Kind)
	}
	if !strings.Contains(got.Content, "telegram unavailable") {
		t.Fatalf("Content = %q, want dispatch error", got.Content)
	}
}

func TestEnqueueRejectsSourceOnlyTarget(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]Node{
		rootNodeID: &fakeNode{},
	})
	defer cleanup()

	err := runtime.Enqueue(jobMessage(NewAgentLocator(rootNodeID), NewTimerSourceLocator(), "do work"))
	if err == nil || !strings.Contains(err.Error(), "cannot be used as a target") {
		t.Fatalf("Enqueue() error = %v, want source-only target rejection", err)
	}
}

func TestCLILogTargetLogsCanonicalLocatorStrings(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := zerolog.New(&buf)
	target := NewCLILogTarget(logger)
	msg := Message{
		ID:        "msg-1",
		SessionID: "session-a",
		Kind:      MessageKindNotification,
		From:      NewAgentLocator(rootNodeID),
		To:        NewCLILogLocator(),
		Content:   "done",
	}

	if err := target.DispatchMessage(context.Background(), msg); err != nil {
		t.Fatalf("DispatchMessage() error = %v", err)
	}

	var payload map[string]any
	if err := json.Unmarshal(buf.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal log payload: %v", err)
	}
	if got := payload["locator"]; got != "integration:cli:log" {
		t.Fatalf("locator = %#v, want integration:cli:log", got)
	}
	if got := payload["kind"]; got != MessageKindNotification {
		t.Fatalf("kind = %#v, want notification", got)
	}
}

func TestRuntimeDoneClosesOnStop(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]Node{
		rootNodeID: &fakeNode{},
	})
	defer cleanup()

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case <-runtime.Done():
	case <-time.After(time.Second):
		t.Fatal("runtime done did not close")
	}
}

func TestEnqueueRejectedDuringShutdown(t *testing.T) {
	t.Parallel()

	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]Node{
		rootNodeID: &fakeNode{},
		"worker":   &fakeNode{},
	})
	defer cleanup()

	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	err := runtime.Enqueue(jobMessage(NewAgentLocator(rootNodeID), NewAgentLocator("worker"), "do work"))
	if err == nil || !strings.Contains(err.Error(), "runtime is stopping") {
		t.Fatalf("Enqueue() error = %v, want shutdown rejection", err)
	}
}

func TestEnqueuePreservesContentWhitespace(t *testing.T) {
	t.Parallel()

	worker := &fakeNode{started: make(chan Message, 1)}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]Node{
		rootNodeID: &fakeNode{},
		"worker":   worker,
	})
	defer cleanup()

	if err := runtime.Enqueue(jobMessage(NewAgentLocator(rootNodeID), NewAgentLocator("worker"), "  do work  ")); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	got := receiveMessage(t, worker.started, "worker message")
	if got.Content != "  do work  " {
		t.Fatalf("Content = %q, want raw content", got.Content)
	}
}

func TestQueuedMessageDoesNotStartAfterShutdownBegins(t *testing.T) {
	t.Parallel()

	worker := &fakeNode{
		started: make(chan Message, 2),
		release: make(chan struct{}),
	}
	runtime, err := New(Config{
		RootNodeID: rootNodeID,
		Nodes:      map[string]Node{rootNodeID: &fakeNode{}, "worker": worker},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = runtime.Stop(context.Background()) }()

	if err := runtime.Enqueue(jobMessage(NewAgentLocator(rootNodeID), NewAgentLocator("worker"), "first")); err != nil {
		t.Fatalf("Enqueue(first) error = %v", err)
	}
	if err := runtime.Enqueue(jobMessage(NewAgentLocator(rootNodeID), NewAgentLocator("worker"), "second")); err != nil {
		t.Fatalf("Enqueue(second) error = %v", err)
	}

	got := receiveMessage(t, worker.started, "first message")
	if got.Content != "first" {
		t.Fatalf("first content = %q, want first", got.Content)
	}

	go func() {
		time.Sleep(10 * time.Millisecond)
		close(worker.release)
	}()
	if err := runtime.Stop(context.Background()); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}

	select {
	case started := <-worker.started:
		t.Fatalf("unexpected message started after shutdown: %+v", started)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestDuplicateNormalizedNodeIDsRejected(t *testing.T) {
	t.Parallel()

	_, err := New(Config{
		RootNodeID: rootNodeID,
		Nodes: map[string]Node{
			rootNodeID: &fakeNode{},
			"Worker":   &fakeNode{},
			"worker":   &fakeNode{},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate node ids") {
		t.Fatalf("New() error = %v, want duplicate normalized node rejection", err)
	}
}

func TestMixedCaseLocalAgentLocatorRoutesToNormalizedNode(t *testing.T) {
	t.Parallel()

	worker := &fakeNode{started: make(chan Message, 1)}
	runtime, cleanup := startTestRuntime(t, zerolog.Nop(), testConfig(nil), map[string]Node{
		rootNodeID: &fakeNode{},
		"worker":   worker,
	})
	defer cleanup()

	msg := jobMessage(NewAgentLocator(rootNodeID), NewLocator(LocatorClassAgent, LocatorTransportLocal, "Worker"), "do work")
	if err := runtime.Enqueue(msg); err != nil {
		t.Fatalf("Enqueue() error = %v", err)
	}

	receiveMessage(t, worker.started, "worker message")
}

func jobMessage(from Locator, to Locator, content string) Message {
	return Message{
		SessionID: "session-a",
		Kind:      MessageKindJob,
		From:      from,
		To:        to,
		Content:   content,
	}
}

func receiveMessage(t *testing.T, ch <-chan Message, label string) Message {
	t.Helper()

	select {
	case msg := <-ch:
		return msg
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", label)
		return Message{}
	}
}

func startTestRuntime(t *testing.T, logger zerolog.Logger, cfg Config, nodes map[string]Node) (*Runtime, func()) {
	t.Helper()

	cfg.RootNodeID = rootNodeID
	cfg.Nodes = nodes
	runtime, err := New(cfg)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	return runtime, func() {
		_ = runtime.Stop(context.Background())
	}
}

func testConfig(targets []Target) Config {
	return Config{
		Targets: targets,
	}
}

const rootNodeID = "taskmaster"

type fakeNode struct {
	mu      sync.Mutex
	outcome Outcome
	emits   []Message
	started chan Message
	release chan struct{}
	done    chan Outcome
}

func (n *fakeNode) Run(ctx context.Context, msg Message, emit EmitFunc) Outcome {
	if n.started != nil {
		n.started <- msg
	}
	for _, emitted := range n.emits {
		_ = emit(ctx, emitted)
	}
	if n.release != nil {
		select {
		case <-n.release:
		case <-ctx.Done():
			return Outcome{Status: OutcomeStatusFailed, Err: ctx.Err()}
		}
	}
	n.mu.Lock()
	outcome := n.outcome
	n.mu.Unlock()
	if outcome.Status == "" && outcome.Err == nil {
		outcome.Status = OutcomeStatusCompleted
	}
	if n.done != nil {
		n.done <- outcome
	}
	return outcome
}

func (n *fakeNode) Close() error {
	return nil
}

type fakeTarget struct {
	supportedLocators map[string]bool
	dispatchStarted   chan Message
	err               error
}

func (t *fakeTarget) Supports(locator Locator) bool {
	return t.supportedLocators[locatorKey(locator)]
}

func (t *fakeTarget) DispatchMessage(_ context.Context, msg Message) error {
	if t.dispatchStarted != nil {
		t.dispatchStarted <- msg
	}
	return t.err
}
