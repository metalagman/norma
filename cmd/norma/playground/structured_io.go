package playgroundcmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"iter"
	"strings"

	"github.com/rs/zerolog"
	"google.golang.org/adk/session"
)

type structuredInput struct {
	Input structuredInputPayload `json:"input"`
}

type structuredInputPayload struct {
	Message string `json:"message"`
}

type structuredOutput struct {
	Status   string                  `json:"status"`
	Summary  structuredOutputSummary `json:"summary"`
	Progress structuredOutputStep    `json:"progress"`
}

type structuredOutputSummary struct {
	Text string `json:"text"`
}

type structuredOutputStep struct {
	Title   string   `json:"title"`
	Details []string `json:"details"`
}

func collectStructuredModelOutput(events iter.Seq2[*session.Event, error], logger zerolog.Logger) (string, error) {
	var accumulated strings.Builder
	eventCount := 0
	chunkCount := 0
	sawTurnComplete := false

	for ev, err := range events {
		if err != nil {
			return "", err
		}
		eventCount++
		if ev == nil || ev.Content == nil {
			logger.Debug().
				Int("event_index", eventCount).
				Msg("received event without content while accumulating model text")
			continue
		}

		nonEmptyParts := 0
		for partIdx, part := range ev.Content.Parts {
			if part == nil || strings.TrimSpace(part.Text) == "" || part.Thought {
				continue
			}
			chunkCount++
			nonEmptyParts++
			accumulated.WriteString(part.Text)
			logger.Debug().
				Int("event_index", eventCount).
				Int("part_index", partIdx).
				Bool("partial", ev.Partial).
				Bool("turn_complete", ev.TurnComplete).
				Int("chunk_len", len(part.Text)).
				Str("chunk_preview", truncateForLog(part.Text, 160)).
				Msg("accumulated model chunk")
		}
		if nonEmptyParts == 0 {
			logger.Debug().
				Int("event_index", eventCount).
				Bool("partial", ev.Partial).
				Bool("turn_complete", ev.TurnComplete).
				Msg("event had no text parts")
		}
		if ev.TurnComplete {
			sawTurnComplete = true
			logger.Debug().
				Int("event_index", eventCount).
				Msg("encountered turn_complete; stopping accumulation")
			break
		}
	}
	logger.Debug().
		Int("event_count", eventCount).
		Int("chunk_count", chunkCount).
		Int("accumulated_len", accumulated.Len()).
		Bool("saw_turn_complete", sawTurnComplete).
		Msg("finished accumulating model turn text")
	return accumulated.String(), nil
}

func normalizeStructuredOutput(raw string) ([]byte, error) {
	text := strings.TrimSpace(raw)
	if text == "" {
		return nil, fmt.Errorf("empty output")
	}

	var out structuredOutput
	if err := json.Unmarshal([]byte(text), &out); err == nil {
		return marshalValidatedStructuredOutput(out)
	}

	candidates := extractBalancedJSONObjects([]byte(text))
	for i := len(candidates) - 1; i >= 0; i-- {
		if err := json.Unmarshal(candidates[i], &out); err != nil {
			continue
		}
		return marshalValidatedStructuredOutput(out)
	}

	return nil, fmt.Errorf("output is not valid structured JSON")
}

func marshalValidatedStructuredOutput(out structuredOutput) ([]byte, error) {
	if strings.TrimSpace(out.Status) == "" {
		return nil, fmt.Errorf("missing status in structured output")
	}
	if strings.TrimSpace(out.Summary.Text) == "" {
		return nil, fmt.Errorf("missing summary.text in structured output")
	}
	if strings.TrimSpace(out.Progress.Title) == "" {
		return nil, fmt.Errorf("missing progress.title in structured output")
	}
	if out.Progress.Details == nil {
		out.Progress.Details = []string{}
	}
	return json.Marshal(out)
}

func prettyJSON(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	var out bytes.Buffer
	if err := json.Indent(&out, data, "", "  "); err != nil {
		return strings.TrimSpace(string(data))
	}
	return out.String()
}

func truncateForLog(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	return s[:limit] + "...(truncated)"
}

func extractBalancedJSONObjects(data []byte) [][]byte {
	start := -1
	depth := 0
	inString := false
	escaped := false
	out := make([][]byte, 0, 4)

	for i, b := range data {
		if start == -1 {
			if b == '{' {
				start = i
				depth = 1
				inString = false
				escaped = false
			}
			continue
		}

		if inString {
			if escaped {
				escaped = false
				continue
			}
			switch b {
			case '\\':
				escaped = true
			case '"':
				inString = false
			}
			continue
		}

		switch b {
		case '"':
			inString = true
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				obj := bytes.TrimSpace(data[start : i+1])
				if len(obj) > 0 {
					cp := make([]byte, len(obj))
					copy(cp, obj)
					out = append(out, cp)
				}
				start = -1
			}
		}
	}

	return out
}
