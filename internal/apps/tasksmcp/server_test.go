package tasksmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/normahq/norma/internal/task"
)

func TestNewServerRequiresTracker(t *testing.T) {
	_, err := NewServer(nil)
	if err == nil {
		t.Fatal("NewServer(nil) error = nil, want non-nil")
	}
}

func TestTasksServerListsAllTrackerParityTools(t *testing.T) {
	ctx, cleanup, session := newTestSession(t, &mockTracker{})
	defer cleanup()
	initResult := session.InitializeResult()
	if !strings.Contains(initResult.Instructions, "manage Norma issues through the Beads tracker") {
		t.Fatalf("InitializeResult().Instructions = %q, want Beads guidance", initResult.Instructions)
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	got := make([]string, 0, len(tools.Tools))
	for _, tool := range tools.Tools {
		got = append(got, tool.Name)
	}
	slices.Sort(got)

	want := []string{
		"norma.tasks.add",
		"norma.tasks.add_dependency",
		"norma.tasks.add_epic",
		"norma.tasks.add_feature",
		"norma.tasks.add_follow_up",
		"norma.tasks.add_label",
		"norma.tasks.add_related_link",
		"norma.tasks.children",
		"norma.tasks.close_with_reason",
		"norma.tasks.delete",
		"norma.tasks.get",
		"norma.tasks.leaf",
		"norma.tasks.list",
		"norma.tasks.list_blocked_dependents",
		"norma.tasks.list_features",
		"norma.tasks.mark_done",
		"norma.tasks.mark_status",
		"norma.tasks.remove_label",
		"norma.tasks.set_notes",
		"norma.tasks.set_run",
		"norma.tasks.update",
		"norma.tasks.update_workflow_state",
	}
	slices.Sort(want)

	if !slices.Equal(got, want) {
		t.Fatalf("tool names = %v, want %v", got, want)
	}
}

func TestTasksToolDescriptionsAndSchemas(t *testing.T) {
	ctx, cleanup, session := newTestSession(t, &mockTracker{})
	defer cleanup()
	_ = session.InitializeResult()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("ListTools() error = %v", err)
	}

	toolByName := map[string]*mcp.Tool{}
	for _, tool := range tools.Tools {
		toolByName[tool.Name] = tool
	}

	if got := toolByName["norma.tasks.leaf"].Description; !strings.Contains(got, "ready executable leaf issues") {
		t.Fatalf("norma.tasks.leaf description = %q, want ready-leaf wording", got)
	}
	if got := toolByName["norma.tasks.update_workflow_state"].Description; !strings.Contains(got, "planning, doing, checking, or acting") {
		t.Fatalf("norma.tasks.update_workflow_state description = %q, want workflow-state guidance", got)
	}

	outSchema, ok := toolByName["norma.tasks.get"].OutputSchema.(map[string]any)
	if !ok {
		t.Fatalf("norma.tasks.get output schema type = %T, want map[string]any", toolByName["norma.tasks.get"].OutputSchema)
	}
	properties := outSchema["properties"].(map[string]any)
	task := properties["task"].(map[string]any)
	taskProperties := task["properties"].(map[string]any)
	if got := taskProperties["priority"].(map[string]any)["description"]; got != "issue priority where lower numbers are more important" {
		t.Fatalf("norma.tasks.get priority description = %v, want issue priority where lower numbers are more important", got)
	}
}

