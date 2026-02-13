package planner

import (
	"context"
	"fmt"
	"strings"

	"github.com/metalagman/norma/internal/task"
)

// BacklogWriter defines the Beads write operations needed by planning.
type BacklogWriter interface {
	AddEpic(ctx context.Context, title, goal string) (string, error)
	AddFeatureDetailed(ctx context.Context, epicID, title, description string) (string, error)
	AddTaskDetailed(ctx context.Context, parentID, title, goal string, criteria []task.AcceptanceCriterion, runID *string) (string, error)
}

// BeadsTool persists decomposition results to Beads via bd.
type BeadsTool struct {
	writer BacklogWriter
}

func NewBeadsTool(writer BacklogWriter) *BeadsTool {
	return &BeadsTool{writer: writer}
}

type ApplyResult struct {
	EpicID   string
	Features []AppliedFeature
}

type AppliedFeature struct {
	FeatureID string
	TaskIDs   []string
}

func (b *BeadsTool) Apply(ctx context.Context, plan Decomposition) (ApplyResult, error) {
	if b.writer == nil {
		return ApplyResult{}, fmt.Errorf("writer is required")
	}
	if err := plan.Validate(); err != nil {
		return ApplyResult{}, err
	}

	epicGoal := strings.TrimSpace(plan.Epic.Description)
	if epicGoal == "" {
		epicGoal = strings.TrimSpace(plan.Summary)
	}

	epicID, err := b.writer.AddEpic(ctx, plan.Epic.Title, epicGoal)
	if err != nil {
		return ApplyResult{}, fmt.Errorf("create epic: %w", err)
	}

	res := ApplyResult{
		EpicID:   epicID,
		Features: make([]AppliedFeature, 0, len(plan.Features)),
	}

	for _, feature := range plan.Features {
		featureID, err := b.writer.AddFeatureDetailed(ctx, epicID, feature.Title, feature.Description)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("create feature %q: %w", feature.Title, err)
		}

		appliedFeature := AppliedFeature{
			FeatureID: featureID,
			TaskIDs:   make([]string, 0, len(feature.Tasks)),
		}
		for _, t := range feature.Tasks {
			goal := readyContract(t)
			taskID, err := b.writer.AddTaskDetailed(ctx, featureID, t.Title, goal, nil, nil)
			if err != nil {
				return ApplyResult{}, fmt.Errorf("create task %q: %w", t.Title, err)
			}
			appliedFeature.TaskIDs = append(appliedFeature.TaskIDs, taskID)
		}

		res.Features = append(res.Features, appliedFeature)
	}

	return res, nil
}

func readyContract(t TaskPlan) string {
	var sb strings.Builder
	sb.WriteString("Objective: ")
	sb.WriteString(strings.TrimSpace(t.Objective))
	sb.WriteString("\n")
	sb.WriteString("Artifact: ")
	sb.WriteString(strings.TrimSpace(t.Artifact))
	sb.WriteString("\n")
	sb.WriteString("Verify:\n")
	for _, step := range t.Verify {
		if strings.TrimSpace(step) == "" {
			continue
		}
		sb.WriteString("- ")
		sb.WriteString(strings.TrimSpace(step))
		sb.WriteString("\n")
	}
	if notes := strings.TrimSpace(t.Notes); notes != "" {
		sb.WriteString("Notes: ")
		sb.WriteString(notes)
		sb.WriteString("\n")
	}
	return strings.TrimSpace(sb.String())
}
