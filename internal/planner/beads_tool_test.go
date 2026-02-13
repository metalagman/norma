package planner

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/metalagman/norma/internal/task"
)

func TestBeadsTool_ApplyCreatesHierarchy(t *testing.T) {
	t.Parallel()

	writer := &fakeBacklogWriter{}
	tool := NewBeadsTool(writer)
	plan := Decomposition{
		Summary: "Epic summary",
		Epic: EpicPlan{
			Title:       "Epic Title",
			Description: "Epic Description",
		},
		Features: []FeaturePlan{
			{
				Title:       "Feature A",
				Description: "Feature A Description",
				Tasks: []TaskPlan{
					{
						Title:     "Task A1",
						Objective: "Implement A1",
						Artifact:  "cmd/norma/plan.go",
						Verify:    []string{"go test ./..."},
						Notes:     "Keep it small",
					},
				},
			},
		},
	}

	res, err := tool.Apply(context.Background(), plan)
	if err != nil {
		t.Fatalf("Apply returned error: %v", err)
	}
	if res.EpicID == "" {
		t.Fatal("epic id is empty")
	}
	if len(res.Features) != 1 {
		t.Fatalf("features created = %d, want %d", len(res.Features), 1)
	}
	if got := len(res.Features[0].TaskIDs); got != 1 {
		t.Fatalf("tasks created = %d, want %d", got, 1)
	}
	if len(writer.createTaskCalls) != 1 {
		t.Fatalf("create task calls = %d, want %d", len(writer.createTaskCalls), 1)
	}
	taskGoal := writer.createTaskCalls[0].goal
	if !strings.Contains(taskGoal, "Objective: Implement A1") {
		t.Fatalf("task goal missing objective section: %q", taskGoal)
	}
	if !strings.Contains(taskGoal, "Artifact: cmd/norma/plan.go") {
		t.Fatalf("task goal missing artifact section: %q", taskGoal)
	}
	if !strings.Contains(taskGoal, "Verify:\n- go test ./...") {
		t.Fatalf("task goal missing verify section: %q", taskGoal)
	}
}

type fakeBacklogWriter struct {
	nextID          int
	createTaskCalls []createTaskCall
}

type createTaskCall struct {
	parentID string
	title    string
	goal     string
}

func (f *fakeBacklogWriter) AddEpic(_ context.Context, _, _ string) (string, error) {
	return f.newID("epic"), nil
}

func (f *fakeBacklogWriter) AddFeatureDetailed(_ context.Context, _, _, _ string) (string, error) {
	return f.newID("feature"), nil
}

func (f *fakeBacklogWriter) AddTaskDetailed(
	_ context.Context,
	parentID, title, goal string,
	_ []task.AcceptanceCriterion,
	_ *string,
) (string, error) {
	f.createTaskCalls = append(f.createTaskCalls, createTaskCall{
		parentID: parentID,
		title:    title,
		goal:     goal,
	})
	return f.newID("task"), nil
}

func (f *fakeBacklogWriter) newID(prefix string) string {
	f.nextID++
	return fmt.Sprintf("%s-%d", prefix, f.nextID)
}
