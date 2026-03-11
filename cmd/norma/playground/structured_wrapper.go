package playgroundcmd

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/metalagman/norma/internal/adk/agentconfig"
	"github.com/metalagman/norma/internal/adk/agentfactory"
	adkstructured "github.com/metalagman/norma/internal/adk/structured"
	"github.com/rs/zerolog"
	"github.com/spf13/cobra"
	adkagent "google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

const defaultStructuredMessage = "hello"

const structuredWrapperInputSchemaJSON = `{
  "type": "object",
  "required": ["input"],
  "properties": {
    "input": {
      "type": "object",
      "required": ["message"],
      "properties": {
        "message": {"type": "string", "minLength": 1}
      }
    }
  }
}`

const structuredWrapperOutputSchemaJSON = `{
  "type": "object",
  "required": ["status", "summary", "progress"],
  "properties": {
    "status": {"type": "string", "minLength": 1},
    "summary": {
      "type": "object",
      "required": ["text"],
      "properties": {
        "text": {"type": "string", "minLength": 1}
      }
    },
    "progress": {
      "type": "object",
      "required": ["title", "details"],
      "properties": {
        "title": {"type": "string", "minLength": 1},
        "details": {
          "type": "array",
          "items": {"type": "string"}
        }
      }
    }
  }
}`

type structuredOptions struct {
	Model string
}

func structuredCommand() *cobra.Command {
	opts := structuredOptions{
		Model: "opencode/minimax-m2.5-free",
	}

	return newACPPlaygroundCommand(
		"structured",
		"Run interactive REPL with a model wrapped in structured I/O guardrails",
		func(cmd *cobra.Command) {
			cmd.Flags().StringVar(&opts.Model, "model", opts.Model, "OpenCode model value used in the preconfigured opencode_acp agent")
		},
		func(ctx context.Context, repoRoot string, stdin io.Reader, stdout, stderr io.Writer) error {
			return runStructuredPlayground(ctx, repoRoot, opts, stdin, stdout, stderr)
		},
	)
}

