// Package appconfig loads and validates runtime application configuration.
//
// It resolves config files from norma-style search paths, expands environment
// variables, applies profile overlays, and validates the decoded runtime
// section with [ValidateSettings]. Use [LoadResolvedSettings] to inspect merged
// settings or [LoadConfigDocument] to decode an application struct directly.
package appconfig
