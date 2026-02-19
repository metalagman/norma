package planner

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestShellTool(t *testing.T) {
	s := NewShellTool(".")

	t.Run("Allowed command", func(t *testing.T) {
		resp, err := s.Run(nil, ShellArgs{Command: "ls"})
		assert.NoError(t, err)
		assert.Equal(t, 0, resp.ExitCode)
		assert.Contains(t, resp.Stdout, "shell_tool.go")
	})

	t.Run("Allowed command echo", func(t *testing.T) {
		resp, err := s.Run(nil, ShellArgs{Command: "echo hello"})
		assert.NoError(t, err)
		assert.Equal(t, 0, resp.ExitCode)
		assert.Contains(t, resp.Stdout, "hello")
	})

	t.Run("Disallowed command", func(t *testing.T) {
		resp, err := s.Run(nil, ShellArgs{Command: "whoami"})
		assert.NoError(t, err)
		assert.Contains(t, resp.Error, "is not allowed")
	})

	t.Run("Dangerous metacharacter", func(t *testing.T) {
		resp, err := s.Run(nil, ShellArgs{Command: "ls; rm -rf /"})
		assert.NoError(t, err)
		assert.Contains(t, resp.Error, "not allowed")
	})

	t.Run("Stderr capture", func(t *testing.T) {
		resp, err := s.Run(nil, ShellArgs{Command: "ls non_existent_file"})
		assert.NoError(t, err)
		assert.NotEqual(t, 0, resp.ExitCode)
		assert.Contains(t, resp.Stderr, "No such file")
	})
}
