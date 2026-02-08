package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/strongdm/kilroy/internal/attractor/model"
	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

func TestFanIn_AllStatusFail_MixedClasses_AggregatesDeterministic(t *testing.T) {
	out := runFanInAllFail(t, []parallelBranchResult{
		makeFailBranchResult("b1", failureClassTransientInfra, "temporary timeout"),
		makeFailBranchResult("b2", failureClassDeterministic, "provider contract mismatch"),
	})

	if out.Status != runtime.StatusFail {
		t.Fatalf("status: got %q want %q", out.Status, runtime.StatusFail)
	}
	assertFailureClassAndSignature(t, out, failureClassDeterministic)
}

func TestFanIn_AllStatusFail_AllTransient_AggregatesTransient(t *testing.T) {
	out := runFanInAllFail(t, []parallelBranchResult{
		makeFailBranchResult("b1", failureClassTransientInfra, "temporary timeout"),
		makeFailBranchResult("b2", failureClassTransientInfra, "connection reset"),
	})

	if out.Status != runtime.StatusFail {
		t.Fatalf("status: got %q want %q", out.Status, runtime.StatusFail)
	}
	assertFailureClassAndSignature(t, out, failureClassTransientInfra)
}

func TestFanIn_AllStatusFail_UnknownClass_AggregatesDeterministic(t *testing.T) {
	out := runFanInAllFail(t, []parallelBranchResult{
		{
			BranchKey: "b1",
			Outcome: runtime.Outcome{
				Status:        runtime.StatusFail,
				FailureReason: "unknown failure",
			},
		},
	})

	if out.Status != runtime.StatusFail {
		t.Fatalf("status: got %q want %q", out.Status, runtime.StatusFail)
	}
	assertFailureClassAndSignature(t, out, failureClassDeterministic)
}

func runFanInAllFail(t *testing.T, results []parallelBranchResult) runtime.Outcome {
	t.Helper()
	ctx := runtime.NewContext()
	ctx.Set("parallel.results", results)

	handler := &FanInHandler{}
	out, err := handler.Execute(context.Background(), &Execution{
		Context:     ctx,
		WorktreeDir: t.TempDir(),
	}, &model.Node{ID: "join"})
	if err != nil {
		t.Fatalf("FanInHandler.Execute: %v", err)
	}
	return out
}

func makeFailBranchResult(key, class, reason string) parallelBranchResult {
	return parallelBranchResult{
		BranchKey: key,
		Outcome: runtime.Outcome{
			Status:        runtime.StatusFail,
			FailureReason: reason,
			Meta: map[string]any{
				"failure_class": class,
			},
		},
	}
}

func assertFailureClassAndSignature(t *testing.T, out runtime.Outcome, wantClass string) {
	t.Helper()
	metaClass := strings.TrimSpace(anyToString(out.Meta["failure_class"]))
	if metaClass != wantClass {
		t.Fatalf("meta.failure_class: got %q want %q (meta=%v)", metaClass, wantClass, out.Meta)
	}
	metaSig := strings.TrimSpace(anyToString(out.Meta["failure_signature"]))
	if !strings.HasPrefix(metaSig, "parallel_all_failed|"+wantClass+"|") {
		t.Fatalf("meta.failure_signature: got %q", metaSig)
	}
	ctxClass := strings.TrimSpace(anyToString(out.ContextUpdates["failure_class"]))
	if ctxClass != wantClass {
		t.Fatalf("context failure_class: got %q want %q", ctxClass, wantClass)
	}
}