func runStructuredPlayground(
	ctx context.Context,
	repoRoot string,
	opts structuredOptions,
	stdin io.Reader,
	stdout, stderr io.Writer,
) error {
	restoreLogLevel := forceGlobalDebugLoggingForStructured()
	defer restoreLogLevel()

	lockedStderr := &syncWriter{writer: stderr}
	logger := zerolog.New(zerolog.ConsoleWriter{Out: lockedStderr, TimeFormat: time.RFC3339}).
		Level(zerolog.DebugLevel).
		With().Timestamp().Str("component", "playground.structured").Logger()
	ctx = logger.WithContext(ctx)

	preconfigured := agentconfig.Config{
		Type:  agentconfig.AgentTypeOpenCodeACP,
		Model: strings.TrimSpace(opts.Model),
	}
	logger.Info().
		Str("repo_root", repoRoot).
		Str("type", preconfigured.Type).
		Str("model", preconfigured.Model).
		Msg("starting structured playground")

	executablePath, err := resolveExecutablePath()
	if err != nil {
		return fmt.Errorf("resolve executable path: %w", err)
	}
	normalized, err := agentconfig.NormalizeACPConfig(preconfigured, executablePath)
	if err != nil {
		return fmt.Errorf("normalize opencode_acp config: %w", err)
	}
	resolvedCmd, err := agentfactory.ResolveACPCommand(normalized)
	if err != nil {
		return fmt.Errorf("resolve normalized ACP command: %w", err)
	}

	factory := agentfactory.NewFactory(map[string]agentconfig.Config{
		"opencode_acp_preconfigured": normalized,
	})
	baseAgent, err := factory.CreateAgent(ctx, "opencode_acp_preconfigured", agentfactory.CreationRequest{
		Name:              "PlaygroundStructuredWrappedBaseAgent",
		Description:       "Playground base agent for structured wrapper",
		WorkingDirectory:  repoRoot,
		SystemInstruction: "It is a good day. Mention this inside one JSON string field of your final response, and do not add any text outside the required JSON object.",
		Stdout:            io.Discard,
		Stderr:            lockedStderr,
		PermissionHandler: func(_ context.Context, req acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
			for _, option := range req.Options {
				if option.Kind == acp.PermissionOptionKindAllowOnce || option.Kind == acp.PermissionOptionKindAllowAlways {
					return acp.RequestPermissionResponse{
						Outcome: acp.NewRequestPermissionOutcomeSelected(option.OptionId),
					}, nil
				}
			}
			return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}, nil
		},
	})
	if err != nil {
		return fmt.Errorf("create base agent: %w", err)
	}
	if closer, ok := baseAgent.(interface{ Close() error }); ok {
		defer func() {
			if closeErr := closer.Close(); closeErr != nil {
				logger.Warn().Err(closeErr).Msg("failed to close base agent runtime")
			}
		}()
	}

	wrapped, err := adkstructured.NewAgent(
		baseAgent,
		adkstructured.WithInputSchema(structuredWrapperInputSchemaJSON),
		adkstructured.WithOutputSchema(structuredWrapperOutputSchemaJSON),
	)
	if err != nil {
		return fmt.Errorf("create structured wrapper: %w", err)
	}

	sessionService := session.InMemoryService()
	adkRunner, err := runner.New(runner.Config{
		AppName:        "norma-playground-structured",
		Agent:          wrapped,
		SessionService: sessionService,
	})
	if err != nil {
		return fmt.Errorf("create adk runner: %w", err)
	}

	const userID = "norma-playground-user"
	sess, err := sessionService.Create(ctx, &session.CreateRequest{
		AppName: "norma-playground-structured",
		UserID:  userID,
	})
	if err != nil {
		return fmt.Errorf("create adk session: %w", err)
	}

	if _, err := fmt.Fprintf(
		stdout,
		"created wrapped agent %q (type=%s)\nresolved cmd: %v\n",
		wrapped.Name(),
		normalized.Type,
		resolvedCmd,
	); err != nil {
		return fmt.Errorf("write startup result: %w", err)
	}

	runTurn := func(message string) error {
		req := structuredInput{Input: structuredInputPayload{Message: strings.TrimSpace(message)}}
		if req.Input.Message == "" {
			req.Input.Message = defaultStructuredMessage
		}
		reqBytes, err := json.Marshal(req)
		if err != nil {
			return fmt.Errorf("marshal structured input: %w", err)
		}
		events := adkRunner.Run(
			ctx,
			userID,
			sess.Session.ID(),
			genai.NewContentFromText(string(reqBytes), genai.RoleUser),
			adkagent.RunConfig{},
		)
		rawOutput, err := collectStructuredModelOutput(events, logger)
		if err != nil {
			logger.Error().Err(err).Msg("structured turn failed before output normalization")
			return err
		}
		logger.Debug().
			Int("raw_output_len", len(rawOutput)).
			Str("raw_output_preview", truncateForLog(rawOutput, 600)).
			Msg("collected accumulated model output from wrapped agent")
		normalizedOutput, err := normalizeStructuredOutput(rawOutput)
		if err != nil {
			logger.Debug().
				Err(err).
				Int("raw_output_len", len(rawOutput)).
				Str("raw_output_preview", truncateForLog(rawOutput, 600)).
				Msg("failed to parse structured output from wrapped agent")
			return err
		}
		if _, err := fmt.Fprintf(stdout, "structured response:\n%s\n", prettyJSON(normalizedOutput)); err != nil {
			return fmt.Errorf("write structured response: %w", err)
		}
		return nil
	}

	logger.Info().Msg("starting interactive structured REPL")
	reader := bufio.NewReader(stdin)
	for {
		if _, err := fmt.Fprint(stdout, "> "); err != nil {
			return fmt.Errorf("write prompt: %w", err)
		}
		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return fmt.Errorf("read repl input: %w", err)
		}
		if errors.Is(err, io.EOF) && len(line) == 0 {
			logger.Info().Msg("received EOF, exiting structured REPL")
			break
		}
		message := strings.TrimSpace(line)
		if message == "" {
			if errors.Is(err, io.EOF) {
				break
			}
			continue
		}
		if message == "exit" || message == "quit" {
			logger.Info().Msg("received exit command, exiting structured REPL")
			break
		}
		if err := runTurn(message); err != nil {
			return err
		}
		if errors.Is(err, io.EOF) {
			break
		}
	}

	logger.Info().
		Str("agent_name", wrapped.Name()).
		Str("type", normalized.Type).
		Strs("command", resolvedCmd).
		Msg("completed structured playground run")
	return nil
}

func forceGlobalDebugLoggingForStructured() func() {
	prev := zerolog.GlobalLevel()
	zerolog.SetGlobalLevel(zerolog.DebugLevel)
	return func() {
		zerolog.SetGlobalLevel(prev)
	}
}

func resolveExecutablePath() (string, error) {
	return os.Executable()
}
