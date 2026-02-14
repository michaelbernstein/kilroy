package version

import "testing"

func TestVersionDefault(t *testing.T) {
	// When built without ldflags (i.e. go test), Version must be "dev".
	// goreleaser overrides this at build time via -X ldflags.
	if Version != "dev" {
		t.Fatalf("expected Version=%q in test builds, got %q", "dev", Version)
	}
}
