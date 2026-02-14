# GoReleaser + Homebrew Tap Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Add cross-platform binary distribution via goreleaser and Homebrew tap so users can `brew install` or download pre-built binaries instead of cloning and building from source.

**Architecture:** goreleaser builds platform binaries on tag push via GitHub Actions, creates a GitHub Release with archives, and pushes a Homebrew formula to a separate tap repo. Version is injected at build time via ldflags.

**Tech Stack:** goreleaser v2, GitHub Actions, Homebrew tap (`danshapiro/homebrew-kilroy`)

---

### Task 1: Rename Go module path

The module path in `go.mod` is `github.com/strongdm/kilroy` but the repo lives at `github.com/danshapiro/kilroy`. This must match for `go install` to work.

**Files:**
- Modify: all files containing `github.com/strongdm/kilroy` (go.mod, Go source, docs/plans)

**Step 1: Run the global replacement**

```bash
cd /home/user/code/kilroy/.worktrees/release-skill
# Rewrite Go source and go.mod (the actual module path references)
find . -type f \( -name '*.go' -o -name 'go.mod' \) \
  -not -path './.git/*' -not -path './.worktrees/*' \
  -exec sed -i 's|github\.com/strongdm/kilroy|github.com/danshapiro/kilroy|g' {} +
# Rewrite doc plan files (code snippets referencing the module path),
# but skip THIS plan file since it references both old and new paths
# in its verification steps.
find ./docs/plans -name '*.md' \
  -not -name '2026-02-13-goreleaser-homebrew-tap.md' \
  -exec sed -i 's|github\.com/strongdm/kilroy|github.com/danshapiro/kilroy|g' {} +
```

**Step 2: Verify it compiles**

Run: `go build -o ./kilroy ./cmd/kilroy`
Expected: exits 0, no errors

**Step 3: Verify version flag still works**

Run: `./kilroy --version`
Expected: `kilroy 0.0.0`

**Step 4: Run the full test suite**

First, capture the pre-existing test state BEFORE the rename to establish a baseline:

Run: `go test ./... 2>&1 | grep '^---' | sort > /tmp/kilroy-test-baseline-before.txt`

Then, after the rename, run the full suite and compare:

Run: `go test ./... 2>&1 | grep '^---' | sort > /tmp/kilroy-test-baseline-after.txt`
Run: `diff /tmp/kilroy-test-baseline-before.txt /tmp/kilroy-test-baseline-after.txt`

Expected: the diff is empty (same test results before and after). If new failures appear that were not in the baseline, the rename introduced a regression — investigate before continuing.

**Step 5: Verify no leftover references**

Run: `grep -r 'github\.com/strongdm/kilroy' --include='*.go' --include='*.md' --include='go.mod' . | grep -v '.git/' | grep -v '.worktrees/'`
Expected: no output

**Step 6: Commit**

```bash
git add -u
git commit -m "refactor: rename module path from strongdm/kilroy to danshapiro/kilroy

The Go module path must match the actual GitHub repo for go install to
work. Mechanical find-and-replace across all Go source, go.mod, and
doc plan files."
```

---

### Task 2: Switch version from const to var for ldflags injection

goreleaser injects the version at build time via `-X` ldflags. This requires a `var`, not a `const`.

**Files:**
- Modify: `internal/version/version.go`

**Step 1: Update version.go**

Replace the entire file with:

```go
// Package version holds the Kilroy release version.
//
// Version is set at build time by goreleaser via ldflags.
// For local builds without ldflags, it defaults to "dev".
package version

// Version is the current Kilroy release version.
// Override at build time: go build -ldflags "-X github.com/danshapiro/kilroy/internal/version.Version=1.2.3"
var Version = "dev"
```

**Step 2: Verify it compiles and prints "dev"**

Run: `go build -o ./kilroy ./cmd/kilroy && ./kilroy --version`
Expected: `kilroy dev`

**Step 3: Verify ldflags injection works**

Run: `go build -ldflags "-X github.com/danshapiro/kilroy/internal/version.Version=0.99.0" -o ./kilroy ./cmd/kilroy && ./kilroy --version`
Expected: `kilroy 0.99.0`

**Step 4: No commit yet** — bundle with Task 3.

---

### Task 3: Add a test for the Version variable default

The `Version` variable changes from `const "0.0.0"` to `var "dev"`. Add a test that the default is `"dev"` in test builds (goreleaser overrides it via ldflags at release time). This ensures the variable isn't accidentally changed to an empty string or wrong default.

**Files:**
- Create: `internal/version/version_test.go`

**Step 1: Write the test**

```go
package version

import "testing"

func TestVersionDefault(t *testing.T) {
	// When built without ldflags (i.e. go test), Version must be "dev".
	// goreleaser overrides this at build time via -X ldflags.
	if Version != "dev" {
		t.Fatalf("expected Version=%q in test builds, got %q", "dev", Version)
	}
}
```

