package proxycmd

import "testing"

func TestCommandRegistered(t *testing.T) {
	cmd := Command()

	sub, _, err := cmd.Find([]string{"codex-acp"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if sub == nil || sub.Name() != "codex-acp" {
		t.Fatalf("subcommand = %v, want codex-acp", sub)
	}
	if got := sub.Flags().Lookup("name"); got == nil {
		t.Fatalf("expected --name flag on codex-acp command")
	}
	if got := sub.Flags().Lookup("codex-model"); got == nil {
		t.Fatalf("expected --codex-model flag on codex-acp command")
	}
	if got := sub.Flags().Lookup("codex-arg"); got != nil {
		t.Fatalf("did not expect deprecated --codex-arg flag on codex-acp command")
	}
	if err := sub.Args(sub, []string{"--", "--trace"}); err == nil {
		t.Fatalf("expected codex-acp to reject positional arguments")
	}
}
