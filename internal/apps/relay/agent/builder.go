package agent

import (
	"bytes"
	"context"
	_ "embed"
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/normahq/norma/internal/git"
	"github.com/normahq/norma/pkg/runtime/agentfactory"
	runtimeconfig "github.com/normahq/norma/pkg/runtime/appconfig"
	"go.uber.org/fx"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/session"
)

//go:embed system_instruction.gotmpl
var relaySystemInstructionTmpl string

const (
	workspaceBranchUnknown = "unknown"
	workspaceBranchNA      = "n/a"
)

type Builder struct {
	factory                *agentfactory.Factory
	normaCfg               runtimeconfig.NormaConfig
	workingDir             string
	workspaceEnabled       bool
	workspaceBaseBranch    string
	relayAgentInstructions map[string]string
}

type relayPromptData struct {
	SessionID         string
	WorkspaceDir      string
	WorkspaceEnabled  bool
	SessionBranch     string
	WorkspaceMode     string
	BaseBranch        string
	RepoBranchAtStart string
	AgentInstructions string
}

func (b *Builder) buildRelaySystemInstruction(
	sessionID,
	agentName,
	sessionBranch,
	workspaceDir,
	repoBranchAtStart string,
) string {
	normalizedAgentName := strings.TrimSpace(agentName)
	repoBranch := strings.TrimSpace(repoBranchAtStart)
	if !b.workspaceEnabled {
		repoBranch = workspaceBranchNA
	} else if repoBranch == "" {
		repoBranch = workspaceBranchUnknown
	}

	baseBranch := strings.TrimSpace(b.workspaceBaseBranch)
	if b.workspaceEnabled && baseBranch == "" {
		if repoBranch != "" && repoBranch != workspaceBranchUnknown {
			baseBranch = repoBranch
		} else {
			baseBranch = workspaceBranchUnknown
		}
	}
	if !b.workspaceEnabled && baseBranch == "" {
		baseBranch = workspaceBranchNA
	}

	workspaceMode := "direct"
	if b.workspaceEnabled {
		workspaceMode = "git-worktree"
	}

	data := relayPromptData{
		SessionID:         sessionID,
		WorkspaceDir:      workspaceDir,
		WorkspaceEnabled:  b.workspaceEnabled,
		SessionBranch:     sessionBranch,
		WorkspaceMode:     workspaceMode,
		BaseBranch:        baseBranch,
		RepoBranchAtStart: repoBranch,
	}

	normaInstruction := ""
	if agentCfg, ok := b.normaCfg.Agents[normalizedAgentName]; ok {
		normaInstruction = agentCfg.SystemInstruction
	}
	relayInstruction := b.relayAgentInstructions[normalizedAgentName]
	data.AgentInstructions = composeAgentInstructions(normaInstruction, relayInstruction)

	var buf bytes.Buffer
	tmpl := template.Must(template.New("relay").Parse(relaySystemInstructionTmpl))
	if err := tmpl.Execute(&buf, data); err != nil {
		return relaySystemInstructionTmpl
	}
	return buf.String()
}

type BuilderParams struct {
	fx.In

	Factory                *agentfactory.Factory
	NormaCfg               runtimeconfig.NormaConfig
	WorkingDir             string
	WorkspaceEnabled       bool              `name:"relay_workspace_enabled"`
	WorkspaceBaseBranch    string            `name:"relay_workspace_base_branch"`
	RelayAgentInstructions map[string]string `name:"relay_agent_system_instructions"`
}

// NewBuilder creates a Builder with the given factory and config.
func NewBuilder(params BuilderParams) *Builder {
	return &Builder{
		factory:                params.Factory,
		normaCfg:               params.NormaCfg,
		workingDir:             strings.TrimSpace(params.WorkingDir),
		workspaceEnabled:       params.WorkspaceEnabled,
		workspaceBaseBranch:    strings.TrimSpace(params.WorkspaceBaseBranch),
		relayAgentInstructions: normalizeRelayAgentInstructions(params.RelayAgentInstructions),
	}
}

// ValidateAgent checks if an agent with the given name can be created.
// It returns an error if the agent is not found or its type is unsupported.
func (b *Builder) ValidateAgent(agentName string) error {
	return b.factory.ValidateAgent(agentName)
}

type BuiltAgent struct {
	Agent      agent.Agent
	Runner     *runner.Runner
	SessionSvc session.Service
	Session    session.Session
}

func (b *Builder) Build(ctx context.Context, sessionID string, chatID int64, topicID int, agentName, workspaceDir string) (*BuiltAgent, error) {
	return b.BuildWithMCPServerIDs(ctx, sessionID, chatID, topicID, agentName, workspaceDir, nil)
}

