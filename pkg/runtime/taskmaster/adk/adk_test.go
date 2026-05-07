package adk

import (
	"strings"
	"testing"

	"google.golang.org/genai"
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

func TestContentTextJoinsTextParts(t *testing.T) {
	t.Parallel()

	content := &genai.Content{
		Parts: []*genai.Part{
			{Text: "first"},
			{Text: "  "},
			{Text: "second"},
		},
	}
	if got := contentText(content); got != "first\n\nsecond" {
		t.Fatalf("contentText() = %q, want first\\n\\nsecond", got)
	}
}
