package relay

// Config holds the configuration for the relay bot.
type Config struct {
	Relay RelayConfig `mapstructure:"relay"`
}

// RelayConfig holds the relay-specific configuration.
type RelayConfig struct {
	RootAgent   string            `mapstructure:"root_agent"`
	Telegram    TelegramConfig    `mapstructure:"telegram"`
	Logger      LoggerConfig      `mapstructure:"logger"`
	WorkingDir  string            `mapstructure:"working_dir"`
	StateDir    string            `mapstructure:"state_dir"`
	Workspace   WorkspaceConfig   `mapstructure:"workspace"`
	MCP         MCPConfig         `mapstructure:"mcp"`
	InternalMCP InternalMCPConfig `mapstructure:"internal_mcp"`
}

// TelegramConfig holds the Telegram bot configuration.
type TelegramConfig struct {
	Token   string        `mapstructure:"token"`
	Webhook WebhookConfig `mapstructure:"webhook"`
}

// WebhookConfig holds Telegram webhook receiver settings.
type WebhookConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	ListenAddr  string `mapstructure:"listen_addr"`
	Path        string `mapstructure:"path"`
	URL         string `mapstructure:"url"`
	SecretToken string `mapstructure:"secret_token"`
}

// LoggerConfig holds the logger configuration.
type LoggerConfig struct {
	Level  string `mapstructure:"level"`
	Pretty bool   `mapstructure:"pretty"`
}

// MCPConfig holds the MCP server configuration.
type MCPConfig struct {
	Address string `mapstructure:"address"`
}

// WorkspaceConfig controls relay Git workspace behavior.
type WorkspaceConfig struct {
	Mode string `mapstructure:"mode"`
}

// InternalMCPConfig contains startup configuration for internal MCP servers.
type InternalMCPConfig struct {
	Servers []string `mapstructure:"servers"`
}
