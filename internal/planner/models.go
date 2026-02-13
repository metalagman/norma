package planner

import (
	"fmt"
	"strings"
)

const (
	ModeWizard = "wizard"
	ModeAuto   = "auto"
)

// Clarification captures one human-provided answer for wizard planning.
type Clarification struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Request is the planning request passed to the planning agent.
type Request struct {
	EpicDescription string          `json:"epic_description"`
	Mode            string          `json:"mode"`
	Clarifications  []Clarification `json:"clarifications,omitempty"`
}

func (r Request) Validate() error {
	if strings.TrimSpace(r.EpicDescription) == "" {
		return fmt.Errorf("epic description is required")
	}
	switch strings.TrimSpace(r.Mode) {
	case ModeAuto, ModeWizard:
	default:
		return fmt.Errorf("unsupported mode %q", r.Mode)
	}
	return nil
}

// Decomposition is the planning output that will be persisted to Beads.
type Decomposition struct {
	Summary  string        `json:"summary"`
	Epic     EpicPlan      `json:"epic"`
	Features []FeaturePlan `json:"features"`
}

func (d Decomposition) Validate() error {
	if strings.TrimSpace(d.Epic.Title) == "" {
		return fmt.Errorf("epic title is required")
	}
	if len(d.Features) == 0 {
		return fmt.Errorf("at least one feature is required")
	}
	for i := range d.Features {
		if err := d.Features[i].Validate(); err != nil {
			return fmt.Errorf("feature[%d]: %w", i, err)
		}
	}
	return nil
}

type EpicPlan struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type FeaturePlan struct {
	Title       string     `json:"title"`
	Description string     `json:"description"`
	Tasks       []TaskPlan `json:"tasks"`
}

func (f FeaturePlan) Validate() error {
	if strings.TrimSpace(f.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if len(f.Tasks) == 0 {
		return fmt.Errorf("at least one task is required")
	}
	for i := range f.Tasks {
		if err := f.Tasks[i].Validate(); err != nil {
			return fmt.Errorf("task[%d]: %w", i, err)
		}
	}
	return nil
}

type TaskPlan struct {
	Title     string   `json:"title"`
	Objective string   `json:"objective"`
	Artifact  string   `json:"artifact"`
	Verify    []string `json:"verify"`
	Notes     string   `json:"notes,omitempty"`
}

func (t TaskPlan) Validate() error {
	if strings.TrimSpace(t.Title) == "" {
		return fmt.Errorf("title is required")
	}
	if strings.TrimSpace(t.Objective) == "" {
		return fmt.Errorf("objective is required")
	}
	if strings.TrimSpace(t.Artifact) == "" {
		return fmt.Errorf("artifact is required")
	}
	if len(t.Verify) == 0 {
		return fmt.Errorf("at least one verify step is required")
	}
	return nil
}
