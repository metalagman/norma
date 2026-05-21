// Package agentconfig defines runtime provider and MCP configuration schemas.
//
// It provides validation for user-facing configuration documents and
// normalization helpers that turn schema objects into runtime-ready settings.
// Applications typically decode config into [Config], call [Config.Validate],
// and then convert it to [ResolvedConfig] with [NormalizeConfig].
package agentconfig
