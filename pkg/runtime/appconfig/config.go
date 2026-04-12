package appconfig

import (
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/go-playground/validator/v10"
	"github.com/go-viper/mapstructure/v2"
	"github.com/normahq/norma/pkg/runtime/agentconfig"
)

// NormaConfig contains runtime agent-factory settings for Norma.
//
// Config shape:
//
//	norma:
//	  providers: ...
//	  mcp_servers: ...
type NormaConfig struct {
	Providers  map[string]agentconfig.Config          `json:"providers,omitempty"   mapstructure:"providers"   validate:"required,gt=0"`
	MCPServers map[string]agentconfig.MCPServerConfig `json:"mcp_servers,omitempty" mapstructure:"mcp_servers" validate:"omitempty"`
}

// Validate validates the runtime norma config.
func (c NormaConfig) Validate() error {
	errList := make([]string, 0)

	if err := normaConfigValidator.Struct(c); err != nil {
		if invErr, ok := err.(*validator.InvalidValidationError); ok {
			return fmt.Errorf("validate norma config: %w", invErr)
		}
		for _, validationErr := range err.(validator.ValidationErrors) {
			errList = append(errList, formatValidationError(validationErr))
		}
	}

	for name, providerCfg := range c.Providers {
		if err := providerCfg.Validate(); err != nil {
			errList = append(errList, fmt.Sprintf("agent %q: %v", name, err))
		}
	}
	for name, mcpCfg := range c.MCPServers {
		if err := agentconfig.ValidateMCPServerConfig(mcpCfg); err != nil {
			errList = append(errList, fmt.Sprintf("mcp %q: %v", name, err))
		}
	}

	if len(errList) == 0 {
		return nil
	}
	sort.Strings(errList)
	return fmt.Errorf("norma config validation failed: %s", strings.Join(errList, "; "))
}

// ValidateSettings decodes and validates raw "norma" settings.
func ValidateSettings(settings map[string]any) error {
	if err := detectLegacyAgentsKey(settings); err != nil {
		return err
	}

	var cfg NormaConfig
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Metadata: nil,
		Result:   &cfg,
		TagName:  "mapstructure",
	})
	if err != nil {
		return fmt.Errorf("failed to create decoder: %w", err)
	}
	if err := decoder.Decode(settings); err != nil {
		return fmt.Errorf("failed to decode settings: %w", err)
	}
	return cfg.Validate()
}

func detectLegacyAgentsKey(settings map[string]any) error {
	normaSettings, ok := settings["norma"]
	if !ok {
		return nil
	}

	normaMap, ok := toStringAnyMap(normaSettings)
	if !ok {
		return nil
	}

	if _, hasAgents := normaMap["agents"]; hasAgents {
		return fmt.Errorf("legacy 'norma.agents' key detected; please migrate to 'norma.providers' (see https://docs.norma.io/migration/agents-to-providers)")
	}

	return nil
}

var normaConfigValidator = newNormaConfigValidator()

func newNormaConfigValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("mapstructure"), ",", 2)[0]
		if name == "" || name == "-" {
			return fld.Name
		}
		return name
	})
	return v
}

func formatValidationError(err validator.FieldError) string {
	field := err.Field()
	switch err.Tag() {
	case "required":
		return field + " is required"
	case "gt":
		return field + " must be greater than " + err.Param()
	case "min":
		return field + " must be at least " + err.Param()
	default:
		return field + " failed validation rule " + err.Tag()
	}
}
