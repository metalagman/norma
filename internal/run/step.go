package run

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

	"github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/model"
	"github.com/rs/zerolog/log"
)

type stepResult struct {
	StepIndex int
	Role      string
	Iteration int
	TempDir   string
	FinalDir  string
	StartedAt time.Time
	EndedAt   time.Time
	Status    string
	Summary   string
	Response  *model.AgentResponse
	Protocol  string
	Verdict   *model.Verdict
	PatchPath string
}

func executeStep(ctx context.Context, runner agent.Runner, req model.AgentRequest, runStepsDir string) (stepResult, error) {
	stepName := fmt.Sprintf("%03d-%s", req.Step.Index, req.Step.Role)
	tmpSuffix, err := randomHex(3)
	if err != nil {
		return stepResult{}, fmt.Errorf("random suffix: %w", err)
	}
	tempDir := filepath.Join(runStepsDir, stepName+".tmp-"+tmpSuffix)
	finalDir := filepath.Join(runStepsDir, stepName)

	startedAt := time.Now().UTC()
	if err := os.MkdirAll(tempDir, 0o755); err != nil {
		return stepResult{}, fmt.Errorf("create temp step dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(tempDir, "logs"), 0o755); err != nil {
		return stepResult{}, fmt.Errorf("create logs dir: %w", err)
	}

	req.Paths.StepDir = tempDir
	if err := writeJSON(filepath.Join(tempDir, "input.json"), req); err != nil {
		return stepResult{}, err
	}

	info := runner.Describe()
	log.Info().
		Str("run_id", req.RunID).
		Str("role", req.Step.Role).
		Int("step_index", req.Step.Index).
		Int("iteration", req.Step.Iteration).
		Str("agent_type", info.Type).
		Strs("cmd", info.Cmd).
		Str("model", info.Model).
		Bool("tty", info.UseTTY).
		Str("work_dir", info.WorkDir).
		Msg("agent start")

	agentStart := time.Now().UTC()
	stdout, stderr, exitCode, runErr := runner.Run(ctx, req)
	agentDuration := time.Since(agentStart)
	writeFile(filepath.Join(tempDir, "logs", "stdout.txt"), stdout)
	writeFile(filepath.Join(tempDir, "logs", "stderr.txt"), stderr)
	log.Info().
		Str("run_id", req.RunID).
		Str("role", req.Step.Role).
		Int("step_index", req.Step.Index).
		Int("iteration", req.Step.Iteration).
		Int("exit_code", exitCode).
		Dur("duration", agentDuration).
		Msg("agent finished")
	log.Debug().
		Str("role", req.Step.Role).
		Int("step_index", req.Step.Index).
		Int("exit_code", exitCode).
		Int("stdout_bytes", len(stdout)).
		Int("stderr_bytes", len(stderr)).
		Str("stdout_excerpt", truncateLog(stdout, 800)).
		Str("stderr_excerpt", truncateLog(stderr, 800)).
		Msg("agent completed")

	res := stepResult{
		StepIndex: req.Step.Index,
		Role:      req.Step.Role,
		Iteration: req.Step.Iteration,
		TempDir:   tempDir,
		FinalDir:  finalDir,
		StartedAt: startedAt,
		EndedAt:   time.Now().UTC(),
		Status:    "ok",
	}

	if runErr != nil || exitCode != 0 {
		res.Status = "fail"
		res.Protocol = "agent_failed"
		res.Summary = fmt.Sprintf("agent failed: %v", runErr)
		log.Debug().Str("role", res.Role).Int("step_index", res.StepIndex).Int("exit_code", exitCode).Msg("agent execution failed")
	} else {
		resp, protoErr := parseAgentResponse(stdout)
		if protoErr != "" {
			res.Status = "fail"
			res.Protocol = protoErr
			res.Summary = protoErr
			log.Debug().Str("role", res.Role).Int("step_index", res.StepIndex).Msg("protocol error")
		} else {
			res.Response = resp
			res.Summary = resp.Summary
			if resp.Status == "fail" {
				res.Status = "fail"
			}
			log.Debug().
				Str("role", res.Role).
				Int("step_index", res.StepIndex).
				Str("response_status", resp.Status).
				Int("files_count", len(resp.Files)).
				Int("next_actions_count", len(resp.NextActions)).
				Msg("agent response parsed")
			if err := writeJSON(filepath.Join(tempDir, "output.json"), resp); err != nil {
				res.Status = "fail"
				res.Protocol = fmt.Sprintf("write output.json: %v", err)
				res.Summary = res.Protocol
			}
		}
	}

	if res.Status == "ok" {
		switch res.Role {
		case "plan":
			if err := validatePlan(filepath.Join(tempDir, "plan.md")); err != nil {
				res.Status = "fail"
				res.Protocol = err.Error()
				res.Summary = res.Protocol
			}
		case "check":
			verdict, err := readVerdict(filepath.Join(tempDir, "verdict.json"))
			if err != nil {
				res.Status = "fail"
				res.Protocol = err.Error()
				res.Summary = res.Protocol
			} else if verdict.Verdict != "PASS" && verdict.Verdict != "FAIL" {
				res.Status = "fail"
				res.Protocol = "invalid verdict.json: verdict must be PASS or FAIL"
				res.Summary = res.Protocol
			} else {
				res.Verdict = verdict
			}
			if _, err := os.Stat(filepath.Join(tempDir, "scorecard.md")); err != nil {
				res.Status = "fail"
				res.Protocol = "missing scorecard.md"
				res.Summary = res.Protocol
			}
		case "act":
			patchPath := filepath.Join(tempDir, "patch.diff")
			if _, err := os.Stat(patchPath); err == nil {
				res.PatchPath = patchPath
			}
		}
	}

	return res, nil
}

func validatePlan(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("missing plan.md")
	}
	text := strings.ToLower(string(data))
	required := []string{
		"backlog",
		"next slice",
		"stop condition",
		"verification",
	}
	missing := make([]string, 0, len(required))
	for _, key := range required {
		if !strings.Contains(text, key) {
			missing = append(missing, key)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("invalid plan.md: missing sections: %s", strings.Join(missing, ", "))
	}
	return nil
}

func parseAgentResponse(stdout []byte) (*model.AgentResponse, string) {
	var resp model.AgentResponse
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return nil, "protocol_error: stdout not valid JSON"
	}
	if resp.Status != "ok" && resp.Status != "fail" {
		return nil, "protocol_error: status must be ok or fail"
	}
	for _, f := range resp.Files {
		if !validRelPath(f) {
			return nil, "protocol_error: files must be relative paths under step_dir"
		}
	}
	return &resp, ""
}

func validRelPath(p string) bool {
	if p == "" {
		return false
	}
	if filepath.IsAbs(p) {
		return false
	}
	clean := filepath.Clean(p)
	if strings.HasPrefix(clean, "..") {
		return false
	}
	if strings.Contains(clean, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

func readVerdict(path string) (*model.Verdict, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("missing verdict.json")
	}
	var verdict model.Verdict
	if err := json.Unmarshal(data, &verdict); err != nil {
		return nil, fmt.Errorf("invalid verdict.json")
	}
	return &verdict, nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal json: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}

func writeFile(path string, data []byte) {
	_ = os.WriteFile(path, data, 0o644)
}

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func truncateLog(data []byte, limit int) string {
	if len(data) == 0 {
		return ""
	}
	text := strings.TrimSpace(string(data))
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "...(truncated)"
}

func finalizeStep(res *stepResult) error {
	if err := os.Rename(res.TempDir, res.FinalDir); err != nil {
		return fmt.Errorf("rename step dir: %w", err)
	}
	if res.PatchPath != "" {
		res.PatchPath = filepath.Join(res.FinalDir, "patch.diff")
	}
	return nil
}
