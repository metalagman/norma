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
	log.Logger = log.Output(writer)
}
