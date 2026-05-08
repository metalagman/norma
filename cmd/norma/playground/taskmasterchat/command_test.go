package taskmasterchatcmd

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
	if err := cmd.Args(cmd, nil); err != nil {
		t.Fatalf("Args(no args) error = %v, want fake chat startup", err)
	}
	if err := cmd.Args(cmd, []string{"extra"}); err == nil {
		t.Fatal("Args(extra arg) error = nil, want no positional args")
	}
}