func (b *Builder) BuildWithMCPServerIDs(
	ctx context.Context,
	sessionID string,
	chatID int64,
	topicID int,
	agentName, workspaceDir string,
	extraMCPServerIDs []string,
) (*BuiltAgent, error) {
	sessionBranch := fmt.Sprintf("norma/relay/%s", sessionID)
	repoBranchAtStart := b.currentRepoBranch(ctx)
	req := agentfactory.BuildRequest{
		AgentID:           agentName,
		Name:              agentName,
		Description:       b.buildAgentDescription(agentName),
		WorkingDirectory:  workspaceDir,
		SystemInstruction: b.buildRelaySystemInstruction(sessionID, agentName, sessionBranch, workspaceDir, repoBranchAtStart),
		MCPServerIDs:      b.buildAgentMCPServerIDs(agentName, extraMCPServerIDs),
	}

	ag, err := b.factory.Build(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("creating agent %q: %w", agentName, err)
	}

	sessionSvc := session.InMemoryService()
	sess, err := sessionSvc.Create(ctx, &session.CreateRequest{
		AppName: fmt.Sprintf("norma-relay-topic-%d", topicID),
		UserID:  sessionID,
	})
	if err != nil {
		if closer, ok := ag.(io.Closer); ok {
			_ = closer.Close()
		}
		return nil, fmt.Errorf("creating session: %w", err)
	}

	r, err := runner.New(runner.Config{
		AppName:        fmt.Sprintf("norma-relay-topic-%d", topicID),
		Agent:          ag,
		SessionService: sessionSvc,
	})
	if err != nil {
		if closer, ok := ag.(io.Closer); ok {
			_ = closer.Close()
		}
		return nil, fmt.Errorf("creating runner: %w", err)
	}

	return &BuiltAgent{
		Agent:      ag,
		Runner:     r,
		SessionSvc: sessionSvc,
		Session:    sess.Session,
	}, nil
}

func (b *Builder) currentRepoBranch(ctx context.Context) string {
	if !b.workspaceEnabled {
		return workspaceBranchNA
	}
	if strings.TrimSpace(b.workingDir) == "" {
		return workspaceBranchUnknown
	}
	branch, err := git.CurrentBranch(ctx, b.workingDir)
	if err != nil {
		return workspaceBranchUnknown
	}
	trimmed := strings.TrimSpace(branch)
	if trimmed == "" {
		return workspaceBranchUnknown
	}
	return trimmed
}

// buildAgentDescription returns a human-readable description of the agent.
func (b *Builder) buildAgentDescription(agentName string) string {
	agentCfg, ok := b.normaCfg.Agents[agentName]
	if !ok {
		return agentName
	}
	return agentCfg.Description(agentName)
}

// GetAgentInfo returns the description and list of MCP server names for an agent.
func (b *Builder) GetAgentInfo(agentName string) (description string, mcpServers []string) {
	agentCfg, ok := b.normaCfg.Agents[agentName]
	if !ok {
		return agentName, bundledMCPServerIDs(b.workspaceEnabled)
	}
	return agentCfg.Description(agentName), mergeMCPServerIDs(agentCfg.MCPServers, nil, b.workspaceEnabled)
}

func (b *Builder) buildAgentMCPServerIDs(agentName string, extra []string) []string {
	agentCfg, ok := b.normaCfg.Agents[agentName]
	if !ok {
		return mergeMCPServerIDs(nil, extra, b.workspaceEnabled)
	}
	return mergeMCPServerIDs(agentCfg.MCPServers, extra, b.workspaceEnabled)
}

func bundledMCPServerIDs(workspaceEnabled bool) []string {
	return []string{"relay"}
}

func mergeMCPServerIDs(explicit, extra []string, workspaceEnabled bool) []string {
	base := bundledMCPServerIDs(workspaceEnabled)
	out := make([]string, 0, len(base)+len(explicit)+len(extra))
	seen := make(map[string]struct{}, len(base)+len(explicit)+len(extra))

	appendUnique := func(id string) {
		trimmed := strings.TrimSpace(id)
		if trimmed == "" {
			return
		}
		if _, ok := seen[trimmed]; ok {
			return
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}

	for _, id := range base {
		appendUnique(id)
	}
	for _, id := range explicit {
		appendUnique(id)
	}
	for _, id := range extra {
		appendUnique(id)
	}

	return out
}

func composeAgentInstructions(normaInstruction, relayInstruction string) string {
	parts := make([]string, 0, 2)
	if trimmedNorma := strings.TrimSpace(normaInstruction); trimmedNorma != "" {
		parts = append(parts, trimmedNorma)
	}
	if trimmedRelay := strings.TrimSpace(relayInstruction); trimmedRelay != "" {
		parts = append(parts, trimmedRelay)
	}
	return strings.Join(parts, "\n\n")
}

func normalizeRelayAgentInstructions(raw map[string]string) map[string]string {
	if len(raw) == 0 {
		return nil
	}

	normalized := make(map[string]string, len(raw))
	for key, value := range raw {
		trimmedKey := strings.TrimSpace(key)
		trimmedValue := strings.TrimSpace(value)
		if trimmedKey == "" || trimmedValue == "" {
			continue
		}
		normalized[trimmedKey] = trimmedValue
	}

	if len(normalized) == 0 {
		return nil
	}
	return normalized
}
