package goalkeepercmd

import (
	"testing"
)

func TestCommandValidation(t *testing.T) {
	t.Parallel()

	cmd := Command()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("Command().Execute() error = nil, want missing goal error")
	}

	cmd = Command()
	cmd.SetArgs([]string{"--max-tool-calls", "-1", "goal"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("Command().Execute() error = nil, want max-tool-calls validation error")
	}
}
