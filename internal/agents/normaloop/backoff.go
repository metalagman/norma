package normaloop

import (
	"strings"
	"time"

	"github.com/normahq/norma/v2/internal/task"

	"google.golang.org/adk/v2/session"
)

func retryEligibleTasks(state session.State, items []task.Task, now time.Time) []task.Task {
	if len(items) == 0 {
		return items
	}

	out := make([]task.Task, 0, len(items))
	for _, item := range items {
		until, ok := taskRetryUntil(state, item.ID)
		if !ok || !now.Before(until) {
			out = append(out, item)
		}
	}
	return out
}

func scheduleTaskRetry(state session.State, taskID string, steps []time.Duration, now time.Time) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" || len(steps) == 0 {
		return
	}

	step := taskRetryStep(state, taskID)
	if step >= len(steps) {
		step = len(steps) - 1
	}
	until := now.Add(steps[step]).UTC().Format(time.RFC3339Nano)
	setStringMapValue(state, failedTaskBackoffUntilKey, taskID, until)

	if step < len(steps)-1 {
		step++
	}
	setIntMapValue(state, failedTaskBackoffStepKey, taskID, step)
}

func clearTaskRetry(state session.State, taskID string) {
	taskID = strings.TrimSpace(taskID)
	if taskID == "" {
		return
	}
	clearStringMapValue(state, failedTaskBackoffUntilKey, taskID)
	clearIntMapValue(state, failedTaskBackoffStepKey, taskID)
}

func taskRetryUntil(state session.State, taskID string) (time.Time, bool) {
	value, ok := stringMapValue(state, failedTaskBackoffUntilKey, taskID)
	if !ok {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, false
	}
	return t, true
}

func taskRetryStep(state session.State, taskID string) int {
	value, ok := intMapValue(state, failedTaskBackoffStepKey, taskID)
	if !ok || value < 0 {
		return 0
	}
	return value
}

func stringMapValue(state session.State, stateKey, itemKey string) (string, bool) {
	raw, err := state.Get(stateKey)
	if err != nil {
		return "", false
	}
	values, ok := raw.(map[string]string)
	if ok {
		value, ok := values[itemKey]
		return value, ok
	}
	anyValues, ok := raw.(map[string]any)
	if !ok {
		return "", false
	}
	value, ok := anyValues[itemKey].(string)
	return value, ok
}

func intMapValue(state session.State, stateKey, itemKey string) (int, bool) {
	raw, err := state.Get(stateKey)
	if err != nil {
		return 0, false
	}
	values, ok := raw.(map[string]int)
	if ok {
		value, ok := values[itemKey]
		return value, ok
	}
	anyValues, ok := raw.(map[string]any)
	if !ok {
		return 0, false
	}
	switch value := anyValues[itemKey].(type) {
	case int:
		return value, true
	case int64:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}

func setStringMapValue(state session.State, stateKey, itemKey, value string) {
	values := map[string]string{}
	if raw, err := state.Get(stateKey); err == nil {
		if existing, ok := raw.(map[string]string); ok {
			for k, v := range existing {
				values[k] = v
			}
		}
		if existing, ok := raw.(map[string]any); ok {
			for k, v := range existing {
				if str, ok := v.(string); ok {
					values[k] = str
				}
			}
		}
	}
	values[itemKey] = value
	_ = state.Set(stateKey, values)
}

func setIntMapValue(state session.State, stateKey, itemKey string, value int) {
	values := map[string]int{}
	if raw, err := state.Get(stateKey); err == nil {
		if existing, ok := raw.(map[string]int); ok {
			for k, v := range existing {
				values[k] = v
			}
		}
		if existing, ok := raw.(map[string]any); ok {
			for k, v := range existing {
				switch typed := v.(type) {
				case int:
					values[k] = typed
				case int64:
					values[k] = int(typed)
				case float64:
					values[k] = int(typed)
				}
			}
		}
	}
	values[itemKey] = value
	_ = state.Set(stateKey, values)
}

func clearStringMapValue(state session.State, stateKey, itemKey string) {
	raw, err := state.Get(stateKey)
	if err != nil {
		return
	}

	values := map[string]string{}
	switch existing := raw.(type) {
	case map[string]string:
		for k, v := range existing {
			if k != itemKey {
				values[k] = v
			}
		}
	case map[string]any:
		for k, v := range existing {
			if k != itemKey {
				if str, ok := v.(string); ok {
					values[k] = str
				}
			}
		}
	}
	_ = state.Set(stateKey, values)
}

func clearIntMapValue(state session.State, stateKey, itemKey string) {
	raw, err := state.Get(stateKey)
	if err != nil {
		return
	}

	values := map[string]int{}
	switch existing := raw.(type) {
	case map[string]int:
		for k, v := range existing {
			if k != itemKey {
				values[k] = v
			}
		}
	case map[string]any:
		for k, v := range existing {
			if k == itemKey {
				continue
			}
			switch typed := v.(type) {
			case int:
				values[k] = typed
			case int64:
				values[k] = int(typed)
			case float64:
				values[k] = int(typed)
			}
		}
	}
	_ = state.Set(stateKey, values)
}
