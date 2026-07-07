package acprepl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	adkagent "google.golang.org/adk/v2/agent"
	runnerpkg "google.golang.org/adk/v2/runner"
	"google.golang.org/adk/v2/session"
)

const testStartupPrompt = "hello"

func TestRunAgentREPL_SendsStartupPromptBeforeInputLoop(t *testing.T) {
	origNewAgentRunner := replNewAgentRunner
	origRunACPToolTurn := replRunACPToolTurn
	t.Cleanup(func() {
		replNewAgentRunner = origNewAgentRunner
		replRunACPToolTurn = origRunACPToolTurn
	})

	replNewAgentRunner = func(context.Context, adkagent.Agent, string, string) (*runnerpkg.Runner, session.Session, error) {
		return nil, newTestSession(t), nil
	}

	prompts := make([]string, 0, 1)
	replRunACPToolTurn = func(_ context.Context, _ *runnerpkg.Runner, _ session.Session, _ string, _ *acpToolTerminal, prompt string) error {
		prompts = append(prompts, prompt)
		return nil
	}

	err := RunAgentREPL(context.Background(), AgentREPLConfig{
		Stdin:  strings.NewReader("exit\n"),
		Stdout: io.Discard,
		Stderr: io.Discard,
		AgentFactory: func(context.Context, PermissionHandler, io.Writer) (adkagent.Agent, func() error, error) {
			return nil, nil, nil
		},
		StartupPrompt: testStartupPrompt,
	})
	if err != nil {
		t.Fatalf("RunAgentREPL() error = %v", err)
	}
	if len(prompts) != 1 || prompts[0] != testStartupPrompt {
		t.Fatalf("startup prompts = %v, want [%s]", prompts, testStartupPrompt)
	}
}

func TestRunAgentREPL_StartupPromptSilentSuppressesOutput(t *testing.T) {
	origNewAgentRunner := replNewAgentRunner
	origRunACPToolTurn := replRunACPToolTurn
	t.Cleanup(func() {
		replNewAgentRunner = origNewAgentRunner
		replRunACPToolTurn = origRunACPToolTurn
	})

	replNewAgentRunner = func(context.Context, adkagent.Agent, string, string) (*runnerpkg.Runner, session.Session, error) {
		return nil, newTestSession(t), nil
	}

	replRunACPToolTurn = func(_ context.Context, _ *runnerpkg.Runner, _ session.Session, _ string, ui *acpToolTerminal, prompt string) error {
		if strings.TrimSpace(prompt) == testStartupPrompt {
			ui.Println("startup-output")
		}
		return nil
	}

	var stdout bytes.Buffer
	err := RunAgentREPL(context.Background(), AgentREPLConfig{
		Stdin:               strings.NewReader("exit\n"),
		Stdout:              &stdout,
		Stderr:              io.Discard,
		StartupPrompt:       testStartupPrompt,
		StartupPromptSilent: true,
		AgentFactory: func(context.Context, PermissionHandler, io.Writer) (adkagent.Agent, func() error, error) {
			return nil, nil, nil
		},
	})
	if err != nil {
		t.Fatalf("RunAgentREPL() error = %v", err)
	}
	if got := stdout.String(); got != "> " {
		t.Fatalf("stdout = %q, want only interactive prompt", got)
	}
}

func TestRunAgentREPL_StartupPromptErrorStopsStartup(t *testing.T) {
	origNewAgentRunner := replNewAgentRunner
	origRunACPToolTurn := replRunACPToolTurn
	t.Cleanup(func() {
		replNewAgentRunner = origNewAgentRunner
		replRunACPToolTurn = origRunACPToolTurn
	})

	replNewAgentRunner = func(context.Context, adkagent.Agent, string, string) (*runnerpkg.Runner, session.Session, error) {
		return nil, newTestSession(t), nil
	}

	wantErr := errors.New("startup failed")
	replRunACPToolTurn = func(_ context.Context, _ *runnerpkg.Runner, _ session.Session, _ string, _ *acpToolTerminal, prompt string) error {
		if strings.TrimSpace(prompt) == testStartupPrompt {
			return wantErr
		}
		return nil
	}

	err := RunAgentREPL(context.Background(), AgentREPLConfig{
		Stdin:  strings.NewReader("exit\n"),
		Stdout: io.Discard,
		Stderr: io.Discard,
		AgentFactory: func(context.Context, PermissionHandler, io.Writer) (adkagent.Agent, func() error, error) {
			return nil, nil, nil
		},
		StartupPrompt: testStartupPrompt,
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("RunAgentREPL() error = %v, want %v", err, wantErr)
	}
}

func newTestSession(t *testing.T) session.Session {
	t.Helper()
	sessionService := session.InMemoryService()
	created, err := sessionService.Create(context.Background(), &session.CreateRequest{
		AppName: "acprepl-test",
		UserID:  "acprepl-test-user",
	})
	if err != nil {
		t.Fatalf("session.Create() error = %v", err)
	}
	return created.Session
}
