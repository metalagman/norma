package normaloop

import (
	"errors"
	"iter"
	"testing"

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
		wantIteration int
		wantErr       bool
	}{
		{
			name: "ok values",
			state: stubState{
				values: map[string]any{
					"verdict":   "PASS",
					"iteration": 3,
				},
			},
			wantVerdict:   "PASS",
			wantIteration: 3,
		},
		{
			name: "missing verdict is allowed",
			state: stubState{
				values: map[string]any{
					"iteration": 2,
				},
			},
			wantVerdict:   "",
			wantIteration: 2,
		},
		{
			name: "missing iteration uses default",
			state: stubState{
				values: map[string]any{
					"verdict": "FAIL",
				},
			},
			wantVerdict:   "FAIL",
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
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			gotVerdict, gotIteration, err := parseFinalState(tc.state)
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
			if gotIteration != tc.wantIteration {
				t.Fatalf("iteration = %d, want %d", gotIteration, tc.wantIteration)
			}
		})
	}
}
