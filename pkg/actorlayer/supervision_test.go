package actorlayer

import (
	"errors"
	"testing"
	"time"
)

func TestThresholdSupervisorEscalatesAfterMaxRestarts(t *testing.T) {
	t.Parallel()

	sup := NewThresholdSupervisor(ThresholdSupervisorConfig{
		Base:        DefaultSupervisor{},
		MaxRestarts: 2,
		Window:      time.Minute,
		OnExceeded:  Escalate,
	})
	ref := Ref{id: "a1"}

	d1 := sup.Decide(SupervisionContext{}, Failure{
		Actor: ref,
		Panic: "boom",
		At:    time.Unix(1_000, 0),
	})
	d2 := sup.Decide(SupervisionContext{}, Failure{
		Actor: ref,
		Panic: "boom",
		At:    time.Unix(1_001, 0),
	})
	d3 := sup.Decide(SupervisionContext{}, Failure{
		Actor: ref,
		Panic: "boom",
		At:    time.Unix(1_002, 0),
	})

	if d1 != Restart {
		t.Fatalf("first directive = %v, want %v", d1, Restart)
	}
	if d2 != Restart {
		t.Fatalf("second directive = %v, want %v", d2, Restart)
	}
	if d3 != Escalate {
		t.Fatalf("third directive = %v, want %v", d3, Escalate)
	}
}

func TestThresholdSupervisorWindowResetsRestartCount(t *testing.T) {
	t.Parallel()

	sup := NewThresholdSupervisor(ThresholdSupervisorConfig{
		Base:        DefaultSupervisor{},
		MaxRestarts: 1,
		Window:      100 * time.Millisecond,
		OnExceeded:  Escalate,
	})
	ref := Ref{id: "a2"}

	d1 := sup.Decide(SupervisionContext{}, Failure{
		Actor: ref,
		Panic: "boom",
		At:    time.Unix(2_000, 0),
	})
	d2 := sup.Decide(SupervisionContext{}, Failure{
		Actor: ref,
		Panic: "boom",
		At:    time.Unix(2_001, 0), // outside the window
	})

	if d1 != Restart {
		t.Fatalf("first directive = %v, want %v", d1, Restart)
	}
	if d2 != Restart {
		t.Fatalf("second directive = %v, want %v", d2, Restart)
	}
}

func TestThresholdSupervisorOnlyTracksRestartDirective(t *testing.T) {
	t.Parallel()

	sup := NewThresholdSupervisor(ThresholdSupervisorConfig{
		Base:        DefaultSupervisor{},
		MaxRestarts: 1,
		Window:      time.Minute,
		OnExceeded:  Escalate,
	})
	ref := Ref{id: "a3"}

	resume := sup.Decide(SupervisionContext{}, Failure{
		Actor: ref,
		Err:   errors.New("regular-error"),
		At:    time.Unix(3_000, 0),
	})
	restart := sup.Decide(SupervisionContext{}, Failure{
		Actor: ref,
		Panic: "boom",
		At:    time.Unix(3_001, 0),
	})
	restart2 := sup.Decide(SupervisionContext{}, Failure{
		Actor: ref,
		Panic: "boom",
		At:    time.Unix(3_002, 0),
	})

	if resume != Resume {
		t.Fatalf("resume directive = %v, want %v", resume, Resume)
	}
	if restart != Restart {
		t.Fatalf("first restart directive = %v, want %v", restart, Restart)
	}
	if restart2 != Escalate {
		t.Fatalf("second restart directive = %v, want %v", restart2, Escalate)
	}
}
