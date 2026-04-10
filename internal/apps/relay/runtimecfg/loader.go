package runtimecfg

import (
	"fmt"
	"strings"

	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
)

// RelayConfig holds the relay runtime fields that affect new session creation.
type RelayConfig struct {
	RootAgent          string   `mapstructure:"root_agent"`
	MCPServers         []string `mapstructure:"mcp_servers"`
	SystemInstructions string   `mapstructure:"system_instructions"`
}

// Document is the runtime relay config document shape.
type Document struct {
	Norma runtimeconfig.NormaConfig `mapstructure:"norma"`
	Relay RelayConfig               `mapstructure:"relay"`
}

// Snapshot is the normalized runtime config view for consumers.
type Snapshot struct {
	Norma runtimeconfig.NormaConfig
	Relay RelayConfig
}

// Loader reloads relay runtime config from disk using the same resolution options as relay start.
type Loader struct {
	runtimeOpts  runtimeconfig.RuntimeLoadOptions
	defaultsYAML []byte
}

// NewLoader creates a runtime config loader.
func NewLoader(runtimeOpts runtimeconfig.RuntimeLoadOptions, defaultsYAML []byte) *Loader {
	return &Loader{
		runtimeOpts: runtimeconfig.RuntimeLoadOptions{
			WorkingDir: strings.TrimSpace(runtimeOpts.WorkingDir),
			ConfigDir:  strings.TrimSpace(runtimeOpts.ConfigDir),
			Profile:    strings.TrimSpace(runtimeOpts.Profile),
		},
		defaultsYAML: append([]byte(nil), defaultsYAML...),
	}
}

// Load reads and decodes the current relay runtime config snapshot.
func (l *Loader) Load() (Snapshot, error) {
	if l == nil {
		return Snapshot{}, fmt.Errorf("runtime config loader is required")
	}

	var doc Document
	_, err := runtimeconfig.LoadConfigDocument(
		l.runtimeOpts,
		runtimeconfig.AppLoadOptions{
			AppName:      "relay",
			DefaultsYAML: l.defaultsYAML,
		},
		&doc,
	)
	if err != nil {
		return Snapshot{}, fmt.Errorf("load runtime relay config: %w", err)
	}

	return Snapshot(doc), nil
}
