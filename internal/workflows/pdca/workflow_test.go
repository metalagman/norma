package pdca

import (
	"errors"
	"iter"
	"testing"

	"github.com/metalagman/norma/internal/workflows/pdca/models"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/session"
)

type stubState struct {
	values map[string]any
	errs   map[string]error
}

func (s stubState) Get(key string) (any, error) {
	if err, ok := s.errs[key]; ok {
		return nil, err
	}
	v, ok := s.values[key]
	if !ok {
		return nil, session.ErrStateKeyNotExist
	}
	return v, nil
}

func (s stubState) Set(string, any) error {
	return nil
}

func (s stubState) All() iter.Seq2[string, any] {
	return func(yield func(string, any) bool) {
		for k, v := range s.values {
			if !yield(k, v) {
				return
			}
		}
	}
}

func TestParseFinalState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		state         stubState
		wantVerdict   string
		wantDecision  string
		wantIteration int
		wantErr       bool
	}{
		{
			name: "ok values",
			state: stubState{
				values: map[string]any{
					"verdict":   "PASS",
					"decision":  "close",
					"iteration": 3,
				},
			},
			wantVerdict:   "PASS",
			wantDecision:  "close",
			wantIteration: 3,
		},
		{
			name: "missing verdict and decision are allowed",
			state: stubState{
				values: map[string]any{
					"iteration": 2,
				},
			},
			wantVerdict:   "",
			wantDecision:  "",
			wantIteration: 2,
		},
		{
			name: "missing iteration uses default",
			state: stubState{
				values: map[string]any{
					"verdict":  "FAIL",
					"decision": "rollback",
				},
			},
			wantVerdict:   "FAIL",
			wantDecision:  "rollback",
			wantIteration: 1,
		},
		{
			name: "invalid verdict type",
			state: stubState{
				values: map[string]any{
					"verdict":   true,
					"iteration": 1,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid decision type",
			state: stubState{
				values: map[string]any{
					"verdict":   "PASS",
					"decision":  true,
					"iteration": 1,
				},
			},
			wantErr: true,
		},
		{
			name: "invalid iteration type",
			state: stubState{
				values: map[string]any{
					"verdict":   "PASS",
					"iteration": "2",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid iteration value",
			state: stubState{
				values: map[string]any{
					"verdict":   "PASS",
					"iteration": 0,
				},
			},
			wantErr: true,
		},
		{
			name: "iteration read error",
			state: stubState{
				values: map[string]any{
					"verdict": "PASS",
				},
				errs: map[string]error{
					"iteration": errors.New("storage failure"),
				},
			},
			wantErr: true,
		},
		{
			name: "fallback to task_state values",
			state: stubState{
				values: map[string]any{
					"iteration": 5,
					"task_state": &models.TaskState{
						Check: &models.CheckOutput{
							Verdict: &models.CheckVerdict{
								Status: "PASS",
							},
						},
						Act: &models.ActOutput{
							Decision: "close",
						},
					},
				},
			},
			wantVerdict:   "PASS",
			wantDecision:  "close",
			wantIteration: 5,
		},
		{
			name: "direct state takes precedence over task_state fallback",
			state: stubState{
				values: map[string]any{
					"verdict":   "FAIL",
					"decision":  "rollback",
					"iteration": 6,
					"task_state": &models.TaskState{
						Check: &models.CheckOutput{
							Verdict: &models.CheckVerdict{
								Status: "PASS",
							},
						},
						Act: &models.ActOutput{
							Decision: "close",
						},
					},
				},
			},
			wantVerdict:   "FAIL",
			wantDecision:  "rollback",
			wantIteration: 6,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotVerdict, gotDecision, gotIteration, err := parseFinalState(tc.state)
			if tc.wantErr {
				if err == nil {
					t.Fatal("parseFinalState() error = nil, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFinalState() unexpected error: %v", err)
			}
			if gotVerdict != tc.wantVerdict {
				t.Fatalf("verdict = %q, want %q", gotVerdict, tc.wantVerdict)
			}
			if gotDecision != tc.wantDecision {
				t.Fatalf("decision = %q, want %q", gotDecision, tc.wantDecision)
			}
			if gotIteration != tc.wantIteration {
				t.Fatalf("iteration = %d, want %d", gotIteration, tc.wantIteration)
			}
		})
	}
}

func TestDeriveFinalOutcome(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		verdict            string
		decision           string
		wantStatus         string
		wantEffectiveState string
	}{
		{
			name:               "pass verdict",
			verdict:            "PASS",
			decision:           "continue",
			wantStatus:         "passed",
			wantEffectiveState: "PASS",
		},
		{
			name:               "fail verdict",
			verdict:            "FAIL",
			decision:           "close",
			wantStatus:         "failed",
			wantEffectiveState: "FAIL",
		},
		{
			name:               "close decision with missing verdict",
			verdict:            "",
			decision:           "close",
			wantStatus:         "passed",
			wantEffectiveState: "PASS",
		},
		{
			name:               "non-close decision with missing verdict",
			verdict:            "",
			decision:           "replan",
			wantStatus:         "stopped",
			wantEffectiveState: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotStatus, gotEffectiveVerdict := deriveFinalOutcome(tc.verdict, tc.decision)
			if gotStatus != tc.wantStatus {
				t.Fatalf("status = %q, want %q", gotStatus, tc.wantStatus)
			}
			if gotEffectiveVerdict != tc.wantEffectiveState {
				t.Fatalf("effectiveVerdict = %q, want %q", gotEffectiveVerdict, tc.wantEffectiveState)
			}
		})
	}
}

func TestNewLoopAgentUsesOnlyOrchestratorSubAgent(t *testing.T) {
	t.Parallel()

	orchestrator, err := agent.New(agent.Config{
		Name:        "PDCAAgent",
		Description: "test orchestrator",
		Run: func(agent.InvocationContext) iter.Seq2[*session.Event, error] {
			return func(func(*session.Event, error) bool) {}
		},
	})
	if err != nil {
		t.Fatalf("create orchestrator: %v", err)
	}

	loop, err := newLoopAgent(3, orchestrator)
	if err != nil {
		t.Fatalf("newLoopAgent() error = %v", err)
	}

	subAgents := loop.SubAgents()
	if len(subAgents) != 1 {
		t.Fatalf("len(loop.SubAgents()) = %d, want 1", len(subAgents))
	}
	if subAgents[0].Name() != orchestrator.Name() {
		t.Fatalf("loop sub-agent = %q, want %q", subAgents[0].Name(), orchestrator.Name())
	}
}
