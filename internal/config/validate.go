package config

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/xeipuuv/gojsonschema"
)

//go:embed schema.json
var schemaJSON string

// ValidateSettings validates raw config settings against the JSON schema.
func ValidateSettings(settings map[string]any) error {
	schemaLoader := gojsonschema.NewStringLoader(schemaJSON)
	documentLoader := gojsonschema.NewGoLoader(settings)

	result, err := gojsonschema.Validate(schemaLoader, documentLoader)
	if err != nil {
		return fmt.Errorf("validate config schema: %w", err)
	}
	if result.Valid() {
		return nil
	}

	errs := make([]string, 0, len(result.Errors()))
	for _, schemaErr := range result.Errors() {
		errs = append(errs, schemaErr.String())
	}
	sort.Strings(errs)

	return fmt.Errorf("config schema validation failed: %s", strings.Join(errs, "; "))
}