func TestTasksToolsSuccess(t *testing.T) {
	ctx, cleanup, session := newTestSession(t, &mockTracker{})
	defer cleanup()
	_ = session.InitializeResult()

	tests := []struct {
		name      string
		toolName  string
		args      map[string]any
		assertKey string
	}{
		{name: "add", toolName: "norma.tasks.add", args: map[string]any{"title": "T", "goal": "G"}, assertKey: "task_id"},
		{name: "add epic", toolName: "norma.tasks.add_epic", args: map[string]any{"title": "E", "goal": "G"}, assertKey: "task_id"},
		{name: "add feature", toolName: "norma.tasks.add_feature", args: map[string]any{"epic_id": "norma-epic.1", "title": "F"}, assertKey: "task_id"},
		{name: "add followup", toolName: "norma.tasks.add_follow_up", args: map[string]any{"parent_id": "norma-parent.1", "title": "FU", "goal": "G"}, assertKey: "task_id"},
		{name: "list", toolName: "norma.tasks.list", args: map[string]any{"status": "doing"}, assertKey: "tasks"},
		{name: "list features", toolName: "norma.tasks.list_features", args: map[string]any{"epic_id": "norma-epic.1"}, assertKey: "tasks"},
		{name: "children", toolName: "norma.tasks.children", args: map[string]any{"parent_id": "norma-parent.1"}, assertKey: "tasks"},
		{name: "get", toolName: "norma.tasks.get", args: map[string]any{"id": "norma-task.1"}, assertKey: "task"},
		{name: "mark done", toolName: "norma.tasks.mark_done", args: map[string]any{"id": "norma-task.1"}},
		{name: "mark status", toolName: "norma.tasks.mark_status", args: map[string]any{"id": "norma-task.1", "status": "doing"}},
		{name: "update", toolName: "norma.tasks.update", args: map[string]any{"id": "norma-task.1", "title": "new", "goal": "new goal"}},
		{name: "delete", toolName: "norma.tasks.delete", args: map[string]any{"id": "norma-task.1"}},
		{name: "set run", toolName: "norma.tasks.set_run", args: map[string]any{"id": "norma-task.1", "run_id": "run-1"}},
		{name: "add dependency", toolName: "norma.tasks.add_dependency", args: map[string]any{"task_id": "norma-task.1", "depends_on_id": "norma-task.2"}},
		{name: "leaf", toolName: "norma.tasks.leaf", args: map[string]any{}, assertKey: "tasks"},
		{name: "workflow state", toolName: "norma.tasks.update_workflow_state", args: map[string]any{"id": "norma-task.1", "state": "planning"}},
		{name: "add label", toolName: "norma.tasks.add_label", args: map[string]any{"id": "norma-task.1", "label": "norma-has-plan"}},
		{name: "remove label", toolName: "norma.tasks.remove_label", args: map[string]any{"id": "norma-task.1", "label": "norma-has-plan"}},
		{name: "set notes", toolName: "norma.tasks.set_notes", args: map[string]any{"id": "norma-task.1", "notes": "{}"}},
		{name: "close with reason", toolName: "norma.tasks.close_with_reason", args: map[string]any{"id": "norma-task.1", "reason": "done"}},
		{name: "add related", toolName: "norma.tasks.add_related_link", args: map[string]any{"from_id": "norma-task.1", "to_id": "norma-task.2"}},
		{name: "list blocked", toolName: "norma.tasks.list_blocked_dependents", args: map[string]any{"id": "norma-task.1"}, assertKey: "tasks"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := callTool(t, ctx, session, tc.toolName, tc.args)
			if result.IsError {
				t.Fatalf("result.IsError = true, want false; content=%v", result.Content)
			}
			payload := structuredResultMap(t, result)
			if payload["ok"] != true {
				t.Fatalf("ok = %v, want true", payload["ok"])
			}
			if tc.assertKey != "" {
				if _, exists := payload[tc.assertKey]; !exists {
					t.Fatalf("payload missing key %q: %v", tc.assertKey, payload)
				}
			}
		})
	}
}

func TestTasksToolValidationErrorShape(t *testing.T) {
	ctx, cleanup, session := newTestSession(t, &mockTracker{})
	defer cleanup()
	_ = session.InitializeResult()

	result := callTool(t, ctx, session, "norma.tasks.mark_done", map[string]any{"id": "   "})
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true")
	}
	payload := structuredResultMap(t, result)
	errorObj, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("payload.error type = %T, want map[string]any", payload["error"])
	}
	if errorObj["code"] != codeValidationError {
		t.Fatalf("error.code = %v, want %q", errorObj["code"], codeValidationError)
	}
	if errorObj["operation"] != "norma.tasks.mark_done" {
		t.Fatalf("error.operation = %v, want %q", errorObj["operation"], "norma.tasks.mark_done")
	}
}

func TestTasksToolBackendErrorShape(t *testing.T) {
	tracker := &mockTracker{failByMethod: map[string]error{"MarkDone": errors.New("boom")}}
	ctx, cleanup, session := newTestSession(t, tracker)
	defer cleanup()
	_ = session.InitializeResult()

	result := callTool(t, ctx, session, "norma.tasks.mark_done", map[string]any{"id": "norma-task.1"})
	if !result.IsError {
		t.Fatalf("result.IsError = false, want true")
	}
	payload := structuredResultMap(t, result)
	errorObj, ok := payload["error"].(map[string]any)
	if !ok {
		t.Fatalf("payload.error type = %T, want map[string]any", payload["error"])
	}
	if errorObj["code"] != codeBackendError {
		t.Fatalf("error.code = %v, want %q", errorObj["code"], codeBackendError)
	}
	if errorObj["operation"] != "norma.tasks.mark_done" {
		t.Fatalf("error.operation = %v, want %q", errorObj["operation"], "norma.tasks.mark_done")
	}
}

