package agent

import (
	"reflect"
	"testing"
)

func TestBundledMCPServerIDs(t *testing.T) {
	tests := []struct {
		name             string
		workspaceEnabled bool
		want             []string
	}{
		{
			name:             "workspace_disabled",
			workspaceEnabled: false,
			want:             []string{"norma.config", "norma.state", "norma.relay"},
		},
		{
			name:             "workspace_enabled",
			workspaceEnabled: true,
			want:             []string{"norma.config", "norma.state", "norma.relay", "norma.workspace"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bundledMCPServerIDs(tt.workspaceEnabled); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("bundledMCPServerIDs(%v) = %#v, want %#v", tt.workspaceEnabled, got, tt.want)
			}
		})
	}
}

func TestMergeMCPServerIDs(t *testing.T) {
	explicit := []string{" custom.one ", "norma.state", "", "custom.one", "custom.two"}
	extra := []string{"relay.extra", "custom.two", " "}
	got := mergeMCPServerIDs(explicit, extra, true)
	want := []string{"norma.config", "norma.state", "norma.relay", "norma.workspace", "custom.one", "custom.two", "relay.extra"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mergeMCPServerIDs(%#v, %#v, true) = %#v, want %#v", explicit, extra, got, want)
	}
}
