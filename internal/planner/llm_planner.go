package planner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/metalagman/norma/internal/adkrunner"
	"github.com/metalagman/norma/internal/planner/llmtools"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/genai"
)

// LLMPlanner implements interactive planning using ADK llmagent.
type LLMPlanner struct {
	repoRoot string
	model    model.LLM
}

// ErrHandledInTUI indicates planner failure was already presented to the user in TUI.
var ErrHandledInTUI = errors.New("planner failure handled in tui")

// NewLLMPlanner constructs a new LLM planner.
func NewLLMPlanner(repoRoot string, m model.LLM) (*LLMPlanner, error) {
	return &LLMPlanner{
		repoRoot: repoRoot,
		model:    m,
	}, nil
}

// Generate runs an interactive planning session.
func (p *LLMPlanner) Generate(ctx context.Context, req Request) (Decomposition, string, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	planRunDir, err := p.newPlanRunDir()
	if err != nil {
		return Decomposition{}, "", err
	}
	eventChan := make(chan *session.Event, 100)
	questionChan := make(chan string)
	responseChan := make(chan string)

	humanTool, err := llmtools.NewHumanTool(func(question string) (string, error) {
		select {
		case questionChan <- question:
		case <-runCtx.Done():
			return "", runCtx.Err()
		}

		select {
		case answer := <-responseChan:
			return answer, nil
		case <-runCtx.Done():
			return "", runCtx.Err()
		}
	})
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create human tool: %w", err)
	}

	beadsTool, err := llmtools.NewBeadsCommandTool(p.repoRoot)
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create beads tool: %w", err)
	}

	shellTool, err := llmtools.NewShellCommandTool(p.repoRoot)
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create shell tool: %w", err)
	}

	// Create the llmagent
	plannerAgent, err := llmagent.New(llmagent.Config{
		Name:        "NormaPlanner",
		Description: "Interactive Norma planning agent that decomposes epics into features and tasks.",
		Model:       p.model,
		Tools:       []tool.Tool{humanTool, shellTool, beadsTool},
		Instruction: buildLLMPlanPrompt(),
	})
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create llmagent: %w", err)
	}

	// Start TUI in a goroutine
	tuiModel, err := newPlannerModel(eventChan, questionChan, responseChan, cancel)
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create TUI model: %w", err)
	}
	prog := tea.NewProgram(tuiModel, tea.WithAltScreen())

	tuiErrChan := make(chan error, 1)
	go func() {
		if _, err := prog.Run(); err != nil {
			tuiErrChan <- err
		}
		close(tuiErrChan)
	}()
	var waitTUIOnce sync.Once
	var waitTUIErr error
	waitTUI := func() error {
		waitTUIOnce.Do(func() {
			if err, ok := <-tuiErrChan; ok {
				waitTUIErr = err
			}
		})
		return waitTUIErr
	}
	quitAndWaitTUI := func() error {
		prog.Quit()
		return waitTUI()
	}
	var closeEventOnce sync.Once
	closeEvent := func() {
		closeEventOnce.Do(func() { close(eventChan) })
	}

	// Run the agent using adkrunner
	initialState := map[string]any{
		"epic_description": req.EpicDescription,
		"decomposition":    Decomposition{},
	}

	initialContent := "Let's start planning."
	if req.EpicDescription != "" {
		initialContent = fmt.Sprintf("Let's start planning for the following project goal: %s", req.EpicDescription)
	}
	var (
		eventDecompositions []Decomposition
		turnText            strings.Builder
		lastCompleteTurn    string
	)

	finalSession, lastContent, err := adkrunner.Run(runCtx, adkrunner.RunInput{
		AppName:        "norma-plan",
		UserID:         "norma-user",
		SessionID:      "plan-" + time.Now().Format("150405"),
		Agent:          plannerAgent,
		InitialState:   initialState,
		InitialContent: genai.NewContentFromText(initialContent, genai.RoleUser),
		OnEvent: func(ev *session.Event) {
			if ev != nil && ev.Content != nil {
				if dec, parseErr := extractDecompositionFromContent(ev.Content); parseErr == nil {
					eventDecompositions = append(eventDecompositions, dec)
				}
				if chunk := extractTextFromContent(ev.Content); chunk != "" {
					turnText.WriteString(chunk)
				}
				if ev.TurnComplete || !ev.Partial {
					if fullTurn := strings.TrimSpace(turnText.String()); fullTurn != "" {
						lastCompleteTurn = fullTurn
						if dec, parseErr := parseJSONFromText(fullTurn); parseErr == nil {
							eventDecompositions = append(eventDecompositions, dec)
						}
					}
					turnText.Reset()
				}
			}
			select {
			case eventChan <- ev:
			case <-runCtx.Done():
			}
		},
	})

	// Signal end of session to TUI
	closeEvent()

	if err != nil {
		if errors.Is(err, context.Canceled) {
			_ = quitAndWaitTUI()
			return Decomposition{}, "", context.Canceled
		}
		closeEvent()
		prog.Send(planFailedMsg(formatPlannerRunError(err)))
		if tuiErr := waitTUI(); tuiErr != nil {
			return Decomposition{}, "", fmt.Errorf("TUI error: %w", tuiErr)
		}
		return Decomposition{}, "", ErrHandledInTUI
	}

	// Extract decomposition from session state.
	var dec Decomposition
	decVal, stateErr := finalSession.State().Get("decomposition")
	if stateErr == nil {
		decBytes, err := json.Marshal(decVal)
		if err != nil {
			_ = quitAndWaitTUI()
			return Decomposition{}, "", fmt.Errorf("marshal decomposition from state: %w", err)
		}
		if err := json.Unmarshal(decBytes, &dec); err != nil {
			_ = quitAndWaitTUI()
			return Decomposition{}, "", fmt.Errorf("unmarshal decomposition: %w", err)
		}
		// Default seeded state is an empty placeholder; recover from model output instead.
		if strings.TrimSpace(dec.Epic.Title) == "" {
			stateErr = fmt.Errorf("decomposition in session state is empty")
		}
	}
	if stateErr != nil && len(eventDecompositions) > 0 {
		// Fallback 1: decode from beads tool call observed in event stream.
		dec = eventDecompositions[len(eventDecompositions)-1]
	} else if stateErr != nil {
		// Fallback 2: parse from complete streamed model text when available.
		streamRemainder := strings.TrimSpace(turnText.String())
		lastContentText := strings.TrimSpace(extractTextFromContent(lastContent))
		contentDec, parseErr := parseDecompositionFromCandidates(
			lastCompleteTurn,
			streamRemainder,
			lastContentText,
		)
		switch {
		case parseErr == nil:
			dec = contentDec
		case strings.TrimSpace(lastCompleteTurn) == "" && streamRemainder == "" && lastContentText == "":
			_ = quitAndWaitTUI()
			return Decomposition{}, "", fmt.Errorf("decomposition not found in session state and no model response received: %w", stateErr)
		default:
			_ = quitAndWaitTUI()
			return Decomposition{}, "", fmt.Errorf("decomposition not found in session state and could not parse from last model response (%v): %w", parseErr, stateErr)
		}
	}

	if err := dec.Validate(); err != nil {
		_ = quitAndWaitTUI()
		return Decomposition{}, "", fmt.Errorf("invalid decomposition: %w", err)
	}

	// Send decomposition to TUI
	prog.Send(planFinishedMsg(dec))

	// Wait for TUI to finish
	if tuiErr := waitTUI(); tuiErr != nil {
		return Decomposition{}, "", fmt.Errorf("TUI error: %w", tuiErr)
	}

	// Save output.json
	outJSON, _ := json.MarshalIndent(dec, "", "  ")
	_ = os.WriteFile(filepath.Join(planRunDir, "output.json"), outJSON, 0o600)

	return dec, planRunDir, nil
}

