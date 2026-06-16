package agentconfig

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-playground/validator/v10"
)

// MCPServerType represents the transport type for an MCP server.
type MCPServerType string

const (
	// MCPServerTypeStdio is the stdio transport type.
	MCPServerTypeStdio MCPServerType = "stdio"
	// MCPServerTypeHTTP is the HTTP transport type.
	MCPServerTypeHTTP MCPServerType = "http"
	// MCPServerTypeSSE is the SSE (Server-Sent Events) transport type.
	MCPServerTypeSSE MCPServerType = "sse"
)

// MCPServerConfig describes how to connect to an MCP server.
type MCPServerConfig struct {
	// Type selects the MCP transport implementation.
	Type MCPServerType `json:"type"                  yaml:"type"                  mapstructure:"type"                validate:"required,oneof=stdio http sse,mcp_requirements"`
	// Cmd is the stdio server executable path or argv prefix.
	Cmd []string `json:"cmd,omitempty"         yaml:"cmd,omitempty"         mapstructure:"cmd"                 validate:"omitempty,dive,notblank"`
	// Args appends additional stdio server arguments after Cmd.
	Args []string `json:"args,omitempty"        yaml:"args,omitempty"        mapstructure:"args"                validate:"omitempty,dive,notblank"`
	// Env defines environment variables for stdio server execution.
	Env map[string]string `json:"env,omitempty"         yaml:"env,omitempty"         mapstructure:"env"`
	// WorkingDir sets the stdio server process working directory.
	WorkingDir string `json:"working_dir,omitempty" yaml:"working_dir,omitempty" mapstructure:"working_dir"         validate:"omitempty,notblank"`
	// URL is the base endpoint for HTTP and SSE MCP transports.
	URL string `json:"url,omitempty"         yaml:"url,omitempty"         mapstructure:"url"`
	// Headers provides additional request headers for HTTP and SSE transports.
	Headers map[string]string `json:"headers,omitempty"     yaml:"headers,omitempty"     mapstructure:"headers"`
}

// ACPConfig is an ACP runtime configuration block used by generic and alias types.
type ACPConfig struct {
	// Cmd is the ACP executable argv prefix before any alias-specific defaults.
	Cmd []string `json:"cmd,omitempty"        yaml:"cmd,omitempty"        mapstructure:"cmd"        validate:"omitempty,dive,notblank"`
	// ExtraArgs appends CLI arguments after the resolved ACP command.
	ExtraArgs []string `json:"extra_args,omitempty" yaml:"extra_args,omitempty" mapstructure:"extra_args" validate:"omitempty,dive,notblank"`
	// Model selects the runtime model identifier when the backend supports it.
	Model string `json:"model,omitempty"      yaml:"model,omitempty"      mapstructure:"model"      validate:"omitempty,notblank"`
	// ReasoningEffort selects the provider reasoning effort when supported.
	ReasoningEffort string `json:"reasoning_effort,omitempty" yaml:"reasoning_effort,omitempty" mapstructure:"reasoning_effort" validate:"omitempty,oneof=minimal low medium high xhigh"`
	// Mode selects the runtime session mode when the backend supports it.
	Mode string `json:"mode,omitempty"       yaml:"mode,omitempty"       mapstructure:"mode"       validate:"omitempty,notblank"`
}

// LocalAPIConfig is a local API-backed runtime configuration block.
type LocalAPIConfig struct {
	// APIKey authenticates requests to the hosted provider API.
	APIKey string `json:"api_key,omitempty" yaml:"api_key,omitempty" mapstructure:"api_key"`
	// Model selects the hosted provider model identifier.
	Model string `json:"model,omitempty"   yaml:"model,omitempty"   mapstructure:"model"   validate:"omitempty,notblank"`
}

// PoolConfig is the pool runtime configuration block.
type PoolConfig struct {
	// Members lists provider IDs in ordered failover order.
	Members []string `json:"members,omitempty" yaml:"members,omitempty" mapstructure:"members" validate:"omitempty,dive,notblank"`
}

