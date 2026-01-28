// Package run implements the orchestrator for the norma development lifecycle.
package run

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rs/zerolog/log"
)

// GitAvailable checks if the given directory is inside a git work tree.
func GitAvailable(ctx context.Context, repoRoot string) bool {
	cmd := exec.CommandContext(ctx, "git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = repoRoot
	return cmd.Run() == nil
}

func runCmd(ctx context.Context, dir string, name string, args ...string) string {
	log.Debug().Str("dir", dir).Str("cmd", name).Strs("args", args).Msg("running git command")
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		log.Warn().Err(err).Str("dir", dir).Str("cmd", name).Strs("args", args).Msg("git command failed")
	}
	return string(out)
}

func runCmdErr(ctx context.Context, dir string, name string, args ...string) error {
	log.Debug().Str("dir", dir).Str("cmd", name).Strs("args", args).Msg("running git command (err return)")
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}
