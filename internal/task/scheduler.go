package task

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"
)

// SelectionPolicy defines how the orchestrator chooses the next issue.
type SelectionPolicy struct {
	ActiveFeatureID string
	ActiveEpicID    string
}

// SelectNextReady chooses the next issue from a ready list and returns a selection reason.
func SelectNextReady(ctx context.Context, tracker Tracker, ready []Task, policy SelectionPolicy) (Task, string, error) {
	if len(ready) == 0 {
		return Task{}, "no_ready_issues", fmt.Errorf("no ready issues")
	}

	scopeID := strings.TrimSpace(policy.ActiveFeatureID)
	scopeLabel := "none"
	if scopeID == "" {
		scopeID = strings.TrimSpace(policy.ActiveEpicID)
		if scopeID != "" {
			scopeLabel = "active_epic"
		}
	} else {
		scopeLabel = "active_feature"
	}

	candidates := ready
	if scopeID != "" {
		filtered := make([]Task, 0, len(ready))
		for _, item := range ready {
			under, err := isUnderParent(ctx, tracker, item, scopeID)
			if err != nil {
				return Task{}, "", err
			}
			if under {
				filtered = append(filtered, item)
			}
		}
		if len(filtered) > 0 {
			candidates = filtered
		} else {
			scopeLabel = "scope_fallback"
		}
	}

	leafCandidates, err := filterLeaves(ctx, tracker, candidates)
	if err != nil {
		return Task{}, "", err
	}
	leafUsed := true
	if len(leafCandidates) == 0 {
		leafCandidates = candidates
		leafUsed = false
	}

	readyCandidates := make([]Task, 0, len(leafCandidates))
	for _, item := range leafCandidates {
		if hasReadyContract(item.Goal) {
			readyCandidates = append(readyCandidates, item)
		}
	}
	readyUsed := true
	if len(readyCandidates) == 0 {
		readyCandidates = leafCandidates
		readyUsed = false
	}

	sort.Slice(readyCandidates, func(i, j int) bool {
		left := readyCandidates[i]
		right := readyCandidates[j]
		if left.Priority != right.Priority {
			return left.Priority < right.Priority
		}
		leftVerify := hasVerifyField(left.Goal)
		rightVerify := hasVerifyField(right.Goal)
		if leftVerify != rightVerify {
			return leftVerify
		}
		leftTime, leftOK := parseTime(left.CreatedAt)
		rightTime, rightOK := parseTime(right.CreatedAt)
		if leftOK && rightOK && !leftTime.Equal(rightTime) {
			return leftTime.Before(rightTime)
		}
		if left.CreatedAt != right.CreatedAt {
			return left.CreatedAt < right.CreatedAt
		}
		return left.ID < right.ID
	})

	selected := readyCandidates[0]
	reason := fmt.Sprintf("scope=%s leaf=%t ready_contract=%t priority=%d verify=%t created_at=%s",
		scopeLabel,
		leafUsed,
		readyUsed,
		selected.Priority,
		hasVerifyField(selected.Goal),
		selected.CreatedAt,
	)
	return selected, reason, nil
}

func filterLeaves(ctx context.Context, tracker Tracker, items []Task) ([]Task, error) {
	leaves := make([]Task, 0, len(items))
	for _, item := range items {
		children, err := tracker.Children(ctx, item.ID)
		if err != nil {
			return nil, err
		}
		if len(children) == 0 {
			leaves = append(leaves, item)
		}
	}
	return leaves, nil
}

func isUnderParent(ctx context.Context, tracker Tracker, item Task, parentID string) (bool, error) {
	parentID = strings.TrimSpace(parentID)
	if parentID == "" {
		return true, nil
	}
	current := strings.TrimSpace(item.ParentID)
	for current != "" {
		parent, err := tracker.Task(ctx, current)
		if err != nil {
			return false, err
		}
		current = strings.TrimSpace(parent.ParentID)
	}
	return false, nil
}

func hasReadyContract(text string) bool {
	return hasField(text, "objective") && hasField(text, "artifact") && hasField(text, "verify")
}

func hasVerifyField(text string) bool {
	return hasField(text, "verify")
}

func hasField(text, field string) bool {
	field = strings.TrimSpace(strings.ToLower(field))
	if field == "" {
		return false
	}
	return strings.Contains(strings.ToLower(text), field+":")
}

func parseTime(value string) (time.Time, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}
