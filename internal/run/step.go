// Package run implements the orchestrator for the norma development lifecycle.
package run

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/metalagman/norma/internal/agent"
	"github.com/metalagman/norma/internal/logging"
	"github.com/metalagman/norma/internal/workflows/normaloop"
	"github.com/rs/zerolog/log"
)

var (
	// ErrRetryable indicates that the step failed in a way that might succeed if retried.
	ErrRetryable = errors.New("retryable agent failure")
)

const (
	statusFail = "fail"
)

type stepResult struct {
	StepIndex int
	Role      string
	Iteration int
	FinalDir  string
	StartedAt time.Time
	EndedAt   time.Time
	Status    string
	Summary   string
	Response  *normaloop.AgentResponse
	Protocol  string
	Retries   int
}

func (r *Runner) executeStep(ctx context.Context, req normaloop.AgentRequest, runStepsDir string) (stepResult, error) {
	role := normaloop.GetRole(req.Step.Name)
	if role == nil {
		return stepResult{}, fmt.Errorf("unknown role %q", req.Step.Name)
	}
	roleRunner, ok := role.Runner().(agent.Runner)
	if !ok || roleRunner == nil {
		return stepResult{}, fmt.Errorf("role %q has no runner", req.Step.Name)
	}

	stepName := fmt.Sprintf("%02d-%s", req.Step.Index, req.Step.Name)
	if req.Context.Attempt > 1 {
		stepName = fmt.Sprintf("%02d-%s-retry-%d", req.Step.Index, req.Step.Name, req.Context.Attempt)
	}
	finalDir := filepath.Join(runStepsDir, stepName)

	startedAt := time.Now().UTC()
	if err := os.MkdirAll(finalDir, 0o755); err != nil {
		return stepResult{}, fmt.Errorf("create step dir: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(finalDir, "logs"), 0o755); err != nil {
		return stepResult{}, fmt.Errorf("create logs dir: %w", err)
	}

	// Mount workspace in step directory
	workspaceDir := filepath.Join(finalDir, "workspace")
	branchName := fmt.Sprintf("norma/task/%s", r.taskID)
	if _, err := mountWorktree(ctx, r.repoRoot, workspaceDir, branchName); err != nil {
		return stepResult{}, fmt.Errorf("mount step workspace: %w", err)
	}
	defer func() {
		_ = removeWorktree(ctx, r.repoRoot, workspaceDir)
	}()

	// Reconstruct progress.md into step artifacts directory
	artifactsDir := filepath.Join(finalDir, "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return stepResult{}, fmt.Errorf("create step artifacts: %w", err)
	}
	if err := r.reconstructProgress(artifactsDir); err != nil {
		log.Warn().Err(err).Msg("failed to reconstruct progress in step artifacts")
	}

	req.Paths.WorkspaceDir = "workspace"
	req.Paths.RunDir = "./"
	req.Paths.Progress = "artifacts/progress.md"
	if err := writeJSON(filepath.Join(finalDir, "input.json"), req); err != nil {
		return stepResult{}, err
	}

	// Use absolute paths for the actual agent execution context
	req.Paths.WorkspaceDir = workspaceDir
	req.Paths.RunDir = finalDir
	req.Paths.Progress = filepath.Join(artifactsDir, "progress.md")

	stdoutPath := filepath.Join(finalDir, "logs", "stdout.txt")
	stderrPath := filepath.Join(finalDir, "logs", "stderr.txt")
	stdoutFile, err := os.Create(stdoutPath)
	if err != nil {
		return stepResult{}, fmt.Errorf("create stdout log: %w", err)
	}
	defer func() {
		if cErr := stdoutFile.Close(); cErr != nil {
			log.Warn().Err(cErr).Msg("failed to close stdout log")
		}
	}()
	stderrFile, err := os.Create(stderrPath)
	if err != nil {
		return stepResult{}, fmt.Errorf("create stderr log: %w", err)
	}
	defer func() {
		if cErr := stderrFile.Close(); cErr != nil {
			log.Warn().Err(cErr).Msg("failed to close stderr log")
		}
	}()

	log.Info().
		Str("role", req.Step.Name).
		Str("run_id", req.Run.ID).
		Int("step_index", req.Step.Index).
		Int("iteration", req.Run.Iteration).
		Int("attempt", req.Context.Attempt).
		Str("run_dir", req.Paths.RunDir).
		Msg("agent start")

	stdoutWriter := io.Writer(stdoutFile)
	stderrWriter := io.Writer(stderrFile)
	if logging.DebugEnabled() {
		stdoutWriter = io.MultiWriter(stdoutFile, os.Stderr)
		stderrWriter = io.MultiWriter(stderrFile, os.Stderr)
	}

	agentStart := time.Now().UTC()
	stdout, _, exitCode, runErr := roleRunner.Run(ctx, req, stdoutWriter, stderrWriter)
	agentDuration := time.Since(agentStart)
	finishEvent := log.Info().
		Str("role", req.Step.Name).
		Str("run_id", req.Run.ID).
		Int("step_index", req.Step.Index).
		Int("iteration", req.Run.Iteration).
		Int("attempt", req.Context.Attempt).
		Int("exit_code", exitCode).
		Str("duration", agentDuration.String())
	if runErr != nil {
		finishEvent = finishEvent.Err(runErr)
		_, _ = fmt.Fprintln(stderrWriter, runErr)
	}
	finishEvent.Msg("agent finished")

	res := stepResult{
		StepIndex: req.Step.Index,
		Role:      req.Step.Name,
		Iteration: req.Run.Iteration,
		FinalDir:  finalDir,
		StartedAt: startedAt,
		EndedAt:   time.Now().UTC(),
		Status:    statusOK,
		Retries:   req.Context.Attempt,
	}

	if runErr != nil || exitCode != 0 {
		res.Status = statusFail
		res.Protocol = "agent_failed"
		res.Summary = fmt.Sprintf("agent failed: %v", runErr)
		log.Warn().Str("role", res.Role).Int("step_index", res.StepIndex).Int("exit_code", exitCode).Msg("agent execution failed")
		return res, ErrRetryable
	}

	// Try reading output.json from step dir first
	var resp *normaloop.AgentResponse
	var protoErr string
	outputPath := filepath.Join(finalDir, "output.json")
	if data, err := os.ReadFile(outputPath); err == nil {
		resp, protoErr = parseAgentResponse(data)
		if protoErr == "" {
			log.Debug().Str("role", res.Role).Msg("using output.json from step directory")
		}
	}

	// Fallback to stdout if output.json is missing or invalid
	if resp == nil {
		resp, protoErr = parseAgentResponse(stdout)
	}

	if protoErr != "" {
		res.Status = statusFail
		res.Protocol = protoErr
		res.Summary = protoErr
		log.Debug().Str("role", res.Role).Int("step_index", res.StepIndex).Msg("protocol error")
		return res, ErrRetryable
	}

	res.Response = resp
	res.Summary = resp.Summary.Text
	if resp.Status != statusOK {
		res.Status = resp.Status
	}
	log.Debug().
		Str("role", res.Role).
		Int("step_index", res.StepIndex).
		Str("response_status", resp.Status).
		Msg("agent response parsed")

	// Ensure output.json exists and is fresh
	if err := writeJSON(outputPath, resp); err != nil {
		res.Status = statusFail
		res.Protocol = fmt.Errorf("write output.json: %v", err).Error()
		res.Summary = res.Protocol
	}

	return res, nil
}

func parseAgentResponse(stdout []byte) (*normaloop.AgentResponse, string) {
	var resp normaloop.AgentResponse
	if err := json.Unmarshal(stdout, &resp); err != nil {
		recovered, ok := extractJSON(stdout)
		if !ok || json.Unmarshal(recovered, &resp) != nil {
			return nil, "protocol_error: stdout not valid JSON"
		}
	}
	if resp.Status != statusOK && resp.Status != statusFail && resp.Status != statusStop && resp.Status != statusError {
		return nil, "protocol_error: invalid status"
	}
	return &resp, ""
}

func extractJSON(data []byte) ([]byte, bool) {
	start := bytes.IndexByte(data, '{')
	end := bytes.LastIndexByte(data, '}')
	if start == -1 || end == -1 || start >= end {
		return nil, false
	}
	return data[start : end+1], true
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

func randomHex(bytesLen int) (string, error) {
	buf := make([]byte, bytesLen)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}
