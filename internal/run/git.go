// Package run implements the orchestrator for the norma development lifecycle.
package run

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"

	"github.com/rs/zerolog/log"
)

func gitAvailable(ctx context.Context, repoRoot string) bool {
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
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}
