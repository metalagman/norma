package taskmaster

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
	"github.com/rs/zerolog"
)

func TestBuildCodexACPCommand(t *testing.T) {
	t.Parallel()

	got := BuildCodexACPCommand("")
	want := []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("BuildCodexACPCommand(\"\") = %v, want %v", got, want)
	}

	got = BuildCodexACPCommand("/tmp/bridge")
	if len(got) != 1 || got[0] != "/tmp/bridge" {
		t.Fatalf("BuildCodexACPCommand(custom) = %v, want custom binary only", got)
	}
}

func TestRootInstructionDefinesGenericCoordinator(t *testing.T) {
	t.Parallel()

	got := rootInstruction()
	for _, want := range []string{
		"generic Taskmaster inbox agent",
		"optional CLI bootstrap tasks and periodic timer tasks",
		"host may also route your plain-text result to the current log sink",
		"does not finish on your turn completion",
		"host context is canceled",
		"Do not impose a fixed workflow or phase order",
		"plain-text task content",
		"Return only the useful result as plain text",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rootInstruction() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{
		"strict PDCA",
		"plan -> do -> check -> act",
		"plan, do, check, and act",
		"one plain-text child agent named worker",
		"taskmaster.schedule_task",
		"taskmaster.finish",
		"`verdict:`",
		"`decision:`",
		"current best summary",
		"task_id",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("rootInstruction() = %q, do not want substring %q", got, unwanted)
		}
	}
}

func TestBuildBootstrapMessage(t *testing.T) {
	t.Parallel()

	if got := buildBootstrapMessage(""); got != nil {
		t.Fatalf("buildBootstrapMessage(empty) = %+v, want nil", got)
	}

	got := buildBootstrapMessage("  count go files  ")
	if got == nil {
		t.Fatal("buildBootstrapMessage(non-empty) = nil, want message")
		return
	}
	if got.SessionID != bootstrapSessionID {
		t.Fatalf("SessionID = %q, want %q", got.SessionID, bootstrapSessionID)
	}
	if got.Kind != taskmasterrt.MessageKindJob {
		t.Fatalf("Kind = %q, want job", got.Kind)
	}
	if !reflect.DeepEqual(got.From, taskmasterrt.NewCLIInputLocator()) {
		t.Fatalf("From = %+v, want CLI input locator", got.From)
	}
	if !reflect.DeepEqual(got.To, taskmasterrt.NewAgentLocator(taskmasterAgentID)) {
		t.Fatalf("To = %+v, want root agent locator", got.To)
	}
	if got.Content != "  count go files  " {
		t.Fatalf("Content = %q, want raw bootstrap content", got.Content)
	}
}

func TestBackgroundTaskSourceEmitsHelloWorld(t *testing.T) {
	t.Parallel()

	tickCh := make(chan time.Time, 1)
	fake := &fakeTicker{ch: tickCh}

	rootRunner := &fakeLocalRunner{started: make(chan startedTask, 1)}
	runtime, err := taskmasterrt.New(taskmasterrt.Config{
		RootNodeID: taskmasterAgentID,
		Nodes:      map[string]taskmasterrt.Node{taskmasterAgentID: rootRunner},
		Targets:    []taskmasterrt.Target{taskmasterrt.NewCLILogTarget(zerolog.Nop())},
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Start(context.Background()); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = runtime.Stop(context.Background()) }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go backgroundTaskSource(ctx, runtime, time.Second, func(time.Duration) ticker { return fake })

	tickCh <- time.Now()
	select {
	case started := <-rootRunner.started:
		if started.Content != timerContentMessage {
			t.Fatalf("started.Content = %q, want raw timer content", started.Content)
		}
		if !reflect.DeepEqual(started.From, taskmasterrt.NewTimerSourceLocator()) {
			t.Fatalf("started.From = %+v, want timer source locator", started.From)
		}
		if !reflect.DeepEqual(started.To, taskmasterrt.NewAgentLocator(taskmasterAgentID)) {
			t.Fatalf("started.To = %+v, want root agent locator", started.To)
		}
		if started.SessionID == "" {
			t.Fatal("background task session_id is empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background task source did not emit synthetic content")
	}
}

type fakeTicker struct {
	ch      chan time.Time
	stopped bool
}

func (t *fakeTicker) Chan() <-chan time.Time { return t.ch }

func (t *fakeTicker) Stop() { t.stopped = true }

type startedTask struct {
	SessionID string
	From      taskmasterrt.Locator
	To        taskmasterrt.Locator
	Content   string
}

type fakeLocalRunner struct {
	started chan startedTask
}

func (r *fakeLocalRunner) Run(_ context.Context, msg taskmasterrt.Message, _ taskmasterrt.EmitFunc) taskmasterrt.Outcome {
	if r.started != nil {
		r.started <- startedTask{SessionID: msg.SessionID, From: msg.From, To: msg.To, Content: msg.Content}
	}
	return taskmasterrt.Outcome{Status: taskmasterrt.OutcomeStatusCompleted}
}

func (r *fakeLocalRunner) Close() error {
	return nil
}
