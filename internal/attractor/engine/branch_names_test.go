package engine

import "testing"

func TestBuildRunBranch_UsesNormalizedPrefix(t *testing.T) {
	got := buildRunBranch("attractor/run/", "r1")
	if got != "attractor/run/r1" {
		t.Fatalf("buildRunBranch = %q, want %q", got, "attractor/run/r1")
	}
}

func TestBuildParallelBranch_UsesSiblingParallelNamespace(t *testing.T) {
	got := buildParallelBranch("attractor/run/", "run-123", "Par Node", "child/a")
	if got != "attractor/run/parallel/run-123/par-node/child-a" {
		t.Fatalf("buildParallelBranch = %q", got)
	}
}