// Config describes how to run an agent.
//
// The schema is strict and discriminated by type:
//
//	type: <agent_type>
//	<agent_type>:
//	  ...type-specific config...
type Config struct {
	// Type selects the runtime backend implementation.
	Type string `json:"type"                           yaml:"type"                           mapstructure:"type"                validate:"required,oneof=generic_acp gemini_acp codex_acp opencode_acp copilot_acp claude_code_acp openai aistudio pool,agent_blocks"`
	// MCPServers lists MCP server IDs resolved by higher-level registries.
	MCPServers []string `json:"mcp_servers,omitempty"          yaml:"mcp_servers,omitempty"          mapstructure:"mcp_servers"         validate:"omitempty,dive,notblank"`
	// SystemInstructions defines provider-level instructions applied by default.
	SystemInstructions string `json:"system_instructions,omitempty"  yaml:"system_instructions,omitempty"  mapstructure:"system_instructions" validate:"omitempty,notblank"`

	// GenericACP configures a custom ACP executable.
	GenericACP *ACPConfig `json:"generic_acp,omitempty"     yaml:"generic_acp,omitempty"     mapstructure:"generic_acp"`
	// GeminiACP configures the Gemini ACP alias.
	GeminiACP *ACPConfig `json:"gemini_acp,omitempty"      yaml:"gemini_acp,omitempty"      mapstructure:"gemini_acp"`
	// CodexACP configures the Codex ACP alias.
	CodexACP *ACPConfig `json:"codex_acp,omitempty"       yaml:"codex_acp,omitempty"       mapstructure:"codex_acp"`
	// OpenCodeACP configures the OpenCode ACP alias.
	OpenCodeACP *ACPConfig `json:"opencode_acp,omitempty"    yaml:"opencode_acp,omitempty"    mapstructure:"opencode_acp"`
	// CopilotACP configures the Copilot ACP alias.
	CopilotACP *ACPConfig `json:"copilot_acp,omitempty"     yaml:"copilot_acp,omitempty"     mapstructure:"copilot_acp"`
	// ClaudeCodeACP configures the Claude Code ACP alias.
	ClaudeCodeACP *ACPConfig `json:"claude_code_acp,omitempty" yaml:"claude_code_acp,omitempty" mapstructure:"claude_code_acp"`
	// OpenAI configures a local OpenAI-backed hosted agent.
	OpenAI *LocalAPIConfig `json:"openai,omitempty"         yaml:"openai,omitempty"          mapstructure:"openai"`
	// AIStudio configures a local Google AI Studio-backed hosted agent.
	AIStudio *LocalAPIConfig `json:"aistudio,omitempty"       yaml:"aistudio,omitempty"        mapstructure:"aistudio"`
	// PoolConfig configures an ordered failover pool.
	PoolConfig *PoolConfig `json:"pool,omitempty"           yaml:"pool,omitempty"            mapstructure:"pool"`
}

// ResolvedConfig is a runtime-ready agent configuration produced from Config normalization.
type ResolvedConfig struct {
	// Type is the normalized runtime backend type.
	Type string
	// MCPServers are the resolved MCP server IDs referenced by the runtime.
	MCPServers []string
	// SystemInstructions are the normalized provider-level instructions.
	SystemInstructions string
	// ReasoningEffort is the normalized provider reasoning effort.
	ReasoningEffort string

	// Command is the runtime command argv for ACP-backed agents.
	Command []string
	// APIKey is the resolved hosted-provider credential.
	APIKey string
	// Model is the resolved runtime model identifier.
	Model string
	// Mode is the resolved runtime mode identifier.
	Mode string
	// PoolMembers are the resolved provider IDs in pool failover order.
	PoolMembers []string
}

// Description returns a human-readable description of the agent config.
// Format: "name: type=<type> model=<model> mode=<mode>" with missing parts omitted.
func (c Config) Description(name string) string {
	var parts []string
	if c.Type != "" {
		parts = append(parts, fmt.Sprintf("type=%s", c.Type))
	}
	if spec, ok := c.selectedACPBlock(); ok {
		if spec.Model != "" {
			parts = append(parts, fmt.Sprintf("model=%s", spec.Model))
		}
		if spec.Mode != "" {
			parts = append(parts, fmt.Sprintf("mode=%s", spec.Mode))
		}
	}
	if spec, ok := c.selectedLocalAPIBlock(); ok {
		if spec.Model != "" {
			parts = append(parts, fmt.Sprintf("model=%s", spec.Model))
		}
	}
	if len(parts) == 0 {
		return name
	}
	return fmt.Sprintf("%s: %s", name, strings.Join(parts, " "))
}

