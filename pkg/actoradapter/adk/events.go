package adk

import (
	"strings"

	"google.golang.org/adk/session"
)

func visibleText(ev *session.Event) string {
	if ev == nil || ev.Content == nil {
		return ""
	}
	parts := make([]string, 0, len(ev.Content.Parts))
	for _, part := range ev.Content.Parts {
		if part == nil || part.Thought {
			continue
		}
		text := strings.TrimSpace(part.Text)
		if text == "" {
			continue
		}
		parts = append(parts, text)
	}
	return strings.Join(parts, "\n\n")
}
