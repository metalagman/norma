// Package config provides configuration loading and management for norma.
package config

import "github.com/normahq/runtime/v2/appconfig"

// ExpandEnv expands $VAR and ${VAR} placeholders in the provided text.
func ExpandEnv(input string) (string, error) {
	return appconfig.ExpandEnv(input)
}
