package providererror

import (
	"testing"

	upstream "github.com/normahq/go-adk-acpagent/providererror"
)

func TestAliasesUseUpstreamValues(t *testing.T) {
	if WireKey != upstream.WireKey {
		t.Fatalf("WireKey = %q, want %q", WireKey, upstream.WireKey)
	}
	if ADKMetadataKey != upstream.ADKMetadataKey {
		t.Fatalf("ADKMetadataKey = %q, want %q", ADKMetadataKey, upstream.ADKMetadataKey)
	}
	err, ok := FromWireData(map[string]any{
		WireKey: map[string]any{"kind": string(KindQuotaExceeded)},
	})
	if !ok {
		t.Fatal("FromWireData() ok = false, want true")
	}
	if err.Kind != upstream.KindQuotaExceeded {
		t.Fatalf("Kind = %q, want %q", err.Kind, upstream.KindQuotaExceeded)
	}
}
