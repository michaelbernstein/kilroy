# Dotfile Creator Guardrails (Rec #1 Only) Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Implement only recommendation #1 in the dotfile creator flow: route `implement` failures by `failure_class`, remove `allow_partial` from the implement node, and keep deterministic failures out of verify loops.

**Architecture:** Keep engine behavior unchanged. Enforce reliability at graph-generation/template level by updating the english-to-dotfile reference template and its instructions, then align `demo/rogue/rogue-spark.dot` as the concrete regression target. Add parser/validator-based tests so this topology does not regress.

**Tech Stack:** Go tests (`internal/attractor/dot`, `internal/attractor/validate`), DOT templates (`skills/english-to-dotfile/reference_template.dot`), skill docs (`skills/english-to-dotfile/SKILL.md`), graph validation (`./kilroy attractor validate`).

---

### Task 1: Add Failing Guardrail Tests For Template Topology

**Files:**
- Create: `internal/attractor/validate/reference_template_guardrail_test.go`
- Test: `internal/attractor/validate/reference_template_guardrail_test.go`

**Step 1: Write the failing test**

```go
package validate

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/danshapiro/kilroy/internal/attractor/dot"
)

func loadReferenceTemplate(t *testing.T) []byte {
    t.Helper()
    repoRoot := findRepoRoot(t)
    p := filepath.Join(repoRoot, "skills", "english-to-dotfile", "reference_template.dot")
    b, err := os.ReadFile(p)
    if err != nil {
        t.Fatalf("read reference template: %v", err)
    }
    return b
}

func findRepoRoot(t *testing.T) string {
    t.Helper()
    dir, err := os.Getwd()
    if err != nil {
        t.Fatal(err)
    }
    for {
        if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
            return dir
        }
        parent := filepath.Dir(dir)
        if parent == dir {
            t.Fatal("could not find repo root (no go.mod)")
        }
        dir = parent
    }
}

func TestReferenceTemplate_ImplementNodeDisablesAllowPartial(t *testing.T) {
    g, err := dot.Parse(loadReferenceTemplate(t))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }
    impl := g.Nodes["implement"]
    if impl == nil {
        t.Fatal("missing implement node")
    }
    if impl.Attr("allow_partial", "false") == "true" {
        t.Fatal("implement.allow_partial must not be true")
    }
}

func TestReferenceTemplate_ImplementFailureRoutedBeforeVerify(t *testing.T) {
    g, err := dot.Parse(loadReferenceTemplate(t))
    if err != nil {
        t.Fatalf("parse: %v", err)
    }

    hasImplementToCheck := false
    hasCheckFailTransient := false
    hasCheckFailDeterministic := false
    hasCheckSuccessToVerify := false

    for _, e := range g.Edges {
        cond := e.Condition()
        switch {
        case e.From == "implement" && e.To == "check_implement" && cond == "":
            hasImplementToCheck = true
        case e.From == "check_implement" && e.To == "implement" && cond == "outcome=fail && context.failure_class=transient_infra" && e.Attr("loop_restart", "false") == "true":
            hasCheckFailTransient = true
        case e.From == "check_implement" && e.To == "postmortem" && cond == "outcome=fail && context.failure_class!=transient_infra":
            hasCheckFailDeterministic = true
        case e.From == "check_implement" && e.To == "verify_fmt" && cond == "outcome=success":
            hasCheckSuccessToVerify = true
        }
    }

    if !hasImplementToCheck || !hasCheckFailTransient || !hasCheckFailDeterministic || !hasCheckSuccessToVerify {
        t.Fatalf("missing implement failure-routing guardrails: implement_to_check=%v transient_fail=%v deterministic_fail=%v success_to_verify=%v",
            hasImplementToCheck, hasCheckFailTransient, hasCheckFailDeterministic, hasCheckSuccessToVerify)
    }
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/attractor/validate -run 'ReferenceTemplate_' -v`
Expected: FAIL because current template still has `allow_partial=true` and no `check_implement` gate.

**Step 3: Commit failing test**

```bash
git add internal/attractor/validate/reference_template_guardrail_test.go
git commit -m "test(validate): add failing guardrails for implement failure routing in reference template"
```

### Task 2: Update Reference Template DOT (No `allow_partial`, Add `check_implement` Gate)

**Files:**
- Modify: `skills/english-to-dotfile/reference_template.dot`
- Test: `internal/attractor/validate/reference_template_guardrail_test.go`

**Step 1: Remove `allow_partial` from implement node and strengthen prompt failure contract**

