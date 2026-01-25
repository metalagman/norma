package logging

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// Init configures zerolog global logger.
func Init(debug bool) {
	level := zerolog.InfoLevel
	if debug {
		level = zerolog.DebugLevel
	}
	zerolog.SetGlobalLevel(level)
	zerolog.TimeFieldFormat = time.RFC3339
	writer := zerolog.ConsoleWriter{Out: os.Stderr, TimeFormat: time.RFC3339}

	// Order fields with role first when present, then other common fields
	writer.FieldsOrder = []string{
		"role",
		"run_id",
		"iteration",
		"step_index",
		"attempt",
	}

	// Debug: let's verify our configuration is applied
	if debug {
		debugLogger := zerolog.New(writer)
		debugLogger.Info().Str("role", "plan").Str("run_id", "debug-test").Int("iteration", 0).Msg("logging initialized - role should be first")
	}

	log.Logger = log.Output(writer)
}
