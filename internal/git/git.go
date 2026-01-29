package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rs/zerolog/log"
)

// Available checks if the given directory is inside a git work tree.
func Available(ctx context.Context, repoRoot string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

func RunCmd(ctx context.Context, dir string, name string, args ...string) string {
	log.Debug().Str("dir", dir).Str("cmd", name).Strs("args", args).Msg("running git command")
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		log.Warn().Err(err).Str("dir", dir).Str("cmd", name).Strs("args", args).Msg("git command failed")
	}
	return string(out)
}

func RunCmdOutput(ctx context.Context, dir string, name string, args ...string) (string, error) {
	log.Debug().Str("dir", dir).Str("cmd", name).Strs("args", args).Msg("running git command (output return)")
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func RunCmdErr(ctx context.Context, dir string, name string, args ...string) error {
	log.Debug().Str("dir", dir).Str("cmd", name).Strs("args", args).Msg("running git command (err return)")
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func CurrentBranch(ctx context.Context, repoRoot string) (string, error) {
	if !Available(ctx, repoRoot) {
		return "", fmt.Errorf("not a git repository: %s", repoRoot)
	}
	out, err := RunCmdOutput(ctx, repoRoot, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", fmt.Errorf("resolve base branch: %w", err)
	}
	branch := strings.TrimSpace(out)
	if branch == "" {
		return "", fmt.Errorf("resolve base branch: empty branch name")
	}
	if branch == "HEAD" {
		return "", fmt.Errorf("resolve base branch: detached HEAD")
	}
	return branch, nil
}
