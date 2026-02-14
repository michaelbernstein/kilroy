// Package version holds the Kilroy release version.
//
// Version is set at build time by goreleaser via ldflags.
// For local builds without ldflags, it defaults to "dev".
package version

// Version is the current Kilroy release version.
// Override at build time: go build -ldflags "-X github.com/danshapiro/kilroy/internal/version.Version=1.2.3"
var Version = "dev"
