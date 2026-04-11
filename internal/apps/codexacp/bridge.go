package codexacp

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"

	acp "github.com/coder/acp-go-sdk"
	"github.com/normahq/norma/internal/apps/appio"
	"github.com/normahq/norma/internal/logging"
)

// DefaultAgentName is the fallback ACP agent name used when app-server identity is unavailable.
const DefaultAgentName = "norma-codex-acp"

// DefaultAgentVersion is the fallback ACP agent version used when app-server identity is unavailable.
const DefaultAgentVersion = "dev"

type appServerBackendFactory func(ctx context.Context, cwd string) (appServerSession, error)

// RunProxy starts a Codex app-server and exposes it as an ACP agent over stdio.
func RunProxy(ctx context.Context, workingDir string, opts Options, stdin io.Reader, stdout, stderr io.Writer) error {
	if stdin == nil {
		return errors.New("stdin is required")
	}
	if stdout == nil {
		return errors.New("stdout is required")
	}
	if stderr == nil {
		return errors.New("stderr is required")
	}
	if err := opts.validate(); err != nil {
		return err
	}

	lockedStderr := appio.NewSyncWriter(stderr)
	logger := logging.Ctx(ctx)

	command := buildCodexAppCommand(opts)
	cmdName, cmdArgs := splitCommandForLog(command)
	requestedAgentName := strings.TrimSpace(opts.Name)
	bridgeClientName := requestedAgentName
	if bridgeClientName == "" {
		bridgeClientName = DefaultAgentName
	}
	logger.Debug().
		Str("working_dir", workingDir).
		Str("agent_name", bridgeClientName).
		Str("cmd", cmdName).
		Strs("args", cmdArgs).
		Msg("starting codex acp bridge")

	sessionFactory := func(factoryCtx context.Context, sessionCWD string) (appServerSession, error) {
		return connectAppServerBackend(factoryCtx, workingDir, sessionCWD, command, bridgeClientName, lockedStderr, logger)
	}
	identity, err := validateAppServerFactory(ctx, sessionFactory, workingDir)
	if err != nil {
		logger.Error().Err(err).Msg("codex app-server initialization failed")
		return err
	}
	agentName, agentVersion := resolveAgentIdentity(requestedAgentName, identity)
	logger.Debug().
		Str("resolved_agent_name", agentName).
		Str("resolved_agent_version", agentVersion).
		Msg("resolved acp agent identity")

	proxy := newCodexACPProxyAgent(sessionFactory, agentName, opts.appConfig(), logger)
	proxy.setAgentVersion(agentVersion)
	conn := acp.NewAgentSideConnection(proxy, stdout, stdin)
	conn.SetLogger(slog.New(slog.NewTextHandler(io.Discard, nil)))
	proxy.setConnection(conn)

	select {
	case <-conn.Done():
		logger.Debug().Msg("acp client disconnected")
		proxy.closeAllSessionBackends()
		return nil
	case <-ctx.Done():
		logger.Debug().Err(ctx.Err()).Msg("proxy context canceled")
		proxy.closeAllSessionBackends()
		return ctx.Err()
	}
}

func buildCodexAppCommand(opts Options) []string {
	_ = opts
	return []string{"codex", "app-server"}
}

func validateAppServerFactory(ctx context.Context, factory appServerBackendFactory, cwd string) (appServerIdentity, error) {
	backend, err := factory(ctx, cwd)
	if err != nil {
		return appServerIdentity{}, err
	}
	defer func() {
		_ = backend.Close()
		_ = backend.Wait()
	}()
	return parseAppServerIdentity(backend.InitializeResponse().UserAgent), nil
}

func splitCommandForLog(command []string) (string, []string) {
	if len(command) == 0 {
		return "", nil
	}
	args := append([]string(nil), command[1:]...)
	return command[0], args
}
