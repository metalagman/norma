package playgroundcmd

import "testing"

func TestPlaygroundCommandRegistered(t *testing.T) {
	cmd := Command()
	for _, name := range []string{
		"goalkeeper",
		"goalkeeper-actor",
		"taskmaster",
		"taskmaster-chat",
		"pdca-taskmaster",
		"pdca-sync",
	} {
		t.Run(name, func(t *testing.T) {
			sub, _, err := cmd.Find([]string{name})
			if err != nil {
				t.Fatalf("find %s subcommand: %v", name, err)
			}
			if sub.Name() != name {
				t.Fatalf("subcommand = %q, want %q", sub.Name(), name)
			}
		})
	}
}
