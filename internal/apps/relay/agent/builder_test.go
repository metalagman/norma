package agent

import (
	"reflect"
	"strings"
	"testing"

	"github.com/normahq/norma/pkg/runtime/agentconfig"
	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
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

func TestComposeAgentInstructions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		normaInstruction string
		relayInstruction string
		want             string
	}{
		{
			name:             "none",
			normaInstruction: "",
			relayInstruction: "",
			want:             "",
		},
		{
			name:             "norma_only",
			normaInstruction: "norma",
			relayInstruction: "",
			want:             "norma",
		},
		{
			name:             "relay_only",
			normaInstruction: "",
			relayInstruction: "relay",
			want:             "relay",
		},
		{
			name:             "both_norma_then_relay",
			normaInstruction: "norma",
			relayInstruction: "relay",
			want:             "norma\n\nrelay",
		},
		{
			name:             "trimmed",
			normaInstruction: "  norma  ",
			relayInstruction: "  relay  ",
			want:             "norma\n\nrelay",
		},
		{
			name:             "whitespace_only",
			normaInstruction: "  \n\t",
			relayInstruction: " ",
			want:             "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := composeAgentInstructions(tt.normaInstruction, tt.relayInstruction)
			if got != tt.want {
				t.Fatalf("composeAgentInstructions(%q, %q) = %q, want %q", tt.normaInstruction, tt.relayInstruction, got, tt.want)
			}
		})
	}
}

func TestBuildRelaySystemInstruction_ComposesAgentInstructions(t *testing.T) {
	t.Parallel()

	builder := &Builder{
		normaCfg: runtimeconfig.NormaConfig{
			Agents: map[string]agentconfig.Config{
				"alpha": {
					SystemInstruction: "norma instruction",
				},
			},
		},
		relayAgentInstructions: map[string]string{
			"alpha": "relay instruction",
		},
	}

	got := builder.buildRelaySystemInstruction("relay-1-2", "alpha", "norma/relay/relay-1-2", "/tmp/work")

	wantSnippet := "Agent-specific instructions:\nnorma instruction\n\nrelay instruction"
	if !strings.Contains(got, wantSnippet) {
		t.Fatalf("buildRelaySystemInstruction() missing snippet %q in output:\n%s", wantSnippet, got)
	}
}

func TestBuildRelaySystemInstruction_OmitsAgentSpecificSectionWhenEmpty(t *testing.T) {
	t.Parallel()

	builder := &Builder{}
	got := builder.buildRelaySystemInstruction("relay-1-2", "alpha", "norma/relay/relay-1-2", "/tmp/work")

	if strings.Contains(got, "Agent-specific instructions:") {
		t.Fatalf("buildRelaySystemInstruction() unexpectedly contained agent instructions block:\n%s", got)
	}
}
