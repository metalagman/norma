package models

// Role defines the interface for a workflow step implementation.
type Role interface {
	Name() string
	InputSchema() string
	OutputSchema() string
	Prompt(req AgentRequest) (string, error)
	MapRequest(req AgentRequest) (any, error)
	MapResponse(outBytes []byte) (AgentResponse, error)
}
