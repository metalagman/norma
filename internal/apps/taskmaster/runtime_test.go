package taskmaster

import (
	"context"
	"strings"
	"testing"
	"time"

	taskmasterrt "github.com/normahq/norma/pkg/runtime/taskmaster"
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
		"Keep coordinating work and updating your current best summary",
		"Do not impose a fixed workflow or phase order",
		"plain-text task content",
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

func TestFormatIngressContent(t *testing.T) {
	t.Parallel()

	got := formatIngressContent("session-a", taskmasterrt.NewCLIInputLocator(), "hello")
	for _, want := range []string{"Session ID:\nsession-a", "Source:\nintegration/cli/input", "Prompt:\nhello"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatIngressContent() = %q, want substring %q", got, want)
		}
	}
}

func TestBackgroundTaskSourceEmitsHelloWorld(t *testing.T) {
	t.Parallel()

	tickCh := make(chan time.Time, 1)
	fake := &fakeTicker{ch: tickCh}

	rootRunner := &fakeLocalRunner{started: make(chan startedTask, 1)}
	runtime, err := taskmasterrt.Start(context.Background(), taskmasterrt.Config{
		RootAgentID:     taskmasterAgentID,
		LocalRunners:    map[string]taskmasterrt.LocalRunner{taskmasterAgentID: rootRunner},
		DefaultReportTo: taskmasterrt.NewAgentLocator(taskmasterAgentID),
		ReportTaskContentFormatter: func(source taskmasterrt.Task, output string, err error) string {
			return output
		},
		ShutdownSummaryFormatter: formatContextDoneSummary,
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() { _ = runtime.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go backgroundTaskSource(ctx, runtime, time.Second, func(time.Duration) ticker { return fake })

	tickCh <- time.Now()
	select {
	case started := <-rootRunner.started:
		if !strings.Contains(started.Content, timerGoalMessage) {
			t.Fatalf("started.Content = %q, want hello world content", started.Content)
		}
		if started.SessionID == "" {
			t.Fatal("background task session_id is empty")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("background task source did not emit synthetic goal")
	}
}

func TestFormatContextDoneSummary(t *testing.T) {
	t.Parallel()

	got := formatContextDoneSummary(taskmasterrt.ShutdownSummaryInput{
		LastRootOutput: "latest summary",
		Err:            context.Canceled,
	})
	if !strings.Contains(got, "Run stopped by signal.") {
		t.Fatalf("formatContextDoneSummary() = %q, want stop headline", got)
	}
	if !strings.Contains(got, "Last completed root output:") {
		t.Fatalf("formatContextDoneSummary() = %q, want last-output header", got)
	}
	if !strings.Contains(got, "latest summary") {
		t.Fatalf("formatContextDoneSummary() = %q, want last root output", got)
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
	Content   string
}

type fakeLocalRunner struct {
	started chan startedTask
}

func (r *fakeLocalRunner) RunTask(_ context.Context, task taskmasterrt.Task) (string, error) {
	if r.started != nil {
		r.started <- startedTask{SessionID: task.SessionID, Content: task.Content}
	}
	return "", nil
}

func (r *fakeLocalRunner) Close() error {
	return nil
}
