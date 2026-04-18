package codexacp

import (
	"fmt"
	"strings"
)

var (
	validCodexApprovalPolicies = map[string]struct{}{
		"untrusted":  {},
		"on-failure": {},
		"on-request": {},
		"never":      {},
	}
	validCodexSandboxModes = map[string]struct{}{
		"read-only":          {},
		"workspace-write":    {},
		"danger-full-access": {},
	}
)

// Options configures Codex bridge backend -> ACP proxy behavior.
type Options struct {
	Name string

	CodexApprovalPolicy        string
	CodexBaseInstructions      string
	CodexCompactPrompt         string
	CodexConfig                map[string]any
	CodexDeveloperInstructions string
	CodexModel                 string
	CodexProfile               string
	CodexSandbox               string
}

type codexAppConfig struct {
	ApprovalPolicy        string
	BaseInstructions      string
	CompactPrompt         string
	Config                map[string]any
	DeveloperInstructions string
	Model                 string
	Profile               string
	Sandbox               string
}

func (c codexAppConfig) withModel(model string) codexAppConfig {
	next := c
	nextModel := strings.TrimSpace(model)
	if nextModel != "" {
		next.Model = nextModel
	}
	return next
}

func (o Options) appConfig() codexAppConfig {
	return codexAppConfig{
		ApprovalPolicy:        strings.TrimSpace(o.CodexApprovalPolicy),
		BaseInstructions:      strings.TrimSpace(o.CodexBaseInstructions),
		CompactPrompt:         strings.TrimSpace(o.CodexCompactPrompt),
		Config:                cloneMap(o.CodexConfig),
		DeveloperInstructions: strings.TrimSpace(o.CodexDeveloperInstructions),
		Model:                 strings.TrimSpace(o.CodexModel),
		Profile:               strings.TrimSpace(o.CodexProfile),
		Sandbox:               strings.TrimSpace(o.CodexSandbox),
	}
}

func (o Options) validate() error {
	if err := validateEnumValue("codex approval policy", o.CodexApprovalPolicy, validCodexApprovalPolicies); err != nil {
		return err
	}
	if err := validateEnumValue("codex sandbox", o.CodexSandbox, validCodexSandboxModes); err != nil {
		return err
	}
	return nil
}

func validateEnumValue(label string, value string, allowed map[string]struct{}) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if _, ok := allowed[trimmed]; ok {
		return nil
	}
	return fmt.Errorf("invalid %s %q", label, trimmed)
}

func cloneMap(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
