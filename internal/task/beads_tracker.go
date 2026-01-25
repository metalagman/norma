package task

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/metalagman/norma/internal/model"
)

// BeadsTracker implements Tracker using the beads CLI tool.
type BeadsTracker struct {
	// Optional: path to bd executable. If empty, uses "bd" from PATH.
	BinPath string
}

// NewBeadsTracker creates a new beads tracker.
func NewBeadsTracker(binPath string) *BeadsTracker {
	if binPath == "" {
		binPath = "bd"
	}
	return &BeadsTracker{BinPath: binPath}
}

// BeadsIssue represents the JSON structure of a beads issue.
type BeadsIssue struct {
	ID                 string `json:"id"`
	Type               string `json:"type"`
	IssueType          string `json:"issue_type"`
	ParentID           string `json:"parent,omitempty"`
	Title              string `json:"title"`
	Description        string `json:"description"`
	AcceptanceCriteria string `json:"acceptance_criteria"`
	Status             string `json:"status"` // open, in_progress, closed, etc.
	Priority           int    `json:"priority"`
	Assignee           string `json:"assignee"`
	CreatedAt          string `json:"created_at"`
	UpdatedAt          string `json:"updated_at"`
	ExternalRef        string `json:"external_ref,omitempty"`
	// Additional fields we might parse if needed
}

// Add creates a task via bd create.
func (t *BeadsTracker) Add(ctx context.Context, title, goal string, criteria []model.AcceptanceCriterion, runID *string) (string, error) {
	description := strings.TrimSpace(goal)
	args := []string{"create", "--title", title, "--description", description, "--type", "task", "--json", "--quiet"}
	if len(criteria) > 0 {
		args = append(args, "--acceptance", formatAcceptanceCriteria(criteria))
	}
	if runID != nil && strings.TrimSpace(*runID) != "" {
		args = append(args, "--external-ref", strings.TrimSpace(*runID))
	}

	out, err := t.exec(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("bd create: %w", err)
	}

	var issue BeadsIssue
	if err := json.Unmarshal(out, &issue); err != nil {
		return "", fmt.Errorf("parse bd response: %w", err)
	}
	return issue.ID, nil
}

// AddEpic creates an epic via bd create.
func (t *BeadsTracker) AddEpic(ctx context.Context, title, goal string) (string, error) {
	args := []string{"create", "--title", title, "--description", goal, "--type", "epic", "--json", "--quiet"}
	out, err := t.exec(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("bd create epic: %w", err)
	}
	var issue BeadsIssue
	if err := json.Unmarshal(out, &issue); err != nil {
		return "", fmt.Errorf("parse bd response: %w", err)
	}
	return issue.ID, nil
}

// AddFeature creates a feature via bd create with parent epic.
func (t *BeadsTracker) AddFeature(ctx context.Context, epicID, title string) (string, error) {
	// Using type feature
	args := []string{"create", "--title", title, "--type", "feature", "--parent", epicID, "--json", "--quiet"}
	out, err := t.exec(ctx, args...)
	if err != nil {
		return "", fmt.Errorf("bd create feature: %w", err)
	}
	var issue BeadsIssue
	if err := json.Unmarshal(out, &issue); err != nil {
		return "", fmt.Errorf("parse bd response: %w", err)
	}
	return issue.ID, nil
}

// List lists tasks via bd list.
func (t *BeadsTracker) List(ctx context.Context, status *string) ([]Task, error) {
	args := []string{"list", "--json", "--quiet", "--limit", "0"}
	if status != nil {
		// Map norma status to beads status
		beadsStatus := *status
		switch *status {
		case "todo":
			beadsStatus = "open"
		case "planning", "doing", "checking", "acting":
			beadsStatus = "in_progress"
		case "done":
			beadsStatus = "closed"
		case "failed":
			// Beads doesn't have failed. Map to open for now.
			beadsStatus = "open"
		case "stopped":
			beadsStatus = "deferred"
		}
		args = append(args, "--status", beadsStatus)
	} else {
		args = append(args, "--all")
	}

	out, err := t.exec(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("bd list: %w", err)
	}

	var issues []BeadsIssue
	if len(out) > 0 {
		if err := json.Unmarshal(out, &issues); err != nil {
			return nil, fmt.Errorf("parse bd list: %w", err)
		}
	}

	var tasks []Task
	for _, issue := range issues {
		tasks = append(tasks, t.toTask(issue))
	}
	return tasks, nil
}

