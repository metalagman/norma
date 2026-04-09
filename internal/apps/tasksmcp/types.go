package tasksmcp

type noInput struct{}

type taskCriterionInput struct {
	ID          string   `json:"id,omitempty" jsonschema:"acceptance criterion id"`
	Text        string   `json:"text" jsonschema:"acceptance criterion text"`
	VerifyHints []string `json:"verify_hints,omitempty" jsonschema:"verification hints"`
}

type taskCriterion struct {
	ID          string   `json:"id" jsonschema:"acceptance criterion id"`
	Text        string   `json:"text" jsonschema:"acceptance criterion text"`
	VerifyHints []string `json:"verify_hints,omitempty" jsonschema:"verification hints"`
}

type taskRecord struct {
	ID        string          `json:"id" jsonschema:"Beads issue ID"`
	Type      string          `json:"type,omitempty" jsonschema:"issue type such as epic, feature, task, bug, or chore"`
	ParentID  string          `json:"parent_id,omitempty" jsonschema:"parent issue ID when this issue is nested under another issue"`
	Title     string          `json:"title" jsonschema:"issue title"`
	Goal      string          `json:"goal,omitempty" jsonschema:"issue goal or objective"`
	Criteria  []taskCriterion `json:"criteria,omitempty" jsonschema:"acceptance criteria attached to the issue"`
	Status    string          `json:"status,omitempty" jsonschema:"issue workflow status"`
	RunID     *string         `json:"run_id,omitempty" jsonschema:"external run ID associated with the issue"`
	Priority  int             `json:"priority,omitempty" jsonschema:"issue priority where lower numbers are more important"`
	Assignee  string          `json:"assignee,omitempty" jsonschema:"assigned owner"`
	Labels    []string        `json:"labels,omitempty" jsonschema:"issue labels"`
	Notes     string          `json:"notes,omitempty" jsonschema:"issue notes"`
	CreatedAt string          `json:"created_at,omitempty" jsonschema:"creation timestamp"`
	UpdatedAt string          `json:"updated_at,omitempty" jsonschema:"last update timestamp"`
}

type ToolError struct {
	Operation string `json:"operation" jsonschema:"tool name that produced the error"`
	Code      string `json:"code" jsonschema:"stable machine-readable error code"`
	Message   string `json:"message" jsonschema:"human-readable error message"`
}

type ToolOutcome struct {
	OK    bool       `json:"ok" jsonschema:"true when the tool completed successfully"`
	Error *ToolError `json:"error,omitempty" jsonschema:"error details when ok is false"`
}

type basicOutput struct {
	ToolOutcome
}

type addTaskInput struct {
	Title    string               `json:"title" jsonschema:"task title"`
	Goal     string               `json:"goal,omitempty" jsonschema:"task goal"`
	Criteria []taskCriterionInput `json:"criteria,omitempty" jsonschema:"acceptance criteria"`
	RunID    *string              `json:"run_id,omitempty" jsonschema:"external run id"`
}

type addTaskOutput struct {
	ToolOutcome
	TaskID string `json:"task_id,omitempty" jsonschema:"created Beads issue ID"`
}

type addEpicInput struct {
	Title string `json:"title" jsonschema:"epic title"`
	Goal  string `json:"goal,omitempty" jsonschema:"epic goal"`
}

type addEpicOutput struct {
	ToolOutcome
	TaskID string `json:"task_id,omitempty" jsonschema:"created epic issue ID"`
}

type addFeatureInput struct {
	EpicID string `json:"epic_id" jsonschema:"epic id"`
	Title  string `json:"title" jsonschema:"feature title"`
}

type addFeatureOutput struct {
	ToolOutcome
	TaskID string `json:"task_id,omitempty" jsonschema:"created feature issue ID"`
}

type addFollowUpInput struct {
	ParentID string               `json:"parent_id" jsonschema:"parent task id"`
	Title    string               `json:"title" jsonschema:"follow-up title"`
	Goal     string               `json:"goal,omitempty" jsonschema:"follow-up goal"`
	Criteria []taskCriterionInput `json:"criteria,omitempty" jsonschema:"acceptance criteria"`
}

type addFollowUpOutput struct {
	ToolOutcome
	TaskID string `json:"task_id,omitempty" jsonschema:"created follow-up issue ID"`
}

type listTasksInput struct {
	Status *string `json:"status,omitempty" jsonschema:"optional status filter"`
}

type listTasksOutput struct {
	ToolOutcome
	Tasks []taskRecord `json:"tasks,omitempty" jsonschema:"matching Beads issues"`
}

type listFeaturesInput struct {
	EpicID string `json:"epic_id" jsonschema:"epic id"`
}

type listFeaturesOutput struct {
	ToolOutcome
	Tasks []taskRecord `json:"tasks,omitempty" jsonschema:"features under the requested epic"`
}

type childrenInput struct {
	ParentID string `json:"parent_id" jsonschema:"parent task id"`
}

type childrenOutput struct {
	ToolOutcome
	Tasks []taskRecord `json:"tasks,omitempty" jsonschema:"child issues under the requested parent"`
}

type getTaskInput struct {
	ID string `json:"id" jsonschema:"task id"`
}

type getTaskOutput struct {
	ToolOutcome
	Task taskRecord `json:"task,omitempty" jsonschema:"requested Beads issue"`
}

type idInput struct {
	ID string `json:"id" jsonschema:"task id"`
}

type markStatusInput struct {
	ID     string `json:"id" jsonschema:"task id"`
	Status string `json:"status" jsonschema:"task status"`
}

type updateTaskInput struct {
	ID    string `json:"id" jsonschema:"task id"`
	Title string `json:"title" jsonschema:"new title"`
	Goal  string `json:"goal,omitempty" jsonschema:"new goal"`
}

type setRunInput struct {
	ID    string `json:"id" jsonschema:"task id"`
	RunID string `json:"run_id" jsonschema:"external run id"`
}

type addDependencyInput struct {
	TaskID      string `json:"task_id" jsonschema:"dependent task id"`
	DependsOnID string `json:"depends_on_id" jsonschema:"dependency task id"`
}

type workflowStateInput struct {
	ID    string `json:"id" jsonschema:"task id"`
	State string `json:"state" jsonschema:"workflow state"`
}

type labelInput struct {
	ID    string `json:"id" jsonschema:"task id"`
	Label string `json:"label" jsonschema:"label value"`
}

type setNotesInput struct {
	ID    string `json:"id" jsonschema:"task id"`
	Notes string `json:"notes" jsonschema:"task notes"`
}

type closeWithReasonInput struct {
	ID     string `json:"id" jsonschema:"task id"`
	Reason string `json:"reason" jsonschema:"close reason"`
}

type addRelatedLinkInput struct {
	FromID string `json:"from_id" jsonschema:"source task id"`
	ToID   string `json:"to_id" jsonschema:"target task id"`
}