func TestDirectAndMCPParityCoreWorkflows(t *testing.T) {
	tracker := &mockTracker{}
	ctx, cleanup, session := newTestSession(t, tracker)
	defer cleanup()
	_ = session.InitializeResult()

	directEpicID, err := tracker.AddEpic(ctx, "Epic", "Goal")
	if err != nil {
		t.Fatalf("direct AddEpic error = %v", err)
	}
	mcpEpic := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.add_epic", map[string]any{"title": "Epic", "goal": "Goal"}))
	if mcpEpic["task_id"] != directEpicID {
		t.Fatalf("mcp task_id = %v, want %q", mcpEpic["task_id"], directEpicID)
	}

	directFeatureID, err := tracker.AddFeature(ctx, "norma-epic.1", "Feature")
	if err != nil {
		t.Fatalf("direct AddFeature error = %v", err)
	}
	mcpFeature := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.add_feature", map[string]any{"epic_id": "norma-epic.1", "title": "Feature"}))
	if mcpFeature["task_id"] != directFeatureID {
		t.Fatalf("mcp task_id = %v, want %q", mcpFeature["task_id"], directFeatureID)
	}

	directTaskID, err := tracker.Add(ctx, "Task", "Goal", nil, nil)
	if err != nil {
		t.Fatalf("direct Add error = %v", err)
	}
	mcpTask := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.add", map[string]any{"title": "Task", "goal": "Goal"}))
	if mcpTask["task_id"] != directTaskID {
		t.Fatalf("mcp task_id = %v, want %q", mcpTask["task_id"], directTaskID)
	}

	status := "doing"
	directList, err := tracker.List(ctx, &status)
	if err != nil {
		t.Fatalf("direct List error = %v", err)
	}
	mcpList := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.list", map[string]any{"status": status}))
	mcpTasks, ok := mcpList["tasks"].([]any)
	if !ok {
		t.Fatalf("mcp tasks type = %T, want []any", mcpList["tasks"])
	}
	if len(mcpTasks) != len(directList) {
		t.Fatalf("len(mcp tasks) = %d, want %d", len(mcpTasks), len(directList))
	}

	if err := tracker.AddDependency(ctx, "norma-task.1", "norma-task.2"); err != nil {
		t.Fatalf("direct AddDependency error = %v", err)
	}
	mcpDep := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.add_dependency", map[string]any{"task_id": "norma-task.1", "depends_on_id": "norma-task.2"}))
	if mcpDep["ok"] != true {
		t.Fatalf("mcp add dependency ok = %v, want true", mcpDep["ok"])
	}

	if err := tracker.AddLabel(ctx, "norma-task.1", "norma-has-plan"); err != nil {
		t.Fatalf("direct AddLabel error = %v", err)
	}
	mcpLabel := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.add_label", map[string]any{"id": "norma-task.1", "label": "norma-has-plan"}))
	if mcpLabel["ok"] != true {
		t.Fatalf("mcp add label ok = %v, want true", mcpLabel["ok"])
	}

	if err := tracker.SetNotes(ctx, "norma-task.1", "notes"); err != nil {
		t.Fatalf("direct SetNotes error = %v", err)
	}
	mcpNotes := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.set_notes", map[string]any{"id": "norma-task.1", "notes": "notes"}))
	if mcpNotes["ok"] != true {
		t.Fatalf("mcp set notes ok = %v, want true", mcpNotes["ok"])
	}

	if err := tracker.MarkStatus(ctx, "norma-task.1", "done"); err != nil {
		t.Fatalf("direct MarkStatus done error = %v", err)
	}
	mcpStatusDone := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.mark_status", map[string]any{"id": "norma-task.1", "status": "done"}))
	if mcpStatusDone["ok"] != true {
		t.Fatalf("mcp mark status done ok = %v, want true", mcpStatusDone["ok"])
	}

	if err := tracker.MarkStatus(ctx, "norma-task.1", "todo"); err != nil {
		t.Fatalf("direct MarkStatus todo error = %v", err)
	}
	mcpStatusTodo := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.mark_status", map[string]any{"id": "norma-task.1", "status": "todo"}))
	if mcpStatusTodo["ok"] != true {
		t.Fatalf("mcp mark status todo ok = %v, want true", mcpStatusTodo["ok"])
	}

	directLeaf, err := tracker.LeafTasks(ctx)
	if err != nil {
		t.Fatalf("direct LeafTasks error = %v", err)
	}
	mcpLeaf := structuredResultMap(t, callTool(t, ctx, session, "norma.tasks.leaf", map[string]any{}))
	mcpLeafTasks, ok := mcpLeaf["tasks"].([]any)
	if !ok {
		t.Fatalf("mcp leaf tasks type = %T, want []any", mcpLeaf["tasks"])
	}
	if len(mcpLeafTasks) != len(directLeaf) {
		t.Fatalf("len(mcp leaf tasks) = %d, want %d", len(mcpLeafTasks), len(directLeaf))
	}
}

