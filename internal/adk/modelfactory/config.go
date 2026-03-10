package modelfactory

import "github.com/metalagman/norma/internal/adk/agentconfig"

// ModelConfig describes how to run a model.
type ModelConfig = agentconfig.Config

// FactoryConfig is a map of model configurations.
type FactoryConfig map[string]ModelConfig

const (
	// ModelTypeGeminiAIStudio is the type for Gemini AI Studio models.
	ModelTypeGeminiAIStudio = agentconfig.AgentTypeGeminiAIStudio
	// ModelTypeExec is the type for executive models.
	ModelTypeExec = agentconfig.AgentTypeExec
	// ModelTypeACPExec is the type for custom ACP CLI executables.
	ModelTypeACPExec = agentconfig.AgentTypeACPExec

	// ModelTypeGemini is the alias for gemini CLI.
	ModelTypeGemini = agentconfig.AgentTypeGemini
	// ModelTypeGeminiACP is the alias for Gemini CLI ACP mode.
	ModelTypeGeminiACP = agentconfig.AgentTypeGeminiACP
	// ModelTypeClaude is the alias for claude CLI.
	ModelTypeClaude = agentconfig.AgentTypeClaude
	// ModelTypeCodex is the alias for codex CLI.
	ModelTypeCodex = agentconfig.AgentTypeCodex
	// ModelTypeCodexACP is the alias for Codex ACP bridge mode.
	ModelTypeCodexACP = agentconfig.AgentTypeCodexACP
	// ModelTypeOpenCode is the alias for opencode CLI.
	ModelTypeOpenCode = agentconfig.AgentTypeOpenCode
	// ModelTypeOpenCodeACP is the alias for OpenCode CLI ACP mode.
	ModelTypeOpenCodeACP = agentconfig.AgentTypeOpenCodeACP
)