func (p *LLMPlanner) newPlanRunDir() (string, error) {
	sfx, err := randomHex(3)
	if err != nil {
		return "", fmt.Errorf("generate planning run id: %w", err)
	}
	runID := fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102-150405"), sfx)
	runDir := filepath.Join(p.repoRoot, ".norma", "plans", runID)
	if err := os.MkdirAll(filepath.Join(runDir, "logs"), 0o700); err != nil {
		return "", fmt.Errorf("create planning logs dir: %w", err)
	}
	return runDir, nil
}

func buildLLMPlanPrompt() string {
	return `You are Norma's planning agent.
Your job is to decompose a project goal (epic) into a Beads-ready hierarchy:
1) one epic
2) multiple features under that epic
3) multiple executable tasks under each feature

Workflow:
1. If the project goal (epic) is provided in the first message, proceed to decomposition.
2. If the goal is missing, empty, or too vague, you MUST use the 'human' tool to ask the user what they want to build.
3. Use the 'beads' tool to inspect the issue tracker (list, show, ready) and understand existing epics, features, and tasks.
4. Use 'run_shell_command' to inspect the current project state (files, structure, code) to make informed planning decisions.
5. Decompose the goal into features and tasks.
6. MANDATORY CONFIRMATION: Once you have a final plan, you MUST present it to the user using the 'human' tool and ask for approval.
7. REFINEMENT: If the user rejects the plan or provides feedback, you MUST refine the plan and ask for approval again.
8. PERSISTENCE: After the user explicitly approves the plan, use the 'beads' tool ('create' operation) to persist the epic, features, and tasks.
   - Use 'beads create --type epic --title ... --description ... --json' to create the epic and get its ID.
   - Use 'beads create --type feature --parent <epic_id> ...' for features.
   - Use 'beads create --type task --parent <feature_id> ...' for tasks.
9. FINISH: Once all artifacts are created in Beads, output the final decomposition as a single JSON code block and finish the session.

CRITICAL RULES:
- NEVER ask the user a question using plain text.
- ALWAYS use the 'human' tool for ANY interaction with the user (including plan approval).
- ALWAYS use the 'beads' tool for ANY interaction with the issue tracker.
- Use 'run_shell_command' to understand the codebase before planning.
- If you just output text without calling a tool, the session will terminate and the plan will be lost.

Tool: run_shell_command
- Allowed commands: ls, grep, cat, find, tree, git, go, echo.
- NO pipes (|), redirects (>, >>), or command chaining (&&, ||, ;, &) allowed.
- Use this to explore the project structure and existing code.

Tool: beads
- Operations: list, show, create, update, close, reopen, delete, ready.
- Use this tool for ALL issue tracker operations.
- Enforce --reason for close, reopen, and delete operations.
- Always prefer this tool over running 'bd' via 'run_shell_command'.

Planning Rules:
- Every task must be executable and include:
  - objective (what it accomplishes)
  - artifact (concrete files/paths/PR surface)
  - verify (concrete commands/checks to prove it works)
- Keep scope pragmatic. Prefer 2-6 features and 1-6 tasks per feature.
- Keep titles concise and action-oriented.
`
}

