package tools

import "github.com/rs/zerolog"

// RuntimeConfig controls command runtime behavior across embedding contexts.
type RuntimeConfig struct {
	// IncludeDebugFlag adds a local --debug flag to the command.
	IncludeDebugFlag bool
	// DebugEnabled reports inherited debug mode (for example, from parent CLI).
	DebugEnabled func() bool
}

// StandaloneRuntimeConfig returns runtime defaults for standalone tool binaries.
func StandaloneRuntimeConfig() RuntimeConfig {
	return RuntimeConfig{IncludeDebugFlag: true}
}

func (c RuntimeConfig) resolveDebug(localDebug bool) bool {
	if localDebug {
		return true
	}
	if c.DebugEnabled == nil {
		return false
	}
	return c.DebugEnabled()
}

func (c RuntimeConfig) resolveLogLevel(defaultLevel zerolog.Level, localDebug bool) zerolog.Level {
	if c.resolveDebug(localDebug) {
		return zerolog.DebugLevel
	}
	return defaultLevel
}
