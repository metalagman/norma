package normaloop

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"time"

	"github.com/normahq/norma/v2/internal/config"
	"github.com/normahq/norma/v2/internal/db"
	runpkg "github.com/normahq/norma/v2/internal/run"
	"github.com/normahq/norma/v2/internal/task"
	"github.com/rs/zerolog"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/workflowagents/loopagent"
)

const (
	statusDoing               = "doing"
	statusTodo                = "todo"
	statusPlanning            = "planning"
	statusChecking            = "checking"
	statusActing              = "acting"
	verdictPass               = "PASS"
	selectorBackoffStepKey    = "selector_backoff_step"
	failedTaskBackoffStepKey  = "failed_task_backoff_step"
	failedTaskBackoffUntilKey = "failed_task_backoff_until"
)

const maxLoopIterations uint = 1_000_000

type Config struct {
	Logger     zerolog.Logger
	Cfg        config.Config
	WorkingDir string
	Tracker    task.Tracker
	RunStore   runStatusStore
	Factory    runpkg.AgentFactory
	Policy     task.SelectionPolicy
}

type runStatusStore interface {
	RunStatus(ctx context.Context, runID string) (string, error)
	CreateRun(ctx context.Context, runID, goal, runDir string, iteration int) error
	UpdateRun(ctx context.Context, runID string, update db.Update, event *db.Event) error
	DB() *sql.DB
}

type loopRuntime struct {
	logger               zerolog.Logger
	cfg                  config.Config
	workingDir           string
	normaDir             string
	tracker              task.Tracker
	runStore             runStatusStore
	factory              runpkg.AgentFactory
	policy               task.SelectionPolicy
	overrideBackoffSteps []time.Duration
}

// New constructs the normaloop ADK loop agent runtime.
func New(cfg Config) (agent.Agent, error) {
	absWorkingDir, err := filepath.Abs(cfg.WorkingDir)
	if err != nil {
		return nil, fmt.Errorf("resolve absolute working dir: %w", err)
	}

	w := &loopRuntime{
		logger:     cfg.Logger.With().Str("component", "normaloop").Logger(),
		cfg:        cfg.Cfg,
		workingDir: absWorkingDir,
		normaDir:   filepath.Join(absWorkingDir, ".norma"),
		tracker:    cfg.Tracker,
		runStore:   cfg.RunStore,
		factory:    cfg.Factory,
		policy:     cfg.Policy,
	}

	iterationAgent, err := w.newIterationAgent()
	if err != nil {
		return nil, fmt.Errorf("create normaloop iteration agent: %w", err)
	}
	selectorAgent, err := w.newSelectorAgent()
	if err != nil {
		return nil, fmt.Errorf("create normaloop selector agent: %w", err)
	}
	loopAgent, err := w.newLoopAgent(selectorAgent, iterationAgent)
	if err != nil {
		return nil, fmt.Errorf("create normaloop loop agent: %w", err)
	}

	return loopAgent, nil
}

func (w *loopRuntime) newLoopAgent(selectorAgent, iterationAgent agent.Agent) (agent.Agent, error) {
	return loopagent.New(loopagent.Config{
		MaxIterations: maxLoopIterations,
		AgentConfig: agent.Config{
			Name:        "NormaLoopAgent",
			Description: "Reads ready tasks and runs PDCA workflow per selected task.",
			SubAgents:   []agent.Agent{selectorAgent, iterationAgent},
		},
	})
}