// PlannerInstruction returns the canonical planner prompt used by Norma planner agents.
func PlannerInstruction() string {
	return buildLLMPlanPrompt()
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func parseJSONFromText(text string) (Decomposition, error) {
	// Try to find markdown code block
	if start := strings.Index(text, "```json"); start != -1 {
		content := text[start+7:]
		if end := strings.Index(content, "```"); end != -1 {
			text = content[:end]
		}
	} else if start := strings.Index(text, "{"); start != -1 {
		// Fallback to first { and last }
		if end := strings.LastIndex(text, "}"); end != -1 && end > start {
			text = text[start : end+1]
		}
	}

	var dec Decomposition
	if err := json.Unmarshal([]byte(text), &dec); err != nil {
		return Decomposition{}, err
	}
	return dec, nil
}

func parseDecompositionFromCandidates(candidates ...string) (Decomposition, error) {
	var lastErr error
	seen := make(map[string]struct{}, len(candidates))

	for _, candidate := range candidates {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}

		dec, err := parseJSONFromText(candidate)
		if err == nil {
			return dec, nil
		}
		lastErr = err
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no candidate response content")
	}
	return Decomposition{}, lastErr
}

func extractTextFromContent(content *genai.Content) string {
	if content == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range content.Parts {
		if part.Text != "" {
			sb.WriteString(part.Text)
		}
	}
	return sb.String()
}

func extractDecompositionFromContent(content *genai.Content) (Decomposition, error) {
	if content == nil {
		return Decomposition{}, fmt.Errorf("content is nil")
	}

	var lastErr error
	for _, part := range content.Parts {
		if part.FunctionCall != nil {
			if dec, err := extractDecompositionFromFunctionCall(part.FunctionCall); err == nil {
				return dec, nil
			} else {
				lastErr = err
			}
		}
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		if dec, err := parseJSONFromText(part.Text); err == nil {
			return dec, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no decomposition found in content")
	}
	return Decomposition{}, lastErr
}

func extractDecompositionFromFunctionCall(call *genai.FunctionCall) (Decomposition, error) {
	if call == nil {
		return Decomposition{}, fmt.Errorf("function call is nil")
	}
	if !strings.EqualFold(call.Name, llmtools.BeadsToolName) {
		return Decomposition{}, fmt.Errorf("function call %q is not %q", call.Name, llmtools.BeadsToolName)
	}
	raw, err := json.Marshal(call.Args)
	if err != nil {
		return Decomposition{}, fmt.Errorf("marshal function args: %w", err)
	}
	var args llmtools.BeadsArgs
	if err := json.Unmarshal(raw, &args); err != nil {
		return Decomposition{}, fmt.Errorf("unmarshal beads tool args: %w", err)
	}
	// We don't have a specific operation that returns a full decomposition anymore.
	// But the agent might still call beads for something else.
	return Decomposition{}, fmt.Errorf("beads tool call does not contain a full decomposition")
}

func formatPlannerRunError(err error) string {
	msg := strings.TrimSpace(err.Error())
	if msg == "" {
		return "Planner run failed due to an unexpected error."
	}
	if strings.Contains(msg, "RESOURCE_EXHAUSTED") || strings.Contains(msg, "Error 429") {
		return "Planner model quota/rate limit exceeded.\n\n" + msg + "\n\nTry again later or switch planner model/provider in .norma/config.yaml."
	}
	return "Planner run failed.\n\n" + msg
}