**Step 2: Run the test**

Run: `go test ./internal/version/...`
Expected: PASS

**Step 3: No commit yet** — bundle with Task 4.

---

### Task 4: Create `.goreleaser.yaml`

**Files:**
- Create: `.goreleaser.yaml`

**Step 1: Write the goreleaser config**

```yaml
version: 2

project_name: kilroy

builds:
  - id: kilroy
    main: ./cmd/kilroy
    binary: kilroy
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
      - windows
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w
      - -X github.com/danshapiro/kilroy/internal/version.Version={{.Version}}
      - -X main.embeddedBuildRevision={{.FullCommit}}

archives:
  - id: default
    format: tar.gz
    format_overrides:
      - goos: windows
        format: zip
    name_template: "{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}"

checksum:
  name_template: checksums.txt

changelog:
  disable: true

# NOTE: `brews` is the current goreleaser v2 config key for Homebrew formulas.
# If goreleaser deprecates it in favor of a new key, it will warn at release
# time. Update the key name if that happens.
brews:
  - name: kilroy
    repository:
      owner: danshapiro
      name: homebrew-kilroy
      token: "{{ .Env.HOMEBREW_TAP_GITHUB_TOKEN }}"
    directory: Formula
    homepage: "https://github.com/danshapiro/kilroy"
    description: "Local-first CLI for running Attractor pipelines in a git repo"
    license: "MIT"
    install: |
      bin.install "kilroy"
    test: |
      system "#{bin}/kilroy", "--version"

release:
  github:
    owner: danshapiro
    name: kilroy
  draft: false
  prerelease: auto
```

**Step 2: Validate the config locally (if goreleaser is installed)**

Run:
```bash
if command -v goreleaser &>/dev/null; then
  goreleaser check
else
  echo "goreleaser not installed locally — config will be validated in CI by the 'Validate GoReleaser config' workflow step"
fi
```
Expected: `config is valid` if goreleaser is installed. If goreleaser IS installed and reports errors, fix the config before proceeding. If goreleaser is not installed locally, the GitHub Actions workflow includes a mandatory `goreleaser check` step that will catch any config errors before the release runs.

---

### Task 5: Create GitHub Actions release workflow

**Files:**
- Create: `.github/workflows/release.yml`

**Step 1: Create the directory**

```bash
mkdir -p .github/workflows
```

**Step 2: Write the workflow**

```yaml
name: Release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Set up Go
        uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - name: Run tests
        run: go test ./...

      - name: Validate GoReleaser config
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: "~> v2"
          args: check

      - name: Run GoReleaser
        uses: goreleaser/goreleaser-action@v6
        with:
          distribution: goreleaser
          version: "~> v2"
          args: release --clean --release-notes=RELEASE_NOTES.md
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          HOMEBREW_TAP_GITHUB_TOKEN: ${{ secrets.HOMEBREW_TAP_GITHUB_TOKEN }}
```

---

### Task 6: Add `/dist/` to `.gitignore`

**Files:**
- Modify: `.gitignore`

**Step 1: Append the dist exclusion**

Add to the end of `.gitignore`:

```
# GoReleaser build output
/dist/
```

**Step 2: Commit Tasks 2-6 together**

```bash
git add internal/version/version.go internal/version/version_test.go .goreleaser.yaml .github/workflows/release.yml .gitignore
git commit -m "feat(release): add goreleaser config, GitHub Actions workflow, and version injection

- internal/version/version.go: change const to var so goreleaser can
  inject the version from the git tag via ldflags. Default is 'dev'
  for local builds.
- internal/version/version_test.go: assert Version == 'dev' in test
  builds (goreleaser overrides via ldflags at release time).
- .goreleaser.yaml: cross-platform builds (linux/darwin/windows x
  amd64/arm64), Homebrew tap (danshapiro/homebrew-kilroy), changelog
  disabled (hand-crafted release notes passed via --release-notes flag
  in the GitHub Actions workflow).
- .github/workflows/release.yml: triggered on v* tag push, validates
  config, runs tests, then goreleaser. Uses HOMEBREW_TAP_GITHUB_TOKEN
  secret for tap push.
- .gitignore: exclude goreleaser dist/ output."
```

---

### Task 7: Update release skill for goreleaser workflow

**Files:**
- Modify: `skills/release-kilroy/SKILL.md`

**Step 1: Update the Version Number section**

Replace:
```
Kilroy uses semver. The version lives in `internal/version/version.go` as a `Version` constant. Decide the bump with the user, but offer a recommendation:
```

With:
```
Kilroy uses semver. The version is injected at build time by goreleaser from the git tag. The file `internal/version/version.go` has `var Version = "dev"` as the default for local builds — do NOT manually edit this for releases.

Decide the bump with the user, but offer a recommendation:
```

**Step 2: Update "Prepare the release" (step 5)**

