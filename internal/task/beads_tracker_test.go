//go:build integration

package task

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestBeadsTrackerImplementsTracker(t *testing.T) {
	_ = Tracker(&BeadsTracker{})
}

func TestCloseWithReason(t *testing.T) {
	tracker := newIntegrationBeadsTracker(t)
	ctx := context.Background()

	issueID, err := tracker.Add(ctx, "Test CloseWithReason", "Testing close with reason", nil, nil)
	if err != nil {
		t.Fatalf("create issue: %v", err)
	}
	cleanupIssue(t, tracker, issueID)

	err = tracker.CloseWithReason(ctx, issueID, "Test completed successfully")
	if err != nil {
		t.Errorf("CloseWithReason failed: %v", err)
	}
}

func TestAddRelatedLink(t *testing.T) {
	tracker := newIntegrationBeadsTracker(t)
	ctx := context.Background()

	issue1, err := tracker.Add(ctx, "Test Issue 1", "First test issue", nil, nil)
	if err != nil {
		t.Fatalf("create first issue: %v", err)
	}
	cleanupIssue(t, tracker, issue1)

	issue2, err := tracker.Add(ctx, "Test Issue 2", "Second test issue", nil, nil)
	if err != nil {
		t.Fatalf("create second issue: %v", err)
	}
	cleanupIssue(t, tracker, issue2)

	err = tracker.AddRelatedLink(ctx, issue1, issue2)
	if err != nil {
		t.Errorf("AddRelatedLink failed: %v", err)
	}
}

func TestListBlockedDependents(t *testing.T) {
	tracker := newIntegrationBeadsTracker(t)
	ctx := context.Background()

	blocker, err := tracker.Add(ctx, "Test Blocker", "This issue blocks another", nil, nil)
	if err != nil {
		t.Fatalf("create blocker issue: %v", err)
	}
	cleanupIssue(t, tracker, blocker)

	blocked, err := tracker.Add(ctx, "Test Blocked", "This issue depends on blocker", nil, nil)
	if err != nil {
		t.Fatalf("create blocked issue: %v", err)
	}
	cleanupIssue(t, tracker, blocked)

	err = tracker.AddDependency(ctx, blocked, blocker)
	if err != nil {
		t.Fatalf("add dependency: %v", err)
	}

	dependents, err := tracker.ListBlockedDependents(ctx, blocker)
	if err != nil {
		t.Errorf("ListBlockedDependents failed: %v", err)
	}

	found := false
	for _, dep := range dependents {
		if dep.ID == blocked {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected to find blocked issue in dependents, got: %v", dependents)
	}
}

func TestAddFollowUp(t *testing.T) {
	tracker := newIntegrationBeadsTracker(t)
	ctx := context.Background()

	parent, err := tracker.Add(ctx, "Test Parent", "Parent issue for follow-up", nil, nil)
	if err != nil {
		t.Fatalf("create parent issue: %v", err)
	}
	cleanupIssue(t, tracker, parent)

	followUpID, err := tracker.AddFollowUp(ctx, parent, "Test FollowUp", "Follow-up task created", nil)
	if err != nil {
		t.Errorf("AddFollowUp failed: %v", err)
	}
	cleanupIssue(t, tracker, followUpID)

	if followUpID == "" {
		t.Error("AddFollowUp returned empty ID")
	}
}

func newIntegrationBeadsTracker(t *testing.T) *BeadsTracker {
	t.Helper()

	if _, err := exec.LookPath("bd"); err != nil {
		t.Skipf("bd not available: %v", err)
	}

	workingDir := t.TempDir()
	runTestCmd(t, workingDir, "git", "init")
	runTestCmd(t, workingDir, "bd", "init", "--prefix", "norma")

	return &BeadsTracker{
		BinPath:    "bd",
		WorkingDir: workingDir,
	}
}

func cleanupIssue(t *testing.T, tracker *BeadsTracker, issueID string) {
	t.Helper()
	t.Cleanup(func() {
		if issueID == "" {
			return
		}
		if err := tracker.Delete(context.Background(), issueID); err != nil {
			t.Errorf("cleanup issue %s: %v", issueID, err)
		}
	})
}

func runTestCmd(t *testing.T, dir, name string, args ...string) {
	t.Helper()

	cmd := exec.Command(name, args...)
	cmd.Dir = filepath.Clean(dir)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("command %s %v failed: %v\n%s", name, args, err, fmt.Sprintf("%s", out))
	}
}
