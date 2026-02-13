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

	"github.com/metalagman/ainvoke/adk"
	normaagent "github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/config"
	"github.com/rs/zerolog/log"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const (
	planInputSchema = `{
  "type":"object",
  "properties":{
    "epic_description":{"type":"string"},
    "mode":{"type":"string","enum":["wizard","auto"]},
    "clarifications":{
      "type":"array",
      "items":{
        "type":"object",
        "properties":{
          "question":{"type":"string"},
          "answer":{"type":"string"}
        },
        "required":["question","answer"],
        "additionalProperties":false
      }
    }
  },
  "required":["epic_description","mode"],
  "additionalProperties":false
}`
	planOutputSchema = `{
  "type":"object",
  "properties":{
    "summary":{"type":"string"},
    "epic":{
      "type":"object",
      "properties":{
        "title":{"type":"string"},
        "description":{"type":"string"}
      },
      "required":["title","description"],
      "additionalProperties":false
    },
    "features":{
      "type":"array",
      "items":{
        "type":"object",
        "properties":{
          "title":{"type":"string"},
          "description":{"type":"string"},
          "tasks":{
            "type":"array",
            "items":{
              "type":"object",
              "properties":{
                "title":{"type":"string"},
                "objective":{"type":"string"},
                "artifact":{"type":"string"},
                "verify":{"type":"array","items":{"type":"string"}},
                "notes":{"type":"string"}
              },
              "required":["title","objective","artifact","verify"],
              "additionalProperties":false
            }
          }
        },
        "required":["title","description","tasks"],
        "additionalProperties":false
      }
    }
  },
  "required":["summary","epic","features"],
  "additionalProperties":false
}`
)

type ExecPlanner struct {
	repoRoot string
	cfg      config.AgentConfig
	cmd      []string
}

func NewExecPlanner(repoRoot string, cfg config.AgentConfig) (*ExecPlanner, error) {
	cmd, err := normaagent.ResolveCmd(cfg)
	if err != nil {
		return nil, fmt.Errorf("resolve planner command: %w", err)
	}
	return &ExecPlanner{
		repoRoot: repoRoot,
		cfg:      cfg,
		cmd:      cmd,
	}, nil
}

func (p *ExecPlanner) Generate(ctx context.Context, req Request) (Decomposition, string, error) {
	if err := req.Validate(); err != nil {
		return Decomposition{}, "", err
	}

	planRunDir, err := p.newPlanRunDir()
	if err != nil {
		return Decomposition{}, "", err
	}

	stdoutFile, stderrFile, closeLogs, err := p.openLogFiles(planRunDir)
	if err != nil {
		return Decomposition{}, "", err
	}
	defer closeLogs()

	prompt := buildPlanPrompt()
	if err := os.WriteFile(filepath.Join(planRunDir, "logs", "prompt.txt"), []byte(prompt), 0o644); err != nil {
		return Decomposition{}, "", fmt.Errorf("write prompt log: %w", err)
	}

	reqJSON, err := json.Marshal(req)
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("marshal request: %w", err)
	}
	if err := os.WriteFile(filepath.Join(planRunDir, "input.json"), reqJSON, 0o644); err != nil {
		return Decomposition{}, "", fmt.Errorf("write input.json: %w", err)
	}

	execAgent, err := adk.NewExecAgent(
		"norma_plan",
		"Norma planning agent",
		p.cmd,
		adk.WithExecAgentPrompt(prompt),
		adk.WithExecAgentInputSchema(planInputSchema),
		adk.WithExecAgentOutputSchema(planOutputSchema),
		adk.WithExecAgentRunDir(planRunDir),
		adk.WithExecAgentUseTTY(p.cfg.UseTTY != nil && *p.cfg.UseTTY),
		adk.WithExecAgentStdout(stdoutFile),
		adk.WithExecAgentStderr(stderrFile),
	)
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create planning exec agent: %w", err)
	}

	sessionService := session.InMemoryService()
	adkRunner, err := runner.New(runner.Config{
		AppName:        "norma-plan",
		Agent:          execAgent,
		SessionService: sessionService,
	})
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create planning runner: %w", err)
	}

	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma-plan",
		UserID:  "norma-user",
	})
	if err != nil {
		return Decomposition{}, "", fmt.Errorf("create planning session: %w", err)
	}

	userContent := genai.NewContentFromText(string(reqJSON), genai.RoleUser)
	var lastOut []byte
	for ev, err := range adkRunner.Run(ctx, "norma-user", sess.Session.ID(), userContent, agent.RunConfig{}) {
		if err != nil {
			return Decomposition{}, "", fmt.Errorf("planning agent run failed: %w", err)
		}
		if ev.Content != nil && len(ev.Content.Parts) > 0 {
			lastOut = []byte(ev.Content.Parts[0].Text)
		}
	}
	if len(lastOut) == 0 {
		return Decomposition{}, "", fmt.Errorf("planning agent produced empty output")
	}

	out, err := parsePlanOutput(lastOut)
	if err != nil {
		return Decomposition{}, "", err
	}
	if err := out.Validate(); err != nil {
		return Decomposition{}, "", fmt.Errorf("invalid decomposition: %w", err)
	}

	outJSON, err := json.MarshalIndent(out, "", "  ")
	if err == nil {
		if writeErr := os.WriteFile(filepath.Join(planRunDir, "output.json"), outJSON, 0o644); writeErr != nil {
			log.Warn().Err(writeErr).Msg("failed to write planning output.json")
		}
	}

	return out, planRunDir, nil
}

