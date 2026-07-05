package providererror

import upstream "github.com/normahq/go-adk-acpagent/providererror"

const (
	// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
	WireKey = upstream.WireKey
	// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
	ADKMetadataKey = upstream.ADKMetadataKey
)

// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
type Kind = upstream.Kind

const (
	// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
	KindQuotaExceeded = upstream.KindQuotaExceeded
	// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
	KindAuthenticationRequired = upstream.KindAuthenticationRequired
	// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
	KindPaymentRequired = upstream.KindPaymentRequired
	// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
	KindRateLimited = upstream.KindRateLimited
	// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
	KindUnavailable = upstream.KindUnavailable
	// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
	KindInvalidRequest = upstream.KindInvalidRequest
	// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
	KindUnknown = upstream.KindUnknown
)

// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.
type ProviderError = upstream.ProviderError

// FromWireData extracts provider_error from JSON-RPC error data or ACP _meta.
//
// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.FromWireData.
func FromWireData(data any) (*ProviderError, bool) {
	return upstream.FromWireData(data)
}

// FromMetadata extracts provider_error from ACP metadata.
//
// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.FromMetadata.
func FromMetadata(meta map[string]any) (*ProviderError, bool) {
	return upstream.FromMetadata(meta)
}

// FromADKMetadata extracts a provider error already mapped to ADK metadata.
//
// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.FromADKMetadata.
func FromADKMetadata(meta map[string]any) (*ProviderError, bool) {
	return upstream.FromADKMetadata(meta)
}

// FromWireValue parses the provider_error object itself.
//
// Deprecated: use github.com/normahq/go-adk-acpagent/providererror.FromWireValue.
func FromWireValue(value any) (*ProviderError, bool) {
	return upstream.FromWireValue(value)
}
