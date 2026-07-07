package pdca

import (
	"errors"
	"iter"
	"testing"

	"github.com/normahq/norma/v2/internal/agents/pdca/contracts"
	"google.golang.org/adk/v2/session"
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
					"verdict":   "pass",
					"decision":  "close",
					"iteration": 3,
				},
			},
			wantVerdict:   "pass",
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
					"verdict":  "fail",
					"decision": "continue",
				},
			},
			wantVerdict:   "fail",
			wantDecision:  "continue",
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
					"verdict":   "pass",
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
					"verdict":   "pass",
					"iteration": "2",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid iteration value",
			state: stubState{
				values: map[string]any{
					"verdict":   "pass",
					"iteration": 0,
				},
			},
			wantErr: true,
		},
		{
			name: "iteration read error",
			state: stubState{
				values: map[string]any{
					"verdict": "pass",
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
					"task_state": &contracts.TaskState{
						Check: []byte(`{"verdict":"pass"}`),
						Act:   []byte(`{"decision":"close"}`),
					},
				},
			},
			wantVerdict:   "pass",
			wantDecision:  "close",
			wantIteration: 5,
		},
		{
			name: "direct state takes precedence over task_state fallback",
			state: stubState{
				values: map[string]any{
					"verdict":   "fail",
					"decision":  "replan",
					"iteration": 6,
					"task_state": &contracts.TaskState{
						Check: []byte(`{"verdict":"pass"}`),
						Act:   []byte(`{"decision":"close"}`),
					},
				},
			},
			wantVerdict:   "fail",
			wantDecision:  "replan",
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
		wantDecision       string
	}{
		{
			name:               "pass verdict with close decision",
			verdict:            "pass",
			decision:           "close",
			wantStatus:         "passed",
			wantEffectiveState: "pass",
			wantDecision:       "close",
		},
		{
			name:               "mixed case protocol literals normalize to lowercase",
			verdict:            " PASS ",
			decision:           " Close ",
			wantStatus:         "passed",
			wantEffectiveState: "pass",
			wantDecision:       "close",
		},
		{
			name:               "pass verdict with continue decision stops",
			verdict:            "pass",
			decision:           "continue",
			wantStatus:         "stopped",
			wantEffectiveState: "pass",
			wantDecision:       "continue",
		},
		{
			name:               "pass verdict with replan decision stops",
			verdict:            "pass",
			decision:           "replan",
			wantStatus:         "stopped",
			wantEffectiveState: "pass",
			wantDecision:       "replan",
		},
		{
			name:               "fail verdict with close decision",
			verdict:            "fail",
			decision:           "close",
			wantStatus:         "failed",
			wantEffectiveState: "fail",
			wantDecision:       "close",
		},
		{
			name:               "close decision with missing verdict",
			verdict:            "",
			decision:           "close",
			wantStatus:         "stopped",
			wantEffectiveState: "",
			wantDecision:       "close",
		},
		{
			name:               "non-close decision with missing verdict",
			verdict:            "",
			decision:           "replan",
			wantStatus:         "stopped",
			wantEffectiveState: "",
			wantDecision:       "replan",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotStatus, gotEffectiveVerdict, gotDecision := deriveFinalOutcome(tc.verdict, tc.decision)
			if gotStatus != tc.wantStatus {
				t.Fatalf("status = %q, want %q", gotStatus, tc.wantStatus)
			}
			if gotEffectiveVerdict != tc.wantEffectiveState {
				t.Fatalf("effectiveVerdict = %q, want %q", gotEffectiveVerdict, tc.wantEffectiveState)
			}
			if gotDecision != tc.wantDecision {
				t.Fatalf("effectiveDecision = %q, want %q", gotDecision, tc.wantDecision)
			}
		})
	}
}

func TestDecisionPropagation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		state        stubState
		wantDecision string
		wantVerdict  string
		wantStatus   string
	}{
		{
			name: "decision propagates to outcome",
			state: stubState{
				values: map[string]any{
					"verdict":   "fail",
					"decision":  "replan",
					"iteration": 3,
				},
			},
			wantDecision: "replan",
			wantVerdict:  "fail",
			wantStatus:   "failed",
		},
		{
			name: "close decision without verdict stops",
			state: stubState{
				values: map[string]any{
					"decision":  "close",
					"iteration": 2,
				},
			},
			wantDecision: "close",
			wantVerdict:  "",
			wantStatus:   "stopped",
		},
		{
			name: "pass with continue decision stops",
			state: stubState{
				values: map[string]any{
					"verdict":   "pass",
					"decision":  "continue",
					"iteration": 1,
				},
			},
			wantDecision: "continue",
			wantVerdict:  "pass",
			wantStatus:   "stopped",
		},
		{
			name: "decision from task_state propagates to outcome",
			state: stubState{
				values: map[string]any{
					"iteration": 5,
					"task_state": &contracts.TaskState{
						Check: []byte(`{"verdict":"pass"}`),
						Act:   []byte(`{"decision":"close"}`),
					},
				},
			},
			wantDecision: "close",
			wantVerdict:  "pass",
			wantStatus:   "passed",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			verdict, decision, iteration, err := parseFinalState(tc.state)
			if err != nil {
				t.Fatalf("parseFinalState() unexpected error: %v", err)
			}

			status, effectiveVerdict, _ := deriveFinalOutcome(verdict, decision)

			if decision != tc.wantDecision {
				t.Fatalf("decision = %q, want %q", decision, tc.wantDecision)
			}
			if effectiveVerdict != tc.wantVerdict {
				t.Fatalf("verdict = %q, want %q", effectiveVerdict, tc.wantVerdict)
			}
			if status != tc.wantStatus {
				t.Fatalf("status = %q, want %q", status, tc.wantStatus)
			}
			_ = iteration
		})
	}
}
