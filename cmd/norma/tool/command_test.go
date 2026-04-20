package toolcmd

import (
	"testing"
)

func TestCommandRegistered(t *testing.T) {
	cmd := Command()

	// Test acp-dump subcommand
	acpDumpSub, _, err := cmd.Find([]string{"acp-dump"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if acpDumpSub == nil || acpDumpSub.Name() != "acp-dump" {
		t.Fatalf("subcommand = %v, want acp-dump", acpDumpSub)
	}
	if got := acpDumpSub.Flags().Lookup("json"); got == nil {
		t.Fatalf("expected --json flag on acp-dump command")
	}

	// Test mcp-dump subcommand
	mcpDumpSub, _, err := cmd.Find([]string{"mcp-dump"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if mcpDumpSub == nil || mcpDumpSub.Name() != "mcp-dump" {
		t.Fatalf("subcommand = %v, want mcp-dump", mcpDumpSub)
	}
	if got := mcpDumpSub.Flags().Lookup("json"); got == nil {
		t.Fatalf("expected --json flag on mcp-dump command")
	}

	// Test acp-repl subcommand
	acpReplSub, _, err := cmd.Find([]string{"acp-repl"})
	if err != nil {
		t.Fatalf("Find() error = %v", err)
	}
	if acpReplSub == nil || acpReplSub.Name() != "acp-repl" {
		t.Fatalf("subcommand = %v, want acp-repl", acpReplSub)
	}
	if got := acpReplSub.Flags().Lookup("model"); got == nil {
		t.Fatalf("expected --model flag on acp-repl command")
	}
	if got := acpReplSub.Flags().Lookup("mode"); got == nil {
		t.Fatalf("expected --mode flag on acp-repl command")
	}
}