func (p *ExecPlanner) newPlanRunDir() (string, error) {
	sfx, err := randomHex(3)
	if err != nil {
		return "", fmt.Errorf("generate planning run id: %w", err)
	}
	runID := fmt.Sprintf("%s-%s", time.Now().UTC().Format("20060102-150405"), sfx)
	runDir := filepath.Join(p.repoRoot, ".norma", "plans", runID)
	if err := os.MkdirAll(filepath.Join(runDir, "logs"), 0o755); err != nil {
		return "", fmt.Errorf("create planning logs dir: %w", err)
	}
	return runDir, nil
}

func (p *ExecPlanner) openLogFiles(runDir string) (*os.File, *os.File, func(), error) {
	stdoutFile, err := os.OpenFile(filepath.Join(runDir, "logs", "stdout.txt"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("open planning stdout log: %w", err)
	}
	stderrFile, err := os.OpenFile(filepath.Join(runDir, "logs", "stderr.txt"), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		_ = stdoutFile.Close()
		return nil, nil, nil, fmt.Errorf("open planning stderr log: %w", err)
	}
	closeFn := func() {
		_ = stdoutFile.Close()
		_ = stderrFile.Close()
	}
	return stdoutFile, stderrFile, closeFn, nil
}

func parsePlanOutput(out []byte) (Decomposition, error) {
	var dec Decomposition
	if err := json.Unmarshal(out, &dec); err == nil {
		return dec, nil
	}
	extracted, ok := normaagent.ExtractJSON(out)
	if !ok {
		return Decomposition{}, fmt.Errorf("planning output is not valid JSON")
	}
	if err := json.Unmarshal(extracted, &dec); err != nil {
		return Decomposition{}, fmt.Errorf("parse planning output: %w", err)
	}
	return dec, nil
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func buildPlanPrompt() string {
	return strings.TrimSpace(`
You are Norma's planning agent.
Your job is to decompose one global epic into a Beads-ready hierarchy:
1) one epic
2) multiple features under that epic
3) multiple executable tasks under each feature

Rules:
- Output ONLY valid JSON matching the provided schema.
- Do not include markdown, comments, or prose outside JSON.
- Every task must be executable and include:
  - objective
  - artifact (concrete files/paths/PR surface)
  - verify (1+ concrete commands/checks)
- Keep scope pragmatic. Prefer 2-6 features and 1-6 tasks per feature.
- Keep titles concise and action-oriented.
- For wizard mode, incorporate provided clarifications.
`)
}