Replace:
```
1. **Bump version** in `internal/version/version.go`
2. **Update README** with any approved changes
3. **Commit** with message like `release: vX.Y.Z`
```

With:
```
1. **Write release notes** to `RELEASE_NOTES.md` in the repo root (following the guidelines above). The GitHub Actions workflow passes `--release-notes=RELEASE_NOTES.md` to goreleaser, which publishes it as the GitHub release body. This file is committed (not gitignored) so it is present at the tagged commit.
2. **Update README** with any approved changes (version is injected by goreleaser from the tag — no file to bump)
3. **Commit** with message like `release: vX.Y.Z`
```

**Step 3: Update "Tag and publish" (step 7)**

Replace:
```
git push origin main
git tag -a vX.Y.Z -m "vX.Y.Z"
git push --tags
gh release create vX.Y.Z --title "vX.Y.Z" --notes "..."  # with the release notes
```

With:
```
git push origin main
git tag -a vX.Y.Z -m "vX.Y.Z"
git push origin vX.Y.Z
# GoReleaser takes over from here via GitHub Actions:
#   - Runs go test ./...
#   - Builds cross-platform binaries (linux/darwin/windows x amd64/arm64)
#   - Creates GitHub release with archives and checksums
#   - Updates Homebrew tap (danshapiro/homebrew-kilroy)
```

**Step 4: Add a "Verify the release" step after Tag and publish**

Insert as new step 8 (renumber old 8 to 9):

```
### 8. Verify the release

1. Watch GitHub Actions: https://github.com/danshapiro/kilroy/actions
2. Confirm the GitHub release has 6 platform archives + checksums.txt
3. Confirm the release notes appear on the GitHub release page
4. Test Homebrew install:
   ```bash
   brew install danshapiro/kilroy/kilroy
   kilroy --version  # should print the new version
   ```
```

**Step 5: Update the Safety section**

In the Safety section at the bottom of the skill, replace:
```
- Commit the version bump before tagging so the tag points to the right commit
```

With:
```
- Commit RELEASE_NOTES.md and any README changes before tagging so the tag points to the right commit
```

**Step 6: No commit yet** — bundle with Task 7.

---

### Task 8: Update README with Installation section

**Files:**
- Modify: `README.md`

**Step 1: Add Installation section after the high-level flow, before "What Is CXDB?"**

Insert after line 10 (`4. Resume interrupted runs...`):

~~~~markdown

## Installation

### Homebrew (macOS and Linux)

```bash
brew install danshapiro/kilroy/kilroy
```

### Go Install

```bash
go install github.com/danshapiro/kilroy/cmd/kilroy@latest
```

### Binary Download

Download the latest release from [GitHub Releases](https://github.com/danshapiro/kilroy/releases).

| Platform       | Archive                          |
|----------------|----------------------------------|
| macOS (Apple)  | `kilroy_*_darwin_arm64.tar.gz`   |
| macOS (Intel)  | `kilroy_*_darwin_amd64.tar.gz`   |
| Linux (x86_64) | `kilroy_*_linux_amd64.tar.gz`    |
| Linux (ARM64)  | `kilroy_*_linux_arm64.tar.gz`    |
| Windows        | `kilroy_*_windows_amd64.zip`     |
| Windows (ARM)  | `kilroy_*_windows_arm64.zip`     |

### Build from Source

```bash
go build -o kilroy ./cmd/kilroy
```
~~~~

**Step 2: Commit Tasks 7-8 together**

```bash
git add skills/release-kilroy/SKILL.md README.md
git commit -m "docs: update release skill and README for goreleaser-based releases

- Release skill: version is now injected by goreleaser from the git tag
  (no manual version.go bump). Tag push triggers GitHub Actions which
  builds binaries, creates release, and updates Homebrew tap. Added
  'Verify the release' step.
- README: added Installation section with Homebrew, go install, binary
  download, and build-from-source options."
```

---

### Post-implementation: Manual setup (not automated)

Before the first release tag is pushed, the user must:

1. Create repo `danshapiro/homebrew-kilroy` on GitHub (public, with a README)
2. Create a GitHub PAT (classic with `repo` scope, or fine-grained with Contents read/write on `danshapiro/homebrew-kilroy`)
3. Add the PAT as repository secret `HOMEBREW_TAP_GITHUB_TOKEN` in `danshapiro/kilroy` > Settings > Secrets and variables > Actions

---

### Verification

After all tasks are complete:

1. `./kilroy --version` prints `kilroy dev`
2. `go build -ldflags "-X github.com/danshapiro/kilroy/internal/version.Version=0.1.0" -o ./kilroy ./cmd/kilroy && ./kilroy --version` prints `kilroy 0.1.0`
3. `goreleaser check` (if installed) reports valid config
4. `grep -r 'strongdm/kilroy' --include='*.go' --include='go.mod' --include='*.md' .` returns nothing
5. `.github/workflows/release.yml` exists and is valid YAML
