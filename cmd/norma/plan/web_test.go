package plancmd

import (
	"testing"

	"github.com/normahq/norma/v2/internal/logging"
)

func TestEnsurePlanWebDebugLoggingEnablesDebug(t *testing.T) {
	if err := logging.Init(logging.WithLevel(logging.LevelInfo)); err != nil {
		t.Fatalf("logging.Init(info): %v", err)
	}

	if err := ensurePlanWebDebugLogging(); err != nil {
		t.Fatalf("ensurePlanWebDebugLogging(): %v", err)
	}

	if !logging.DebugEnabled() {
		t.Fatal("DebugEnabled() = false, want true")
	}
}

func TestEnsurePlanWebDebugLoggingPreservesTrace(t *testing.T) {
	if err := logging.Init(logging.WithLevel(logging.LevelTrace)); err != nil {
		t.Fatalf("logging.Init(trace): %v", err)
	}

	if err := ensurePlanWebDebugLogging(); err != nil {
		t.Fatalf("ensurePlanWebDebugLogging(): %v", err)
	}

	if !logging.TraceEnabled() {
		t.Fatal("TraceEnabled() = false, want true")
	}
}
