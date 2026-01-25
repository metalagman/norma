package run

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type budgetError struct {
	reason string
}

func (e budgetError) Error() string {
	return e.reason
}

func applyPatch(ctx context.Context, repoRoot string, patchPath string, budgets Budgets) (string, string, error) {
	if budgets.MaxPatchKB > 0 {
		info, err := os.Stat(patchPath)
		if err != nil {
			return "", "", fmt.Errorf("stat patch: %w", err)
		}
		maxBytes := int64(budgets.MaxPatchKB) * 1024
		if info.Size() > maxBytes {
			return "", "", budgetError{reason: "patch exceeds max_patch_kb"}
		}
	}
	if budgets.MaxChangedFiles > 0 {
		count, err := countChangedFiles(patchPath)
		if err != nil {
			return "", "", err
		}
		if count > budgets.MaxChangedFiles {
			return "", "", budgetError{reason: "patch exceeds max_changed_files"}
		}
	}

	gitOK := gitAvailable(ctx, repoRoot)
	if gitOK {
		beforeHash := strings.TrimSpace(runCmd(ctx, repoRoot, "git", "rev-parse", "HEAD"))
		beforeStatus := runCmd(ctx, repoRoot, "git", "status", "--porcelain")
		if err := runCmdErr(ctx, repoRoot, "git", "apply", "--whitespace=nowarn", patchPath); err != nil {
			_ = bestEffortRollback(ctx, repoRoot, patchPath)
			return beforeHash, beforeStatus, fmt.Errorf("git apply failed: %w", err)
		}
		afterHash := strings.TrimSpace(runCmd(ctx, repoRoot, "git", "rev-parse", "HEAD"))
		afterStatus := runCmd(ctx, repoRoot, "git", "status", "--porcelain")
		return beforeHash + "|" + afterHash, beforeStatus + "|" + afterStatus, nil
	}

	if err := runCmdErr(ctx, repoRoot, "patch", "-p1", "-i", patchPath); err != nil {
		return "", "", fmt.Errorf("patch apply failed: %w", err)
	}
	return "", "", nil
}

func isBudgetError(err error) bool {
	var be budgetError
	return errors.As(err, &be)
}

func countChangedFiles(patchPath string) (int, error) {
	data, err := os.ReadFile(patchPath)
	if err != nil {
		return 0, fmt.Errorf("read patch: %w", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	seen := make(map[string]struct{})
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "diff --git ") {
			parts := strings.Fields(line)
			if len(parts) >= 4 {
				path := strings.TrimPrefix(parts[3], "b/")
				seen[path] = struct{}{}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return 0, fmt.Errorf("scan patch: %w", err)
	}
	return len(seen), nil
}

func gitAvailable(ctx context.Context, repoRoot string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

func bestEffortRollback(ctx context.Context, repoRoot, patchPath string) error {
	status := runCmd(ctx, repoRoot, "git", "diff", "--name-only")
	if strings.TrimSpace(status) == "" {
		return nil
	}
	return runCmdErr(ctx, repoRoot, "git", "apply", "-R", patchPath)
}

func runCmd(ctx context.Context, dir string, name string, args ...string) string {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	return string(out)
}

func runCmdErr(ctx context.Context, dir string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// Budgets mirrors config/model budgets for patch application.
type Budgets struct {
	MaxIterations   int
	MaxPatchKB      int
	MaxChangedFiles int
	MaxRiskyFiles   int
}

func budgetsFromConfig(cfg BudgetsConfig) Budgets {
	return Budgets{
		MaxIterations:   cfg.MaxIterations,
		MaxPatchKB:      cfg.MaxPatchKB,
		MaxChangedFiles: cfg.MaxChangedFiles,
		MaxRiskyFiles:   cfg.MaxRiskyFiles,
	}
}

// BudgetsConfig is a small adapter to avoid import cycles.
type BudgetsConfig struct {
	MaxIterations   int
	MaxPatchKB      int
	MaxChangedFiles int
	MaxRiskyFiles   int
}
