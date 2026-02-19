package planner

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/metalagman/norma/internal/adkrunner"
	"google.golang.org/adk/agent/llmagent"
	"google.golang.org/adk/model"
	"google.golang.org/adk/session"
	"google.golang.org/adk/tool"
	"google.golang.org/adk/tool/functiontool"
	"google.golang.org/genai"
)

// LLMPlanner implements interactive planning using ADK llmagent.
type LLMPlanner struct {
	repoRoot string
	model    model.LLM

	// TUI communication
	eventChan    chan *session.Event
	questionChan chan string
	responseChan chan string
}

// NewLLMPlanner constructs a new LLM planner.
func NewLLMPlanner(repoRoot string, m model.LLM) (*LLMPlanner, error) {
	return &LLMPlanner{
		repoRoot:     repoRoot,
		model:        m,
		eventChan:    make(chan *session.Event, 100),
		questionChan: make(chan string),
		responseChan: make(chan string),
	}, nil
}

// Generate runs an interactive planning session.
func (p *LLMPlanner) Generate(ctx context.Context, req Request) (Decomposition, string, error) {
	planRunDir, err := p.newPlanRunDir()
	if err != nil {
		return Decomposition{}, "", err
	}

	// Define tools using functiontool.New
	humanTool, err := functiontool.New(functiontool.Config{
		Name:        "human",
		Description: "Ask the user a question for clarification.",
	}, p.handleHumanQuestion)
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create human tool: %w", err)
	}

	persistTool, err := functiontool.New(functiontool.Config{
		Name:        "persist_plan",
		Description: "Persist the final decomposition and finish the planning session.",
	}, handlePersistPlan)
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create persist tool: %w", err)
	}

	shell := NewShellTool(p.repoRoot)
	shellTool, err := functiontool.New(functiontool.Config{
		Name:        "run_shell_command",
		Description: "Run a shell command for project inspection. Available commands: ls, grep, cat, find, tree, git, go, bd, echo. No pipes or redirects allowed.",
	}, shell.Run)
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create shell tool: %w", err)
	}

	// Create the llmagent
	plannerAgent, err := llmagent.New(llmagent.Config{
		Name:        "NormaPlanner",
		Description: "Interactive Norma planning agent that decomposes epics into features and tasks.",
		Model:       p.model,
		Tools:       []tool.Tool{humanTool, persistTool, shellTool},
		Instruction: buildLLMPlanPrompt(),
	})
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create llmagent: %w", err)
	}

	// Start TUI in a goroutine
	tuiModel, err := newPlannerModel(p.eventChan, p.questionChan, p.responseChan)
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

	// Run the agent using adkrunner
	initialState := map[string]any{
		"epic_description": req.EpicDescription,
	}

	initialContent := "Let's start planning."
	if req.EpicDescription != "" {
		initialContent = fmt.Sprintf("Let's start planning for the following project goal: %s", req.EpicDescription)
	}

	finalSession, lastContent, err := adkrunner.Run(ctx, adkrunner.RunInput{
		AppName:        "norma-plan",
		UserID:         "norma-user",
		SessionID:      "plan-" + time.Now().Format("150405"),
		Agent:          plannerAgent,
		InitialState:   initialState,
		InitialContent: genai.NewContentFromText(initialContent, genai.RoleUser),
		OnEvent: func(ev *session.Event) {
			p.eventChan <- ev
		},
	})

	// Signal end of session to TUI
	close(p.eventChan)

	if err != nil {
		prog.Quit()
		return Decomposition{}, "", fmt.Errorf("planning run failed: %w", err)
	}

	// Extract decomposition from session state
	var dec Decomposition
	decVal, err := finalSession.State().Get("decomposition")
	if err == nil {
		decBytes, err := json.Marshal(decVal)
		if err != nil {
			prog.Quit()
			return Decomposition{}, "", fmt.Errorf("marshal decomposition from state: %w", err)
		}
		if err := json.Unmarshal(decBytes, &dec); err != nil {
			prog.Quit()
			return Decomposition{}, "", fmt.Errorf("unmarshal decomposition: %w", err)
		}
	} else {
		// Fallback: try to parse from the last content received
		if lastContent == nil {
			prog.Quit()
			return Decomposition{}, "", fmt.Errorf("decomposition not found in session state and no model response received: %w", err)
		}
		found := false
		for _, part := range lastContent.Parts {
			if part.Text != "" {
				// Try to find JSON in the text
				if jsonDec, parseErr := parseJSONFromText(part.Text); parseErr == nil {
					dec = jsonDec
					found = true
					break
				}
			}
		}
		if !found {
			prog.Quit()
			return Decomposition{}, "", fmt.Errorf("decomposition not found in session state and could not parse from last model response: %w", err)
		}
	}

	if err := dec.Validate(); err != nil {
		prog.Quit()
		return Decomposition{}, "", fmt.Errorf("invalid decomposition: %w", err)
	}

	// Send decomposition to TUI
	prog.Send(planFinishedMsg(dec))

	// Wait for TUI to finish
	if tuiErr := <-tuiErrChan; tuiErr != nil {
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
3. Use 'run_shell_command' to inspect the current project state (files, structure, code) to make informed planning decisions.
4. Decompose the goal into features and tasks.
5. If you need more information or clarification to create a high-quality, executable plan, you MUST use the 'human' tool.
6. Once you have a full understanding of the scope and can produce a complete decomposition, use the 'persist_plan' tool to save the plan.
7. Do NOT finish the session until you have called 'persist_plan' with a valid decomposition.
8. If your environment does not support tool calling, output the final decomposition as a single JSON code block at the end of your response.

CRITICAL RULES:
- NEVER ask the user a question using plain text.
- ALWAYS use the 'human' tool for ANY interaction with the user.
- Use 'run_shell_command' to understand the codebase before planning.
- The session MUST remain active until 'persist_plan' is successfully called.
- If you just output text without calling a tool, the session will terminate and the plan will be lost.

Tool: run_shell_command
- Allowed commands: ls, grep, cat, find, tree, git, go, bd, echo.
- NO pipes (|), redirects (>, >>), or command chaining (&&, ||, ;, &) allowed.
- Use this to explore the project structure and existing code.

Planning Rules:
- Every task must be executable and include:
  - objective (what it accomplishes)
  - artifact (concrete files/paths/PR surface)
  - verify (concrete commands/checks to prove it works)
- Keep scope pragmatic. Prefer 2-6 features and 1-6 tasks per feature.
- Keep titles concise and action-oriented.
`
}

type humanArgs struct {
	Question string `json:"question"`
}

func (p *LLMPlanner) handleHumanQuestion(tctx tool.Context, args humanArgs) (string, error) {
	p.questionChan <- args.Question
	ans := <-p.responseChan
	return ans, nil
}

func handlePersistPlan(tctx tool.Context, dec Decomposition) (string, error) {
	if err := dec.Validate(); err != nil {
		return "", fmt.Errorf("validation failed: %w", err)
	}

	if err := tctx.State().Set("decomposition", dec); err != nil {
		return "", fmt.Errorf("failed to set decomposition in state: %w", err)
	}

	return "Plan persisted successfully. You can now finish the session.", nil
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