func newTestSession(t *testing.T, tracker task.Tracker) (context.Context, func(), *mcp.ClientSession) {
	t.Helper()

	server, err := NewServer(tracker)
	if err != nil {
		t.Fatalf("NewServer() error = %v", err)
	}

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		_ = server.Run(ctx, serverTransport)
	}()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		cancel()
		t.Fatalf("client.Connect() error = %v", err)
	}

	cleanup := func() {
		cancel()
		_ = session.Close()
	}
	return ctx, cleanup, session
}

func callTool(t *testing.T, ctx context.Context, session *mcp.ClientSession, toolName string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: toolName, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) error = %v", toolName, err)
	}
	return result
}

func structuredResultMap(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	if result == nil {
		t.Fatal("result is nil")
	}
	switch typed := result.StructuredContent.(type) {
	case map[string]any:
		return typed
	case json.RawMessage:
		var decoded map[string]any
		if err := json.Unmarshal(typed, &decoded); err != nil {
			t.Fatalf("json.Unmarshal(structured content) error = %v", err)
		}
		return decoded
	case nil:
		if len(result.Content) > 0 {
			if textContent, ok := result.Content[0].(*mcp.TextContent); ok {
				var decoded map[string]any
				if err := json.Unmarshal([]byte(textContent.Text), &decoded); err == nil {
					return decoded
				}
			}
		}
		t.Fatalf("result.StructuredContent is nil")
	default:
		t.Fatalf("unexpected structured content type %T", result.StructuredContent)
	}
	return nil
}

type mockTracker struct {
	failByMethod map[string]error
}

func (m *mockTracker) fail(method string) error {
	if m == nil || m.failByMethod == nil {
		return nil
	}
	if err, ok := m.failByMethod[method]; ok {
		return err
	}
	return nil
}

func (m *mockTracker) Add(_ context.Context, _, _ string, _ []task.AcceptanceCriterion, _ *string) (string, error) {
	if err := m.fail("Add"); err != nil {
		return "", err
	}
	return "norma-task.1", nil
}

func (m *mockTracker) AddEpic(_ context.Context, _, _ string) (string, error) {
	if err := m.fail("AddEpic"); err != nil {
		return "", err
	}
	return "norma-epic.1", nil
}

func (m *mockTracker) AddFeature(_ context.Context, _, _ string) (string, error) {
	if err := m.fail("AddFeature"); err != nil {
		return "", err
	}
	return "norma-feature.1", nil
}

func (m *mockTracker) List(_ context.Context, status *string) ([]task.Task, error) {
	if err := m.fail("List"); err != nil {
		return nil, err
	}
	state := "todo"
	if status != nil {
		state = *status
	}
	return []task.Task{sampleTask("norma-task.1", state)}, nil
}

func (m *mockTracker) ListFeatures(_ context.Context, _ string) ([]task.Task, error) {
	if err := m.fail("ListFeatures"); err != nil {
		return nil, err
	}
	return []task.Task{sampleTask("norma-feature.1", "todo")}, nil
}

func (m *mockTracker) Children(_ context.Context, _ string) ([]task.Task, error) {
	if err := m.fail("Children"); err != nil {
		return nil, err
	}
	return []task.Task{sampleTask("norma-child.1", "todo")}, nil
}

func (m *mockTracker) Task(_ context.Context, id string) (task.Task, error) {
	if err := m.fail("Task"); err != nil {
		return task.Task{}, err
	}
	return sampleTask(id, "doing"), nil
}

func (m *mockTracker) MarkDone(_ context.Context, _ string) error {
	return m.fail("MarkDone")
}

func (m *mockTracker) MarkStatus(_ context.Context, _, _ string) error {
	return m.fail("MarkStatus")
}

func (m *mockTracker) Update(_ context.Context, _, _, _ string) error {
	return m.fail("Update")
}

