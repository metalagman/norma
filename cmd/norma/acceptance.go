package main

import (
	"fmt"

	"github.com/metalagman/norma/internal/task"
)

func normalizeAC(texts []string) []task.AcceptanceCriterion {
	if len(texts) == 0 {
		return nil
	}
	out := make([]task.AcceptanceCriterion, 0, len(texts))
	for i, text := range texts {
		id := fmt.Sprintf("AC%d", i+1)
		out = append(out, task.AcceptanceCriterion{ID: id, Text: text})
	}
	return out
}
