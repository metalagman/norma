package command

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	codexacpbridge "github.com/normahq/norma/internal/apps/codexacpbridge"
	"github.com/normahq/norma/internal/logging"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

func TestCommandUsesBridgeComponentLogger(t *testing.T) {
	origRunProxy := runProxy
	origInitLogging := initLogging
	origLogger := log.Logger
	t.Cleanup(func() {
		runProxy = origRunProxy
		initLogging = origInitLogging
		log.Logger = origLogger
	})

	initLogging = func(...logging.OptOptionsSetter) error {
		return nil
	}
	runProxy = func(ctx context.Context, _ string, _ codexacpbridge.Options, _ io.Reader, _, _ io.Writer) error {
		logging.Ctx(ctx).Info().Msg("probe")
		return nil
	}

	var logs bytes.Buffer
	log.Logger = zerolog.New(&logs).Level(zerolog.DebugLevel)

	cmd := Command()
	cmd.SetIn(strings.NewReader(""))
	cmd.SetOut(io.Discard)
	cmd.SetErr(io.Discard)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	got := logs.String()
	if !strings.Contains(got, `"component":"codex.acp.bridge"`) {
		t.Fatalf("logs missing bridge component: %q", got)
	}
	if strings.Contains(got, `"component":"codex.acp.proxy"`) {
		t.Fatalf("logs contain old proxy component: %q", got)
	}
}