// Description returns a human-readable description of the resolved runtime config.
func (c ResolvedConfig) Description(name string) string {
	var parts []string
	if c.Type != "" {
		parts = append(parts, fmt.Sprintf("type=%s", c.Type))
	}
	if c.Model != "" {
		parts = append(parts, fmt.Sprintf("model=%s", c.Model))
	}
	if c.Mode != "" {
		parts = append(parts, fmt.Sprintf("mode=%s", c.Mode))
	}
	if len(parts) == 0 {
		return name
	}
	return fmt.Sprintf("%s: %s", name, strings.Join(parts, " "))
}

var configValidator = newConfigValidator()

func newConfigValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "" || name == "-" {
			return fld.Name
		}
		return name
	})
	_ = v.RegisterValidation("notblank", validateNotBlank)
	_ = v.RegisterValidation("agent_blocks", validateAgentBlocks)
	_ = v.RegisterValidation("mcp_requirements", validateMCPRequirements)
	return v
}

// Validate validates the agent configuration.
func (c Config) Validate() error {
	errs := make([]string, 0)

	if err := configValidator.Struct(c); err != nil {
		if invErr, ok := err.(*validator.InvalidValidationError); ok {
			return fmt.Errorf("validate agent config: %w", invErr)
		}
		for _, validationErr := range err.(validator.ValidationErrors) {
			if validationErr.Tag() == "agent_blocks" {
				errs = append(errs, explainAgentBlocksError(c))
				continue
			}
			errs = append(errs, formatValidationError(validationErr))
		}
	}

	if len(errs) == 0 {
		if err := validateAgentConfigSemantics(c); err != nil {
			errs = append(errs, err.Error())
		}
	}

	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)

	return fmt.Errorf("agent config schema validation failed: %s", strings.Join(errs, "; "))
}

func formatValidationError(err validator.FieldError) string {
	field := err.Field()
	switch err.Tag() {
	case "required":
		return field + " is required"
	case "oneof":
		return field + " must be one of: " + err.Param()
	case "min":
		return field + " must be at least " + err.Param()
	case "notblank":
		return field + " must have at least 1 character"
	case "agent_blocks":
		return "type-specific block configuration is invalid for selected type"
	case "mcp_requirements":
		return "mcp server type-specific requirements are invalid"
	default:
		return field + " failed validation rule " + err.Tag()
	}
}

const (
	// AgentTypeGenericACP is the type for custom ACP CLI executables.
	AgentTypeGenericACP = "generic_acp"

	// AgentTypeGeminiACP is the alias for Gemini CLI ACP mode.
	AgentTypeGeminiACP = "gemini_acp"
	// AgentTypeCodexACP is the alias for Codex ACP bridge mode.
	AgentTypeCodexACP = "codex_acp"
	// AgentTypeOpenCodeACP is the alias for OpenCode CLI ACP mode.
	AgentTypeOpenCodeACP = "opencode_acp"
	// AgentTypeCopilotACP is the alias for Copilot CLI ACP mode.
	AgentTypeCopilotACP = "copilot_acp"
	// AgentTypeClaudeCodeACP is the alias for Claude Code ACP mode.
	AgentTypeClaudeCodeACP = "claude_code_acp"
	// AgentTypeOpenAI is the type for local OpenAI-backed providers.
	AgentTypeOpenAI = "openai"
	// AgentTypeAIStudio is the type for local Google AI Studio-backed providers.
	AgentTypeAIStudio = "aistudio"
	// AgentTypePool is the pool type with ordered failover.
	AgentTypePool = "pool"
)

// SupportedAgentTypes returns all supported agent types.
func SupportedAgentTypes() []string {
	return []string{
		AgentTypeGenericACP,
		AgentTypeGeminiACP,
		AgentTypeCodexACP,
		AgentTypeOpenCodeACP,
		AgentTypeCopilotACP,
		AgentTypeClaudeCodeACP,
		AgentTypeOpenAI,
		AgentTypeAIStudio,
		AgentTypePool,
	}
}

// IsValidAgentType reports whether the given type is a valid agent type.
func IsValidAgentType(agentType string) bool {
	for _, t := range SupportedAgentTypes() {
		if t == agentType {
			return true
		}
	}
	return false
}

// IsPoolType reports whether an agent type is a pool.
func IsPoolType(agentType string) bool {
	return strings.TrimSpace(agentType) == AgentTypePool
}

