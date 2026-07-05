package acpagent

import (
	"context"

	upstream "github.com/normahq/go-adk-acpagent"
)

// Deprecated: use github.com/normahq/go-adk-acpagent.
type InstructionProvider = upstream.InstructionProvider

// Deprecated: use github.com/normahq/go-adk-acpagent.
type Config = upstream.Config

// Deprecated: use github.com/normahq/go-adk-acpagent.
type Agent = upstream.Agent

// Deprecated: use github.com/normahq/go-adk-acpagent.
type PermissionHandler = upstream.PermissionHandler

// Deprecated: use github.com/normahq/go-adk-acpagent.
type ClientConfig = upstream.ClientConfig

// Deprecated: use github.com/normahq/go-adk-acpagent.
type ExtendedSessionNotification = upstream.ExtendedSessionNotification

// Deprecated: use github.com/normahq/go-adk-acpagent.
type Client = upstream.Client

// Deprecated: use github.com/normahq/go-adk-acpagent.
type PromptResult = upstream.PromptResult

// Deprecated: use github.com/normahq/go-adk-acpagent.
type MCPServerType = upstream.MCPServerType

// Deprecated: use github.com/normahq/go-adk-acpagent.
type MCPServerConfig = upstream.MCPServerConfig

const (
	// Deprecated: use github.com/normahq/go-adk-acpagent.
	SessionStateKey = upstream.SessionStateKey
	// Deprecated: use github.com/normahq/go-adk-acpagent.
	PlanStateKey = upstream.PlanStateKey
	// Deprecated: use github.com/normahq/go-adk-acpagent.
	CWDStateKey = upstream.CWDStateKey
	// Deprecated: use github.com/normahq/go-adk-acpagent.
	MCPServerTypeStdio = upstream.MCPServerTypeStdio
	// Deprecated: use github.com/normahq/go-adk-acpagent.
	MCPServerTypeHTTP = upstream.MCPServerTypeHTTP
	// Deprecated: use github.com/normahq/go-adk-acpagent.
	MCPServerTypeSSE = upstream.MCPServerTypeSSE
)

// Deprecated: use github.com/normahq/go-adk-acpagent.
var ErrPromptAlreadyActive = upstream.ErrPromptAlreadyActive

// New creates an ADK agent backed by an ACP client process.
//
// Deprecated: use github.com/normahq/go-adk-acpagent.New.
func New(cfg Config) (*Agent, error) {
	return upstream.New(cfg)
}

// NewClient starts an ACP subprocess and returns a protocol client over stdio.
//
// Deprecated: use github.com/normahq/go-adk-acpagent.NewClient.
func NewClient(ctx context.Context, cfg ClientConfig) (*Client, error) {
	return upstream.NewClient(ctx, cfg)
}