func (m *mockTracker) Delete(_ context.Context, _ string) error {
	return m.fail("Delete")
}

func (m *mockTracker) SetRun(_ context.Context, _, _ string) error {
	return m.fail("SetRun")
}

func (m *mockTracker) AddDependency(_ context.Context, _, _ string) error {
	return m.fail("AddDependency")
}

func (m *mockTracker) LeafTasks(_ context.Context) ([]task.Task, error) {
	if err := m.fail("LeafTasks"); err != nil {
		return nil, err
	}
	return []task.Task{sampleTask("norma-leaf.1", "todo")}, nil
}

func (m *mockTracker) UpdateWorkflowState(_ context.Context, _, _ string) error {
	return m.fail("UpdateWorkflowState")
}

func (m *mockTracker) AddLabel(_ context.Context, _, _ string) error {
	return m.fail("AddLabel")
}

func (m *mockTracker) RemoveLabel(_ context.Context, _, _ string) error {
	return m.fail("RemoveLabel")
}

func (m *mockTracker) SetNotes(_ context.Context, _, _ string) error {
	return m.fail("SetNotes")
}

func (m *mockTracker) CloseWithReason(_ context.Context, _, _ string) error {
	return m.fail("CloseWithReason")
}

func (m *mockTracker) AddRelatedLink(_ context.Context, _, _ string) error {
	return m.fail("AddRelatedLink")
}

func (m *mockTracker) ListBlockedDependents(_ context.Context, _ string) ([]task.Task, error) {
	if err := m.fail("ListBlockedDependents"); err != nil {
		return nil, err
	}
	return []task.Task{sampleTask("norma-blocked.1", "todo")}, nil
}

func (m *mockTracker) AddFollowUp(_ context.Context, _, _, _ string, _ []task.AcceptanceCriterion) (string, error) {
	if err := m.fail("AddFollowUp"); err != nil {
		return "", err
	}
	return "norma-followup.1", nil
}

func sampleTask(id, status string) task.Task {
	runID := "run-1"
	return task.Task{
		ID:       id,
		Type:     "task",
		ParentID: "norma-parent.1",
		Title:    "sample " + id,
		Goal:     "sample goal",
		Criteria: []task.AcceptanceCriterion{{ID: "AC1", Text: "must work"}},
		Status:   status,
		RunID:    &runID,
		Priority: 1,
		Assignee: "agent",
		Labels:   []string{"norma"},
		Notes:    "{}",
	}
}

func (m *mockTracker) String() string {
	return fmt.Sprintf("mockTracker(%v)", m.failByMethod)
}

func TestRunHTTPRequiresTracker(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunHTTP(ctx, nil, "localhost:0")
	if err == nil {
		t.Fatal("RunHTTP(nil tracker) error = nil, want non-nil")
	}
}

func TestRunHTTPRequiresAddr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunHTTP(ctx, &mockTracker{}, "")
	if err == nil {
		t.Fatal("RunHTTP(empty addr) error = nil, want non-nil")
	}
}

func TestRunHTTPStartsAndAcceptsConnections(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- RunHTTP(ctx, &mockTracker{}, "localhost:0")
	}()

	// Give the server time to start.
	select {
	case <-time.After(100 * time.Millisecond):
	case err := <-errCh:
		t.Fatalf("RunHTTP returned early with error: %v", err)
	}

	cancel()

	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("RunHTTP error = %v, want nil or context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("RunHTTP did not stop after context cancel")
	}
}

func TestStartHTTPServerRequiresTracker(t *testing.T) {
	_, err := StartHTTPServer(context.Background(), nil, "localhost:0")
	if err == nil {
		t.Fatal("StartHTTPServer(nil tracker) error = nil, want non-nil")
	}
}

func TestStartHTTPServerRequiresAddr(t *testing.T) {
	_, err := StartHTTPServer(context.Background(), &mockTracker{}, "")
	if err == nil {
		t.Fatal("StartHTTPServer(empty addr) error = nil, want non-nil")
	}
}

func TestStartHTTPServerStartsAndReturnsAddr(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	result, err := StartHTTPServer(ctx, &mockTracker{}, "127.0.0.1:0")
	if err != nil {
		t.Fatalf("StartHTTPServer() error = %v", err)
	}
	defer func() {
		if err := result.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	if result.Addr == "" {
		t.Fatal("StartHTTPServer() Addr is empty")
	}
	if !strings.Contains(result.Addr, "127.0.0.1:") {
		t.Fatalf("StartHTTPServer() Addr = %q, want contains 127.0.0.1:", result.Addr)
	}
}