// ListFeatures lists features for a given epic.
func (t *BeadsTracker) ListFeatures(ctx context.Context, epicID string) ([]Task, error) {
	// bd list --parent <epicID> --type feature
	args := []string{"list", "--parent", epicID, "--type", "feature", "--json", "--quiet", "--limit", "0"}
	out, err := t.exec(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("bd list features: %w", err)
	}

	var issues []BeadsIssue
	if len(out) > 0 {
		if err := json.Unmarshal(out, &issues); err != nil {
			return nil, fmt.Errorf("parse bd list: %w", err)
		}
	}

	var tasks []Task
	for _, issue := range issues {
		tasks = append(tasks, t.toTask(issue))
	}
	return tasks, nil
}

// Children lists child issues for a given parent.
func (t *BeadsTracker) Children(ctx context.Context, parentID string) ([]Task, error) {
	if strings.TrimSpace(parentID) == "" {
		return nil, fmt.Errorf("parent id is required")
	}
	args := []string{"list", "--parent", parentID, "--json", "--quiet", "--limit", "0", "--all"}
	out, err := t.exec(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("bd list children: %w", err)
	}

	var issues []BeadsIssue
	if len(out) > 0 {
		if err := json.Unmarshal(out, &issues); err != nil {
			return nil, fmt.Errorf("parse bd list children: %w", err)
		}
	}

	var tasks []Task
	for _, issue := range issues {
		tasks = append(tasks, t.toTask(issue))
	}
	return tasks, nil
}

// Get fetches a task via bd show.
func (t *BeadsTracker) Get(ctx context.Context, id string) (Task, error) {
	args := []string{"show", id, "--json", "--quiet"}
	out, err := t.exec(ctx, args...)
	if err != nil {
		// bd show returns error if not found?
		return Task{}, fmt.Errorf("bd show: %w", err)
	}

	// bd show outputs a list of issues (even for one ID)
	var issues []BeadsIssue
	if err := json.Unmarshal(out, &issues); err != nil {
		return Task{}, fmt.Errorf("parse bd show: %w", err)
	}
	if len(issues) == 0 {
		return Task{}, fmt.Errorf("task %s not found", id)
	}
	return t.toTask(issues[0]), nil
}

// MarkDone marks a task as done (closed).
func (t *BeadsTracker) MarkDone(ctx context.Context, id string) error {
	_, err := t.exec(ctx, "close", id, "--json", "--quiet")
	return err
}

// MarkStatus updates task status.
func (t *BeadsTracker) MarkStatus(ctx context.Context, id string, status string) error {
	beadsStatus := status
	switch status {
	case "todo":
		beadsStatus = "open"
	case "planning", "doing", "checking", "acting":
		beadsStatus = "in_progress"
	case "done":
		beadsStatus = "closed"
	case "failed":
		// Beads has no failed. Maybe add label?
		// For now map to open + label "failed"? Or just keep open.
		beadsStatus = "open"
	case "stopped":
		beadsStatus = "deferred"
	}

	// If mapping to same status, we use bd update --status
	_, err := t.exec(ctx, "update", id, "--status", beadsStatus, "--json", "--quiet")
	return err
}

// Update updates title and goal.
func (t *BeadsTracker) Update(ctx context.Context, id string, title, goal string) error {
	description := strings.TrimSpace(goal)
	_, err := t.exec(ctx, "update", id, "--title", title, "--description", description, "--json", "--quiet")
	return err
}

// Delete deletes a task.
func (t *BeadsTracker) Delete(ctx context.Context, id string) error {
	_, err := t.exec(ctx, "delete", id, "--json", "--quiet")
	return err
}

// SetRun sets the run ID (as external ref).
func (t *BeadsTracker) SetRun(ctx context.Context, id string, runID string) error {
	trimmedRunID := strings.TrimSpace(runID)
	if trimmedRunID == "" {
		return fmt.Errorf("runID is required")
	}
	_, err := t.exec(ctx, "update", id, "--external-ref", trimmedRunID, "--json", "--quiet")
	return err
}

// AddDependency adds a dependency.
func (t *BeadsTracker) AddDependency(ctx context.Context, taskID, dependsOnID string) error {
	// taskID depends on dependsOnID.
	// beads: bd dep add <task> <dependency>
	_, err := t.exec(ctx, "dep", "add", taskID, dependsOnID, "--json", "--quiet")
	return err
}

