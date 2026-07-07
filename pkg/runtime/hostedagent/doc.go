// Package hostedagent adapts local API-backed models to the ADK agent
// interface.
//
// The package centers on [New], which wraps a [google.golang.org/adk/v2/model.LLM]
// into an ADK agent. Helper constructors such as [NewOpenAIModel] and
// [NewAIStudioModel] build model implementations for common hosted providers.
package hostedagent
