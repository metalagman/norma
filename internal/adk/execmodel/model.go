// Package execmodel provides an implementation of the ADK model.LLM interface
// that executes an external command using the ainvoke library.
package execmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"

	"github.com/metalagman/ainvoke"
	"google.golang.org/adk/model"
	"google.golang.org/genai"
)

var _ model.LLM = (*Model)(nil)

// Model implements model.LLM using ainvoke.ExecRunner.
type Model struct {
	runner *ainvoke.ExecRunner
	cfg    Config
}

// Config describes how to run the executive model.
type Config struct {
	Name         string
	Cmd          []string
	UseTTY       bool
	RunDir       string
	InputSchema  string
	OutputSchema string
	Stdout       io.Writer
	Stderr       io.Writer
}

const (
	defaultInputSchema  = `{"type":"object","properties":{"input":{"type":"string"}},"required":["input"]}`
	defaultOutputSchema = `{"type":"object","properties":{"output":{"type":"string"}},"required":["output"]}`
)

// New creates a new executive model.
func New(cfg Config) (*Model, error) {
	if len(cfg.Cmd) == 0 {
		return nil, fmt.Errorf("cmd is required")
	}
	if cfg.InputSchema == "" {
		cfg.InputSchema = defaultInputSchema
	}
	if cfg.OutputSchema == "" {
		cfg.OutputSchema = defaultOutputSchema
	}
	runner, err := ainvoke.NewRunner(ainvoke.AgentConfig{
		Cmd:    cfg.Cmd,
		UseTTY: cfg.UseTTY,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create ainvoke runner: %w", err)
	}
	return &Model{
		runner: runner,
		cfg:    cfg,
	}, nil
}

// Name returns the model name.
func (m *Model) Name() string {
	if m.cfg.Name != "" {
		return m.cfg.Name
	}
	if len(m.cfg.Cmd) > 0 {
		return m.cfg.Cmd[0]
	}
	return "exec"
}

// GenerateContent executes the command using ainvoke.
func (m *Model) GenerateContent(ctx context.Context, req *model.LLMRequest, stream bool) iter.Seq2[*model.LLMResponse, error] {
	return func(yield func(*model.LLMResponse, error) bool) {
		if stream {
			yield(nil, fmt.Errorf("streaming is not supported by exec model"))
			return
		}

		var systemInstructions []string
		if req.Config != nil && req.Config.SystemInstruction != nil {
			for _, part := range req.Config.SystemInstruction.Parts {
				if part.Text != "" {
					systemInstructions = append(systemInstructions, part.Text)
				}
			}
		}

		inv := ainvoke.Invocation{
			RunDir:       m.cfg.RunDir,
			SystemPrompt: strings.Join(systemInstructions, "\n"),
			Input:        m.prepareInput(req.Contents),
			InputSchema:  m.cfg.InputSchema,
			OutputSchema: m.cfg.OutputSchema,
		}

		var runOpts []ainvoke.RunOption
		if m.cfg.Stdout != nil {
			runOpts = append(runOpts, ainvoke.WithStdout(m.cfg.Stdout))
		}
		if m.cfg.Stderr != nil {
			runOpts = append(runOpts, ainvoke.WithStderr(m.cfg.Stderr))
		}

		outBytes, errBytes, _, err := m.runner.Run(ctx, inv, runOpts...)
		if err != nil {
			if len(errBytes) == 0 && len(outBytes) > 0 {
				errBytes = outBytes
			}
			if len(errBytes) > 0 {
				yield(nil, fmt.Errorf("exec model run failed: %w (output: %s)", err, string(errBytes)))
			} else {
				yield(nil, fmt.Errorf("exec model run failed: %w", err))
			}
			return
		}

		outputData, err := os.ReadFile(filepath.Join(m.cfg.RunDir, ainvoke.OutputFileName))
		if err != nil {
			yield(nil, fmt.Errorf("read output: %w", err))
			return
		}

		yield(&model.LLMResponse{
			Content: genai.NewContentFromText(m.formatResponse(outputData), genai.RoleModel),
		}, nil)
	}
}

func (m *Model) prepareInput(contents []*genai.Content) any {
	if m.cfg.InputSchema == defaultInputSchema {
		var text string
		if len(contents) > 0 && len(contents[0].Parts) > 0 {
			text = contents[0].Parts[0].Text
		}
		return map[string]any{"input": text}
	}
	return contents
}

func (m *Model) formatResponse(outputData []byte) string {
	if m.cfg.OutputSchema != defaultOutputSchema {
		return string(outputData)
	}

	var outputObj any
	if err := json.Unmarshal(outputData, &outputObj); err != nil {
		return string(outputData)
	}

	obj, ok := outputObj.(map[string]any)
	if !ok {
		return string(outputData)
	}

	out, ok := obj["output"].(string)
	if !ok {
		return string(outputData)
	}

	return out
}
