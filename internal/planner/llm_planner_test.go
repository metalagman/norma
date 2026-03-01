package planner

import (
	"testing"

	"google.golang.org/genai"
)

func TestParseDecompositionFromCandidates_UsesCompleteTurnBeforeTrailingChunk(t *testing.T) {
	t.Parallel()

	fullTurn := "Here is the final plan:\n```json\n" + validDecompositionJSON + "\n```"
	trailingChunk := "continue"

	dec, err := parseDecompositionFromCandidates(fullTurn, trailingChunk)
	if err != nil {
		t.Fatalf("parseDecompositionFromCandidates returned error: %v", err)
	}
	if dec.Epic.Title != "Epic Title" {
		t.Fatalf("epic title = %q, want %q", dec.Epic.Title, "Epic Title")
	}
	if got := len(dec.Features); got != 1 {
		t.Fatalf("features len = %d, want %d", got, 1)
	}
}

func TestParseDecompositionFromCandidates_FallsBackToLaterCandidate(t *testing.T) {
	t.Parallel()

	dec, err := parseDecompositionFromCandidates("continue", validDecompositionJSON)
	if err != nil {
		t.Fatalf("parseDecompositionFromCandidates returned error: %v", err)
	}
	if dec.Epic.Title != "Epic Title" {
		t.Fatalf("epic title = %q, want %q", dec.Epic.Title, "Epic Title")
	}
}

func TestExtractTextFromContent_ConcatenatesTextParts(t *testing.T) {
	t.Parallel()

	content := &genai.Content{
		Parts: []*genai.Part{
			{Text: "alpha"},
			{Text: "beta"},
			{FunctionCall: &genai.FunctionCall{Name: "persist_plan"}},
		},
	}

	got := extractTextFromContent(content)
	if got != "alphabeta" {
		t.Fatalf("extractTextFromContent = %q, want %q", got, "alphabeta")
	}
}

const validDecompositionJSON = `{
  "summary": "S",
  "epic": {
    "title": "Epic Title",
    "description": "Epic Description"
  },
  "features": [
    {
      "title": "Feature A",
      "description": "Feature A Description",
      "tasks": [
        {
          "title": "Task A1",
          "objective": "Do A1",
          "artifact": "internal/planner/llm_planner.go",
          "verify": [
            "go test ./..."
          ]
        }
      ]
    }
  ]
}`
