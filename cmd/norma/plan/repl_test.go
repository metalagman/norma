package plancmd

import (
	"bytes"
	"io"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestPlannerREPLConfig_DoesNotConfigureStartupPrompt(t *testing.T) {
	cmd := &cobra.Command{}
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)

	got := plannerREPLConfig(cmd, t.TempDir(), plannerRuntimeConfig{PlannerProviderID: "planner-id"})
	if got.StartupPrompt != "" {
		t.Fatalf("StartupPrompt = %q, want empty", got.StartupPrompt)
	}
	if got.StartupPromptSilent {
		t.Fatal("StartupPromptSilent = true, want false")
	}
	if got.AppName != plannerREPLAppName {
		t.Fatalf("AppName = %q, want %q", got.AppName, plannerREPLAppName)
	}
	if got.UserID != plannerREPLUserID {
		t.Fatalf("UserID = %q, want %q", got.UserID, plannerREPLUserID)
	}
	if got.AgentFactory == nil {
		t.Fatal("AgentFactory = nil, want non-nil")
	}
}

func TestPrintPlannerREPLIntro_PrintsExpectedPrompt(t *testing.T) {
	var out bytes.Buffer
	if err := printPlannerREPLIntro(&out); err != nil {
		t.Fatalf("printPlannerREPLIntro() error = %v", err)
	}
	if got := out.String(); got != plannerREPLIntroMsg+"\n" {
		t.Fatalf("intro output = %q, want %q", got, plannerREPLIntroMsg+"\n")
	}
}
