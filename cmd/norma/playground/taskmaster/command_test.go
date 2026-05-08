package taskmastercmd

import "testing"

func TestCommandValidation(t *testing.T) {
	t.Parallel()

	cmd := Command()
	if cmd.Flags().Lookup("model") != nil {
		t.Fatal("Command() exposes --model flag, want fixed model config")
	}
	if cmd.Flags().Lookup("max-tool-calls") != nil {
		t.Fatal("Command() exposes --max-tool-calls, want no taskmaster tool-call guard")
	}

	cmd = Command()
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err == nil {
		t.Fatal("Command().Execute() error = nil, want missing content error")
	}
}