// LeafTasks returns ready tasks.
func (t *BeadsTracker) LeafTasks(ctx context.Context) ([]Task, error) {
	// bd ready lists ready tasks
	out, err := t.exec(ctx, "ready", "--json", "--quiet")
	if err != nil {
		return nil, fmt.Errorf("bd ready: %w", err)
	}

	var issues []BeadsIssue
	if len(out) > 0 {
		if err := json.Unmarshal(out, &issues); err != nil {
			return nil, fmt.Errorf("parse bd ready: %w", err)
		}
	}

	var tasks []Task
	for _, issue := range issues {
		tasks = append(tasks, t.toTask(issue))
	}
	return tasks, nil
}

func (t *BeadsTracker) exec(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, t.BinPath, args...)
	// beads relies on PWD for context
	cmd.Dir = "."
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// Pass environment variables if needed
	cmd.Env = os.Environ()

	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("exec %s %v: %w (stderr: %s)", t.BinPath, args, err, stderr.String())
	}
	return stdout.Bytes(), nil
}

func (t *BeadsTracker) toTask(issue BeadsIssue) Task {
	status := "todo"
	switch issue.Status {
	case "open":
		status = "todo"
	case "in_progress", "planning", "doing", "checking", "acting":
		status = "doing"
	case "closed":
		status = "done"
	case "deferred":
		status = "stopped"
		// default keeps "todo"
	}

	goal := strings.TrimSpace(issue.Description)
	goal, legacyAC := splitLegacyAC(goal)
	criteria := parseAcceptanceCriteria(issue.AcceptanceCriteria)
	if len(criteria) == 0 && legacyAC != "" {
		criteria = parseAcceptanceCriteria(legacyAC)
	}
	var runID *string
	if strings.TrimSpace(issue.ExternalRef) != "" {
		r := strings.TrimSpace(issue.ExternalRef)
		runID = &r
	}
	issueType := issue.IssueType
	if issueType == "" {
		issueType = issue.Type
	}

	return Task{
		ID:        issue.ID,
		Type:      issueType,
		ParentID:  issue.ParentID,
		Title:     issue.Title,
		Goal:      goal,
		Criteria:  criteria,
		Status:    status,
		RunID:     runID,
		Priority:  issue.Priority,
		Assignee:  issue.Assignee,
		CreatedAt: issue.CreatedAt,
		UpdatedAt: issue.UpdatedAt,
	}
}

func formatAcceptanceCriteria(criteria []model.AcceptanceCriterion) string {
	lines := make([]string, 0, len(criteria))
	for i, ac := range criteria {
		text := strings.TrimSpace(ac.Text)
		if text == "" {
			continue
		}
		id := strings.TrimSpace(ac.ID)
		if id == "" {
			id = fmt.Sprintf("AC%d", i+1)
		}
		lines = append(lines, fmt.Sprintf("%s: %s", id, text))
	}
	return strings.Join(lines, "\n")
}

func parseAcceptanceCriteria(raw string) []model.AcceptanceCriterion {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	lines := strings.Split(raw, "\n")
	out := make([]model.AcceptanceCriterion, 0, len(lines))
	fallback := 1
	for _, line := range lines {
		line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		id, text := parseACLine(line)
		if id == "" {
			id = fmt.Sprintf("AC%d", fallback)
			fallback++
			text = line
		}
		out = append(out, model.AcceptanceCriterion{ID: id, Text: text})
	}
	return out
}

func parseACLine(line string) (string, string) {
	colon := strings.Index(line, ":")
	if colon == -1 {
		return "", ""
	}
	id := strings.TrimSpace(line[:colon])
	if !isACID(id) {
		return "", ""
	}
	text := strings.TrimSpace(line[colon+1:])
	return id, text
}

func isACID(value string) bool {
	if len(value) < 3 {
		return false
	}
	if !strings.HasPrefix(value, "AC") {
		return false
	}
	for _, r := range value[2:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func splitLegacyAC(description string) (string, string) {
	const markerBold = "**Acceptance Criteria:**"
	if idx := strings.Index(description, markerBold); idx != -1 {
		goal := strings.TrimSpace(description[:idx])
		ac := strings.TrimSpace(description[idx+len(markerBold):])
		return goal, ac
	}
	const markerPlain = "Acceptance Criteria:"
	if idx := strings.Index(description, markerPlain); idx != -1 {
		goal := strings.TrimSpace(description[:idx])
		ac := strings.TrimSpace(description[idx+len(markerPlain):])
		return goal, ac
	}
	return strings.TrimSpace(description), ""
}
