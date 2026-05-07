package taskmaster

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/normahq/norma/internal/apps/taskmastercore"
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
		"generic Taskmaster async root agent",
		"one plain-text child agent named worker",
		"taskmaster.schedule_task",
		"session_id",
		"class: agent, transport: local, key: worker",
		"class: integration, transport: cli, key: log",
		"completion goes only to the current log",
		"background timer may also deliver simple hello world goals",
		"does not finish on your turn completion",
		"host context is canceled",
		"Do not impose a fixed workflow or phase order",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rootInstruction() = %q, want substring %q", got, want)
		}
	}
	for _, unwanted := range []string{
		"strict PDCA",
		"plan -> do -> check -> act",
		"plan, do, check, and act",
		"taskmaster.finish",
		"`verdict:`",
		"`decision:`",
	} {
		if strings.Contains(got, unwanted) {
			t.Fatalf("rootInstruction() = %q, do not want substring %q", got, unwanted)
		}
	}
}

func TestWorkerInstructionIsGenericPlainText(t *testing.T) {
	t.Parallel()

	for _, want := range []string{
		"generic plain-text worker",
		"Return only the useful result as plain text",
		"Do not use JSON, schemas, field names, or code fences",
	} {
		if !strings.Contains(workerInstruction, want) {
			t.Fatalf("workerInstruction = %q, want substring %q", workerInstruction, want)
		}
	}
}

func TestBackgroundGoalSourceEmitsHelloWorld(t *testing.T) {
	t.Parallel()

	tickCh := make(chan time.Time, 1)
	fake := &fakeTicker{ch: tickCh}
	source := backgroundGoalSource(time.Second, func(time.Duration) ticker { return fake })

	got := make(chan taskmastercore.IngressRequest, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go source(ctx, func(req taskmastercore.IngressRequest) error {
		got <- req
		return nil
	})

	tickCh <- time.Now()
	select {
	case req := <-got:
		if req.Prompt != timerGoalMessage {
			t.Fatalf("prompt = %q, want %q", req.Prompt, timerGoalMessage)
		}
		if req.SessionID == "" {
			t.Fatal("background goal session_id is empty")
		}
		if got := req.Source; got.Class != taskmastercore.LocatorClassIntegration || got.Transport != taskmastercore.LocatorTransportTimer || got.Key != taskmastercore.DefaultTimerKey {
			t.Fatalf("source = %+v, want integration/timer/default", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background goal source did not emit synthetic goal")
	}
	cancel()
}

type fakeTicker struct {
	ch      chan time.Time
	stopped bool
}

func (t *fakeTicker) Chan() <-chan time.Time { return t.ch }

func (t *fakeTicker) Stop() { t.stopped = true }
