package taskmaster

import (
	"context"
	"strings"
	"testing"
	"time"
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
		"taskmaster.finish",
		"human_output current_log",
		"completion goes only to the current log",
		"background timer may also deliver simple hello world goals",
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

	got := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go source(ctx, func(prompt string) error {
		got <- prompt
		return nil
	})

	tickCh <- time.Now()
	select {
	case prompt := <-got:
		if prompt != timerGoalMessage {
			t.Fatalf("prompt = %q, want %q", prompt, timerGoalMessage)
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
