package goalkeepercmd

import (
	"testing"
)

func TestCommandValidation(t *testing.T) {
	t.Parallel()

	cmd := Command()
	if cmd.Flags().Lookup("model") != nil {
		t.Fatal("Command() exposes --model flag, want fixed model config")
	}

	cmd = Command()
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

func TestNotifyCommandValidation(t *testing.T) {
	t.Parallel()

	cmd := NotifyCommand()
	if cmd.Flags().Lookup("model") != nil {
		t.Fatal("NotifyCommand() exposes --model flag, want fixed model config")
	}

	cmd = NotifyCommand()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("NotifyCommand().Execute() error = nil, want missing goal error")
	}

	cmd = NotifyCommand()
	cmd.SetArgs([]string{"--max-tool-calls", "-1", "goal"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("NotifyCommand().Execute() error = nil, want max-tool-calls validation error")
	}
}