```dot
implement [
    shape=box,
    class="hard",
    max_retries=2,
    prompt="...\nWrite status JSON: outcome=success if build passes, outcome=fail with failure_reason, failure_class, and failure_signature otherwise."
]
```

**Step 2: Add explicit post-implement routing gate**

```dot
check_implement [shape=diamond, label="Implement OK?"]

implement -> check_implement
check_implement -> verify_fmt [condition="outcome=success"]
check_implement -> implement [condition="outcome=fail && context.failure_class=transient_infra", loop_restart=true]
check_implement -> postmortem [condition="outcome=fail && context.failure_class!=transient_infra"]
```

**Step 3: Run tests and graph validation**

Run:
- `go test ./internal/attractor/validate -run 'ReferenceTemplate_' -v`
- `./kilroy attractor validate --graph skills/english-to-dotfile/reference_template.dot`

Expected:
- tests PASS
- validator returns `ok` with no new failure-loop warnings.

**Step 4: Commit**

```bash
git add skills/english-to-dotfile/reference_template.dot internal/attractor/validate/reference_template_guardrail_test.go
git commit -m "dotfile-template: gate implement failures before verify and remove allow_partial"
```

### Task 3: Update english-to-dotfile Skill Instructions To Match New Guardrail

**Files:**
- Modify: `skills/english-to-dotfile/SKILL.md`

**Step 1: Add explicit requirement for implement post-check routing**

Add guidance near loop/inner-retry sections:

```md
- After any single-writer `implement` node, route through a diamond gate (`check_implement`) before entering deterministic verify nodes.
- `check_implement` must split fail paths by `context.failure_class`:
  - transient_infra -> `implement` with `loop_restart=true`
  - deterministic/non-transient -> `postmortem` (no restart)
```

**Step 2: Constrain `allow_partial` usage in this pattern**

Update the `allow_partial` row and template guidance:

```md
- In the reference hill-climbing template, do not set `allow_partial=true` on the primary `implement` node.
- Use `allow_partial` only when a downstream gate explicitly handles partial outcomes without masking hard failures.
```

**Step 3: Strengthen status payload requirement in implementation prompt template**

```md
Write status JSON: outcome=success if all criteria pass,
outcome=fail with failure_reason, failure_class, failure_signature, and details otherwise.
```

**Step 4: Commit**

```bash
git add skills/english-to-dotfile/SKILL.md
git commit -m "skills(english-to-dotfile): require implement failure-class gate and no default allow_partial"
```

### Task 4: Align `demo/rogue/rogue-spark.dot` With The Same Guardrail

**Files:**
- Modify: `demo/rogue/rogue-spark.dot`

**Step 1: Remove `allow_partial=true` from `implement`**

```dot
implement [
    shape=box,
    class="hard",
    max_retries=2,
    ...
]
```

**Step 2: Insert `check_implement` gate and reroute edges**

```dot
check_implement [shape=diamond, label="Implement OK?"]

plan -> implement
implement -> check_implement
check_implement -> verify_fmt [condition="outcome=success"]
check_implement -> implement [condition="outcome=fail && context.failure_class=transient_infra", loop_restart=true]
check_implement -> postmortem [condition="outcome=fail && context.failure_class!=transient_infra"]
```

**Step 3: Validate graph**

Run: `./kilroy attractor validate --graph demo/rogue/rogue-spark.dot`
Expected: `ok: rogue-spark.dot` and no `fail_loop_failure_class_guard` warning.

**Step 4: Commit**

```bash
git add demo/rogue/rogue-spark.dot
git commit -m "demo(rogue-spark): gate implement failures and remove allow_partial"
```

### Task 5: End-to-End Regression Checks (Template + Demo Graph)

**Files:**
- Verify only

**Step 1: Run targeted tests**

Run:
- `go test ./internal/attractor/validate -run 'ReferenceTemplate_|FailLoop|LoopRestartFailureEdge' -v`

Expected: PASS.

**Step 2: Run DOT validator checks**

Run:
- `./kilroy attractor validate --graph skills/english-to-dotfile/reference_template.dot`
- `./kilroy attractor validate --graph demo/rogue/rogue-spark.dot`

Expected: both `ok`.

**Step 3: Sanity-check no `allow_partial=true` remains in the guarded implement templates**

Run:
- `rg -n 'allow_partial=true' skills/english-to-dotfile/reference_template.dot demo/rogue/rogue-spark.dot`

Expected: no matches.

**Step 4: Final commit**

```bash
git add -A
git commit -m "test+dotfile-skill: lock implement failure routing guardrails and remove allow_partial defaults"
```
