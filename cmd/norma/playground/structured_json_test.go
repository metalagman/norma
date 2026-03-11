package playgroundcmd

import (
	"encoding/json"
	"testing"
)

func TestNormalizeStructuredOutputUsesLastValidObject(t *testing.T) {
	t.Parallel()

	raw := `Example only: {"status":"ok"} Final: {"status":"ok","summary":{"text":"hello"},"progress":{"title":"done","details":[]}}`
	got, err := normalizeStructuredOutput(raw)
	if err != nil {
		t.Fatalf("normalizeStructuredOutput() error = %v", err)
	}

	var out structuredOutput
	if err := json.Unmarshal(got, &out); err != nil {
		t.Fatalf("unmarshal normalized output error = %v", err)
	}
	if out.Summary.Text != defaultStructuredMessage {
		t.Fatalf("summary.text = %q, want %q", out.Summary.Text, defaultStructuredMessage)
	}
	if out.Progress.Title != "done" {
		t.Fatalf("progress.title = %q, want %q", out.Progress.Title, "done")
	}
}
