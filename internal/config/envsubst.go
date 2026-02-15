// Package config provides configuration loading and management for norma.
package config

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
)

var placeholderPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// ExpandEnv expands $VAR and ${VAR} placeholders in the provided text.
func ExpandEnv(input string) (string, error) {
	missingVars := findMissingEnvVars(input)
	if len(missingVars) > 0 {
		return "", fmt.Errorf("missing environment variable(s): %s", strings.Join(missingVars, ", "))
	}

	return os.ExpandEnv(input), nil
}

func findMissingEnvVars(input string) []string {
	matches := placeholderPattern.FindAllStringSubmatch(input, -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	missing := make([]string, 0, len(matches))
	for _, match := range matches {
		name := match[1]
		if name == "" {
			name = match[2]
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}

		if _, ok := os.LookupEnv(name); !ok {
			missing = append(missing, name)
		}
	}

	sort.Strings(missing)
	return missing
}
