package agent

import (
	"context"
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
			want:             []string{"relay"},
		},
		{
			name:             "workspace_enabled",
			workspaceEnabled: true,
			want:             []string{"relay"},
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
	explicit := []string{" custom.one ", "relay", "", "custom.one", "custom.two"}
	extra := []string{"relay.extra", "custom.two", " "}
	got := mergeMCPServerIDs(explicit, extra, true)
	want := []string{"relay", "custom.one", "custom.two", "relay.extra"}
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

	got := builder.buildRelaySystemInstruction(
		"tg-1-2",
		"telegram",
		"alpha",
		"norma/relay/tg-1-2",
		"/tmp/work",
		"main",
	)

	wantSnippet := "Agent-specific instructions:\nnorma instruction\n\nrelay instruction"
	if !strings.Contains(got, wantSnippet) {
		t.Fatalf("buildRelaySystemInstruction() missing snippet %q in output:\n%s", wantSnippet, got)
	}
}

func TestBuildRelaySystemInstruction_OmitsAgentSpecificSectionWhenEmpty(t *testing.T) {
	t.Parallel()

	builder := &Builder{}
	got := builder.buildRelaySystemInstruction(
		"tg-1-2",
		"telegram",
		"alpha",
		"norma/relay/tg-1-2",
		"/tmp/work",
		"main",
	)

	if strings.Contains(got, "Agent-specific instructions:") {
		t.Fatalf("buildRelaySystemInstruction() unexpectedly contained agent instructions block:\n%s", got)
	}
}

func TestBuildRelaySystemInstruction_IncludesGitWorkspaceContext(t *testing.T) {
	t.Parallel()

	builder := &Builder{
		workspaceEnabled:    true,
		workspaceBaseBranch: "main",
		workingDir:          "/repo",
	}

	got := builder.buildRelaySystemInstruction(
		"tg-1-2",
		"telegram",
		"alpha",
		"norma/relay/tg-1-2",
		"/tmp/work",
		"develop",
	)

	wantSnippets := []string{
		"Workspace settings:",
		"This session belongs to channel type: telegram.",
		"Mode: git-worktree",
		"Path: /tmp/work",
		"Config path: /repo/.config/relay/config.yaml",
		"Base branch: main",
		"Session branch: norma/relay/tg-1-2",
		"Main repo branch at start: develop",
		"Git workspace guidance:",
	}
	for _, snippet := range wantSnippets {
		if !strings.Contains(got, snippet) {
			t.Fatalf("buildRelaySystemInstruction() missing snippet %q in output:\n%s", snippet, got)
		}
	}
}

func TestBuildRelaySystemInstruction_IncludesDirectModeSettingsWhenWorkspaceDisabled(t *testing.T) {
	t.Parallel()

	builder := &Builder{workspaceEnabled: false, workingDir: "/repo"}
	got := builder.buildRelaySystemInstruction(
		"tg-1-2",
		"telegram",
		"alpha",
		"norma/relay/tg-1-2",
		"/tmp/work",
		"main",
	)

	wantSnippets := []string{
		"Workspace settings:",
		"This session belongs to channel type: telegram.",
		"Mode: direct",
		"Path: /tmp/work",
		"Config path: /repo/.config/relay/config.yaml",
		"Base branch: n/a",
		"Git workspace tooling: disabled",
	}
	for _, snippet := range wantSnippets {
		if !strings.Contains(got, snippet) {
			t.Fatalf("buildRelaySystemInstruction() missing snippet %q in output:\n%s", snippet, got)
		}
	}

	if strings.Contains(got, "Git workspace guidance:") {
		t.Fatalf("buildRelaySystemInstruction() unexpectedly included git guidance in direct mode:\n%s", got)
	}
	if strings.Contains(got, "Available namespaces:") {
		t.Fatalf("buildRelaySystemInstruction() unexpectedly included generic MCP namespace docs:\n%s", got)
	}
}

func TestCurrentRepoBranch_Fallbacks(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		builder Builder
		want    string
	}{
		{
			name:    "workspace_disabled",
			builder: Builder{workspaceEnabled: false},
			want:    "n/a",
		},
		{
			name:    "missing_working_dir",
			builder: Builder{workspaceEnabled: true},
			want:    "unknown",
		},
		{
			name:    "non_git_working_dir",
			builder: Builder{workspaceEnabled: true, workingDir: t.TempDir()},
			want:    "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.builder.currentRepoBranch(context.Background()); got != tt.want {
				t.Fatalf("currentRepoBranch() = %q, want %q", got, tt.want)
			}
		})
	}
}