// IsACPType reports whether an agent type uses the ACP runtime.
func IsACPType(agentType string) bool {
	switch strings.TrimSpace(agentType) {
	case AgentTypeGenericACP, AgentTypeGeminiACP, AgentTypeOpenCodeACP, AgentTypeCodexACP, AgentTypeCopilotACP, AgentTypeClaudeCodeACP:
		return true
	default:
		return false
	}
}

// IsPlannerSupportedType reports whether planner mode supports the agent type.
func IsPlannerSupportedType(agentType string) bool {
	return IsACPType(agentType)
}

// SupportedMCPServerTypes returns all supported MCP server types.
func SupportedMCPServerTypes() []MCPServerType {
	return []MCPServerType{
		MCPServerTypeStdio,
		MCPServerTypeHTTP,
		MCPServerTypeSSE,
	}
}

// IsValidMCPServerType reports whether the given type is a valid MCP server type.
func IsValidMCPServerType(serverType MCPServerType) bool {
	for _, t := range SupportedMCPServerTypes() {
		if t == serverType {
			return true
		}
	}
	return false
}

// ValidateMCPServerConfig validates an MCP server configuration.
func ValidateMCPServerConfig(cfg MCPServerConfig) error {
	errs := make([]string, 0)
	if err := configValidator.Struct(cfg); err != nil {
		if invErr, ok := err.(*validator.InvalidValidationError); ok {
			return fmt.Errorf("validate mcp server config: %w", invErr)
		}
		for _, validationErr := range err.(validator.ValidationErrors) {
			if validationErr.Tag() == "mcp_requirements" {
				errs = append(errs, explainMCPRequirementsError(cfg))
				continue
			}
			errs = append(errs, formatValidationError(validationErr))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	sort.Strings(errs)
	return fmt.Errorf("mcp server config validation failed: %s", strings.Join(errs, "; "))
}

func validateNotBlank(fl validator.FieldLevel) bool {
	if fl.Field().Kind() != reflect.String {
		return false
	}
	return strings.TrimSpace(fl.Field().String()) != ""
}

func validateMCPRequirements(fl validator.FieldLevel) bool {
	cfg, ok := fl.Parent().Interface().(MCPServerConfig)
	if !ok {
		return false
	}
	switch cfg.Type {
	case MCPServerTypeStdio:
		return len(cfg.Cmd) > 0
	case MCPServerTypeHTTP, MCPServerTypeSSE:
		return strings.TrimSpace(cfg.URL) != ""
	default:
		return false
	}
}

func validateAgentBlocks(fl validator.FieldLevel) bool {
	cfg, ok := fl.Parent().Interface().(Config)
	if !ok {
		return false
	}
	present := 0
	if cfg.GenericACP != nil {
		present++
	}
	if cfg.GeminiACP != nil {
		present++
	}
	if cfg.CodexACP != nil {
		present++
	}
	if cfg.OpenCodeACP != nil {
		present++
	}
	if cfg.CopilotACP != nil {
		present++
	}
	if cfg.ClaudeCodeACP != nil {
		present++
	}
	if cfg.OpenAI != nil {
		present++
	}
	if cfg.AIStudio != nil {
		present++
	}
	if cfg.PoolConfig != nil {
		present++
	}
	if present == 0 && strings.TrimSpace(cfg.Type) == AgentTypeCopilotACP {
		return true
	}
	if present != 1 {
		return false
	}

	switch strings.TrimSpace(cfg.Type) {
	case AgentTypeGenericACP:
		return cfg.GenericACP != nil && len(cfg.GenericACP.Cmd) > 0
	case AgentTypeGeminiACP:
		return cfg.GeminiACP != nil && len(cfg.GeminiACP.Cmd) == 0
	case AgentTypeCodexACP:
		return cfg.CodexACP != nil && len(cfg.CodexACP.Cmd) == 0
	case AgentTypeOpenCodeACP:
		return cfg.OpenCodeACP != nil && len(cfg.OpenCodeACP.Cmd) == 0
	case AgentTypeCopilotACP:
		return cfg.CopilotACP == nil || len(cfg.CopilotACP.Cmd) == 0
	case AgentTypeClaudeCodeACP:
		return cfg.ClaudeCodeACP != nil && len(cfg.ClaudeCodeACP.Cmd) == 0
	case AgentTypeOpenAI:
		return cfg.OpenAI != nil
	case AgentTypeAIStudio:
		return cfg.AIStudio != nil
	case AgentTypePool:
		return cfg.PoolConfig != nil && len(cfg.PoolConfig.Members) > 0
	default:
		return false
	}
}

func explainAgentBlocksError(cfg Config) string {
	typeBlocks := map[string]bool{
		AgentTypeGenericACP:    cfg.GenericACP != nil,
		AgentTypeGeminiACP:     cfg.GeminiACP != nil,
		AgentTypeCodexACP:      cfg.CodexACP != nil,
		AgentTypeOpenCodeACP:   cfg.OpenCodeACP != nil,
		AgentTypeCopilotACP:    cfg.CopilotACP != nil,
		AgentTypeClaudeCodeACP: cfg.ClaudeCodeACP != nil,
		AgentTypeOpenAI:        cfg.OpenAI != nil,
		AgentTypeAIStudio:      cfg.AIStudio != nil,
		AgentTypePool:          cfg.PoolConfig != nil,
	}
	selectedCount := 0
	for _, present := range typeBlocks {
		if present {
			selectedCount++
		}
	}
	if selectedCount == 0 && strings.TrimSpace(cfg.Type) == AgentTypeCopilotACP {
		return ""
	}
	if selectedCount != 1 {
		return "exactly one type-specific block must be set"
	}

	typeName := strings.TrimSpace(cfg.Type)
	if present, ok := typeBlocks[typeName]; ok && !present {
		return fmt.Sprintf("%s block is required for type %s", typeName, typeName)
	}
	for blockType, present := range typeBlocks {
		if !present || blockType == typeName {
			continue
		}
		return fmt.Sprintf("%s block must be omitted when type is %s", blockType, typeName)
	}

	switch typeName {
	case AgentTypeGenericACP:
		if cfg.GenericACP == nil || len(cfg.GenericACP.Cmd) == 0 {
			return fmt.Sprintf("cmd is required for type %s", AgentTypeGenericACP)
		}
	case AgentTypeGeminiACP:
		if cfg.GeminiACP != nil && len(cfg.GeminiACP.Cmd) > 0 {
			return fmt.Sprintf("cmd must be omitted for type %s", AgentTypeGeminiACP)
		}
	case AgentTypeCodexACP:
		if cfg.CodexACP != nil && len(cfg.CodexACP.Cmd) > 0 {
			return fmt.Sprintf("cmd must be omitted for type %s", AgentTypeCodexACP)
		}
	case AgentTypeOpenCodeACP:
		if cfg.OpenCodeACP != nil && len(cfg.OpenCodeACP.Cmd) > 0 {
			return fmt.Sprintf("cmd must be omitted for type %s", AgentTypeOpenCodeACP)
		}
	case AgentTypeCopilotACP:
		if cfg.CopilotACP != nil && len(cfg.CopilotACP.Cmd) > 0 {
			return fmt.Sprintf("cmd must be omitted for type %s", AgentTypeCopilotACP)
		}
	case AgentTypeClaudeCodeACP:
		if cfg.ClaudeCodeACP != nil && len(cfg.ClaudeCodeACP.Cmd) > 0 {
			return fmt.Sprintf("cmd must be omitted for type %s", AgentTypeClaudeCodeACP)
		}
	case AgentTypeOpenAI:
		if cfg.OpenAI == nil {
			return fmt.Sprintf("openai block is required for type %s", AgentTypeOpenAI)
		}
	case AgentTypeAIStudio:
		if cfg.AIStudio == nil {
			return fmt.Sprintf("aistudio block is required for type %s", AgentTypeAIStudio)
		}
	case AgentTypePool:
		if cfg.PoolConfig == nil || len(cfg.PoolConfig.Members) == 0 {
			return "pool.members is required for type pool"
		}
	}
	return "type-specific block configuration is invalid for selected type"
}

func explainMCPRequirementsError(cfg MCPServerConfig) string {
	switch cfg.Type {
	case MCPServerTypeStdio:
		return "cmd is required for stdio type"
	case MCPServerTypeHTTP, MCPServerTypeSSE:
		return "url is required for http/sse type"
	default:
		return "mcp server type-specific requirements are invalid"
	}
}

// NormalizeConfig canonicalizes aliases and returns a runtime-ready configuration.
func NormalizeConfig(cfg Config, executablePath string) (ResolvedConfig, error) {
	_ = executablePath
	if err := validateAgentConfigSemantics(cfg); err != nil {
		return ResolvedConfig{}, err
	}
	resolved := ResolvedConfig{
		MCPServers:         append([]string(nil), cfg.MCPServers...),
		SystemInstructions: cfg.SystemInstructions,
	}

	switch strings.TrimSpace(cfg.Type) {
	case AgentTypeGeminiACP:
		if cfg.GeminiACP == nil {
			return ResolvedConfig{}, fmt.Errorf("gemini_acp block is required")
		}
		return resolveACPConfig(resolved, AgentTypeGenericACP, ACPConfig{
			Cmd:             appendGeminiModelFlag([]string{"gemini", "--acp"}, cfg.GeminiACP.Model),
			ExtraArgs:       append([]string(nil), cfg.GeminiACP.ExtraArgs...),
			Model:           cfg.GeminiACP.Model,
			ReasoningEffort: cfg.GeminiACP.ReasoningEffort,
			Mode:            cfg.GeminiACP.Mode,
		}), nil
	case AgentTypeOpenCodeACP:
		if cfg.OpenCodeACP == nil {
			return ResolvedConfig{}, fmt.Errorf("opencode_acp block is required")
		}
		return resolveACPConfig(resolved, AgentTypeGenericACP, ACPConfig{
			Cmd:             []string{"opencode", "acp"},
			ExtraArgs:       append([]string(nil), cfg.OpenCodeACP.ExtraArgs...),
			Model:           cfg.OpenCodeACP.Model,
			ReasoningEffort: cfg.OpenCodeACP.ReasoningEffort,
			Mode:            cfg.OpenCodeACP.Mode,
		}), nil
	case AgentTypeCodexACP:
		if cfg.CodexACP == nil {
			return ResolvedConfig{}, fmt.Errorf("codex_acp block is required")
		}
		return resolveACPConfig(resolved, AgentTypeGenericACP, ACPConfig{
			Cmd:             []string{"npx", "-y", "@normahq/codex-acp-bridge@latest"},
			ExtraArgs:       append([]string(nil), cfg.CodexACP.ExtraArgs...),
			Model:           cfg.CodexACP.Model,
			ReasoningEffort: cfg.CodexACP.ReasoningEffort,
			Mode:            cfg.CodexACP.Mode,
		}), nil
	case AgentTypeCopilotACP:
		copilotACP := cfg.CopilotACP
		if copilotACP == nil {
			copilotACP = &ACPConfig{}
		}
		return resolveACPConfig(resolved, AgentTypeGenericACP, ACPConfig{
			Cmd:             []string{"copilot", "--acp", "--stdio"},
			ExtraArgs:       append([]string(nil), copilotACP.ExtraArgs...),
			Model:           copilotACP.Model,
			ReasoningEffort: copilotACP.ReasoningEffort,
			Mode:            copilotACP.Mode,
		}), nil
	case AgentTypeClaudeCodeACP:
		if cfg.ClaudeCodeACP == nil {
			return ResolvedConfig{}, fmt.Errorf("claude_code_acp block is required")
		}
		return resolveACPConfig(resolved, AgentTypeGenericACP, ACPConfig{
			Cmd:             []string{"npx", "-y", "@zed-industries/claude-code-acp@latest"},
			ExtraArgs:       append([]string(nil), cfg.ClaudeCodeACP.ExtraArgs...),
			Model:           cfg.ClaudeCodeACP.Model,
			ReasoningEffort: cfg.ClaudeCodeACP.ReasoningEffort,
			Mode:            cfg.ClaudeCodeACP.Mode,
		}), nil
	case AgentTypeGenericACP:
		if cfg.GenericACP == nil {
			return ResolvedConfig{}, fmt.Errorf("generic_acp block is required")
		}
		return resolveACPConfig(resolved, AgentTypeGenericACP, *cfg.GenericACP), nil
	case AgentTypeOpenAI:
		if cfg.OpenAI == nil {
			return ResolvedConfig{}, fmt.Errorf("openai block is required")
		}
		resolved.Type = AgentTypeOpenAI
		resolved.APIKey = cfg.OpenAI.APIKey
		resolved.Model = cfg.OpenAI.Model
		return resolved, nil
	case AgentTypeAIStudio:
		if cfg.AIStudio == nil {
			return ResolvedConfig{}, fmt.Errorf("aistudio block is required")
		}
		resolved.Type = AgentTypeAIStudio
		resolved.APIKey = cfg.AIStudio.APIKey
		resolved.Model = cfg.AIStudio.Model
		return resolved, nil
	case AgentTypePool:
		if cfg.PoolConfig == nil {
			return ResolvedConfig{}, fmt.Errorf("pool block is required")
		}
		resolved.Type = AgentTypePool
		resolved.PoolMembers = append([]string(nil), cfg.PoolConfig.Members...)
		return resolved, nil
	default:
		return ResolvedConfig{}, fmt.Errorf("unsupported agent type %q", cfg.Type)
	}
}

// NormalizeConfigs canonicalizes agent configs for a map of named config blocks.
func NormalizeConfigs(cfgs map[string]Config, executablePath string) (map[string]ResolvedConfig, error) {
	if len(cfgs) == 0 {
		return map[string]ResolvedConfig{}, nil
	}

	resolved := make(map[string]ResolvedConfig, len(cfgs))
	for name, cfg := range cfgs {
		normCfg, err := NormalizeConfig(cfg, executablePath)
		if err != nil {
			return nil, fmt.Errorf("normalize agent %q: %w", name, err)
		}
		resolved[name] = normCfg
	}

	return resolved, nil
}

// NormalizeACPConfig is kept for compatibility and delegates to NormalizeConfig.
func NormalizeACPConfig(cfg Config, executablePath string) (ResolvedConfig, error) {
	return NormalizeConfig(cfg, executablePath)
}

// NormalizeACPConfigs is kept for compatibility and delegates to NormalizeConfigs.
func NormalizeACPConfigs(cfgs map[string]Config, executablePath string) (map[string]ResolvedConfig, error) {
	return NormalizeConfigs(cfgs, executablePath)
}

func resolveACPConfig(base ResolvedConfig, resolvedType string, spec ACPConfig) ResolvedConfig {
	base.Type = resolvedType
	base.Model = spec.Model
	base.ReasoningEffort = strings.TrimSpace(spec.ReasoningEffort)
	base.Mode = spec.Mode
	base.Command = resolveTemplatedArgs(spec.Cmd, spec.Model)
	if len(spec.ExtraArgs) > 0 {
		base.Command = append(base.Command, resolveTemplatedArgs(spec.ExtraArgs, spec.Model)...)
	}
	return base
}

func appendGeminiModelFlag(cmd []string, model string) []string {
	if model == "" {
		return cmd
	}
	return append(cmd, "--model", model)
}

func resolveTemplatedArgs(args []string, model string) []string {
	if len(args) == 0 {
		return nil
	}
	res := make([]string, len(args))
	for i, arg := range args {
		res[i] = strings.ReplaceAll(arg, "{{.Model}}", model)
	}
	return res
}

func (c Config) selectedACPBlock() (*ACPConfig, bool) {
	switch strings.TrimSpace(c.Type) {
	case AgentTypeGenericACP:
		return c.GenericACP, c.GenericACP != nil
	case AgentTypeGeminiACP:
		return c.GeminiACP, c.GeminiACP != nil
	case AgentTypeCodexACP:
		return c.CodexACP, c.CodexACP != nil
	case AgentTypeOpenCodeACP:
		return c.OpenCodeACP, c.OpenCodeACP != nil
	case AgentTypeCopilotACP:
		return c.CopilotACP, c.CopilotACP != nil
	case AgentTypeClaudeCodeACP:
		return c.ClaudeCodeACP, c.ClaudeCodeACP != nil
	default:
		return nil, false
	}
}

func (c Config) selectedLocalAPIBlock() (*LocalAPIConfig, bool) {
	switch strings.TrimSpace(c.Type) {
	case AgentTypeOpenAI:
		return c.OpenAI, c.OpenAI != nil
	case AgentTypeAIStudio:
		return c.AIStudio, c.AIStudio != nil
	default:
		return nil, false
	}
}

func validateAgentConfigSemantics(cfg Config) error {
	agentType := strings.TrimSpace(cfg.Type)
	var reasoningEffort string
	if spec, ok := cfg.selectedACPBlock(); ok {
		reasoningEffort = strings.TrimSpace(spec.ReasoningEffort)
	}

	if reasoningEffort == "" {
		return nil
	}
	if agentType != AgentTypeCodexACP {
		return fmt.Errorf("reasoning_effort is only supported for type %s", AgentTypeCodexACP)
	}

	return nil
}
