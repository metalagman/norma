// Package logging provides application-wide logging configuration.
package logging

import (
	"os"
	"time"

	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var debugEnabled bool

// Init initializes the global logger.
func Init(debug bool) {
	debugEnabled = debug
	level := zerolog.InfoLevel
	if debug {
		level = zerolog.DebugLevel
	}
	zerolog.SetGlobalLevel(level)
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stderr,
		TimeFormat: time.RFC3339,
	})
}

// DebugEnabled reports whether debug logging is enabled.
func DebugEnabled() bool {
	return debugEnabled
}
