package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/strongdm/kilroy/internal/attractor/model"
	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

func TestLoopRestart_FailsFastOnEmptyBaseLogsRoot(t *testing.T) {
	g := model.NewGraph("restart-guardrail")
	g.Attrs["max_restarts"] = "1"

	e := &Engine{
		Graph:        g,
		baseLogsRoot: "",
	}

	_, err := e.loopRestart(context.Background(), "next", "check", runtime.Outcome{Status: runtime.StatusFail, FailureReason: "timeout"}, failureClassTransientInfra)
	if err == nil {
		t.Fatalf("expected loopRestart error, got nil")
	}
	if !strings.Contains(err.Error(), "base logs root is empty") {
		t.Fatalf("error = %q, want base logs root guardrail", err.Error())
	}
}
