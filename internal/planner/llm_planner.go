package planner

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

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
}

// NewLLMPlanner constructs a new LLM planner.
func NewLLMPlanner(repoRoot string, m model.LLM) (*LLMPlanner, error) {
	return &LLMPlanner{
		repoRoot: repoRoot,
		model:    m,
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
	}, handleHumanQuestion)
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

	// Create the llmagent
	plannerAgent, err := llmagent.New(llmagent.Config{
		Name:        "NormaPlanner",
		Description: "Interactive Norma planning agent that decomposes epics into features and tasks.",
		Model:       p.model,
		Tools:       []tool.Tool{humanTool, persistTool},
		Instruction: buildLLMPlanPrompt(),
	})
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create llmagent: %w", err)
	}

	// Run the agent using adkrunner
	initialState := map[string]any{
		"epic_description": req.EpicDescription,
	}

	finalSession, err := adkrunner.Run(ctx, adkrunner.RunInput{
		AppName:        "norma-plan",
		UserID:         "norma-user",
		SessionID:      "plan-" + time.Now().Format("150405"),
		Agent:          plannerAgent,
		InitialState:   initialState,
		InitialContent: genai.NewContentFromText("Let's start planning.", genai.RoleUser),
		OnEvent: func(ev *session.Event) {
			if ev == nil || ev.Content == nil {
				return
			}
			for _, part := range ev.Content.Parts {
				if part.Text != "" {
					fmt.Print(part.Text)
				}
			}
		},
	})
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("planning run failed: %w", err)
	}

	// Extract decomposition from session state
	decVal, err := finalSession.State().Get("decomposition")
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("decomposition not found in session state: %w", err)
	}

	decBytes, err := json.Marshal(decVal)
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("marshal decomposition from state: %w", err)
	}

	var dec Decomposition
	if err := json.Unmarshal(decBytes, &dec); err != nil {
		return Decomposition{}, "", fmt.Errorf("unmarshal decomposition: %w", err)
	}

	if err := dec.Validate(); err != nil {
		return Decomposition{}, "", fmt.Errorf("invalid decomposition: %w", err)
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
1. Check the session state for 'epic_description'. 
2. If 'epic_description' is missing, empty, or too vague, use the 'human' tool to ask the user what they want to build. 
3. Once you have a clear understanding, decompose the goal into features and tasks.
4. If you need more information or clarification to create a high-quality, executable plan, use the 'human' tool again.
5. Once you have a full understanding of the scope and can produce a complete decomposition, use the 'persist_plan' tool to save the plan.
6. Do NOT finish the session until you have called 'persist_plan' with a valid decomposition.

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

func handleHumanQuestion(tctx tool.Context, args humanArgs) (string, error) {
	fmt.Printf(`
[PLANNER QUESTION]: %s
> `, args.Question)
	scanner := bufio.NewScanner(os.Stdin)
	if scanner.Scan() {
		return scanner.Text(), nil
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return "No answer provided.", nil
}

func handlePersistPlan(tctx tool.Context, dec Decomposition) (string, error) {
	if err := dec.Validate(); err != nil {
		return "", fmt.Errorf("validation failed: %w", err)
	}

	type sessioner interface {
		Session() session.Session
	}
	if s, ok := tctx.(sessioner); ok {
		if err := s.Session().State().Set("decomposition", dec); err != nil {
			return "", err
		}
	} else {
		return "", fmt.Errorf("internal error: could not access session from tool context")
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
