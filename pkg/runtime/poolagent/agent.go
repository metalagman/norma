package poolagent

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/normahq/norma/pkg/runtime/agentconfig"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
	"iter"
)

type MemberConfig struct {
	Name string
	Cfg  agentconfig.Config
}

type AgentCreator interface {
	CreateAgent(ctx context.Context, name string, req AgentRequest) (agent.Agent, error)
}

type AgentRequest struct {
	Name               string
	Description        string
	SystemInstructions string
	WorkingDirectory   string
}

type PoolExecutor struct {
	poolName     string
	members      []MemberConfig
	agentCreator AgentCreator
	req          AgentRequest
	mu           sync.Mutex
	cachedAgent  agent.Agent
}

func NewPoolExecutor(poolName string, members []MemberConfig, agentCreator AgentCreator, req AgentRequest) *PoolExecutor {
	return &PoolExecutor{
		poolName:     poolName,
		members:      members,
		agentCreator: agentCreator,
		req:          req,
	}
}

func (p *PoolExecutor) Agent(ctx context.Context) (agent.Agent, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cachedAgent != nil {
		return p.cachedAgent, nil
	}

	var lastErr error
	attemptErrors := make([]AttemptError, 0, len(p.members))

	for i, member := range p.members {
		req := p.req
		req.Name = p.poolName + "_" + member.Name

		inner, err := p.agentCreator.CreateAgent(ctx, member.Name, req)
		if err != nil {
			errMsg := fmt.Sprintf("create agent %q: %v", member.Name, err)
			attemptErrors = append(attemptErrors, AttemptError{
				Member: member.Name,
				Index:  i,
				Err:    errMsg,
			})
			lastErr = fmt.Errorf("pool %q: all members failed", p.poolName)
			continue
		}

		p.cachedAgent = inner
		return inner, nil
	}

	return nil, &AllPoolMembersFailedError{
		PoolName:    p.poolName,
		MemberNames: p.memberNames(),
		Errors:      attemptErrors,
		Err:         lastErr,
	}
}

func (p *PoolExecutor) memberNames() string {
	names := make([]string, len(p.members))
	for i, m := range p.members {
		names[i] = m.Name
	}
	return strings.Join(names, ", ")
}

func (p *PoolExecutor) Close() error {
	if p.cachedAgent != nil {
		if closer, ok := p.cachedAgent.(interface{ Close() error }); ok {
			return closer.Close()
		}
	}
	return nil
}

type AttemptError struct {
	Member string
	Index  int
	Err    string
}

type AllPoolMembersFailedError struct {
	PoolName    string
	MemberNames string
	Errors      []AttemptError
	Err         error
}

func (e *AllPoolMembersFailedError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "pool %q: all %d members failed\n", e.PoolName, len(e.Errors))
	for _, ae := range e.Errors {
		fmt.Fprintf(&b, "  [%d] %s: %s\n", ae.Index+1, ae.Member, ae.Err)
	}
	return b.String()
}

func (e *AllPoolMembersFailedError) Unwrap() error {
	return e.Err
}

type PoolAgent struct {
	agent.Agent
	executor *PoolExecutor
}

func NewPoolAgent(ctx context.Context, poolName string, members []MemberConfig, req AgentRequest, agentCreator AgentCreator) (*PoolAgent, error) {
	executor := NewPoolExecutor(poolName, members, agentCreator, req)

	_, err := executor.Agent(ctx)
	if err != nil {
		return nil, err
	}

	poolAgent := &PoolAgent{
		executor: executor,
	}

	base, err := agent.New(agent.Config{
		Name:        poolName,
		Description: fmt.Sprintf("Pool agent with %d members: %s", len(members), executor.memberNames()),
		Run:         poolAgent.run,
		SubAgents:   nil,
	})
	if err != nil {
		_ = executor.Close()
		return nil, fmt.Errorf("create adk pool agent: %w", err)
	}
	poolAgent.Agent = base
	return poolAgent, nil
}

func (p *PoolAgent) run(ctx agent.InvocationContext) iter.Seq2[*session.Event, error] {
	return func(yield func(*session.Event, error) bool) {
		currentAgent, err := p.executor.Agent(ctx)
		if err != nil {
			yield(nil, err)
			return
		}

		for done := false; !done; {
			run := currentAgent.Run(ctx)
			retryAgent := false
			for ev, err := range run {
				if err != nil {
					p.executor.cachedAgent = nil

					var retryErr error
					currentAgent, retryErr = p.executor.Agent(ctx)
					if retryErr != nil {
						yield(nil, retryErr)
						return
					}
					retryAgent = true
					break
				}
				if !yield(ev, nil) {
					done = true
					break
				}
			}
			if retryAgent {
				continue
			}
			return
		}
	}
}

func (p *PoolAgent) Close() error {
	return p.executor.Close()
}

var _ agent.Agent = (*PoolAgent)(nil)
