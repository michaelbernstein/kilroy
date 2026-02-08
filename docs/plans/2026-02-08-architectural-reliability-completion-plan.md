# Architectural Reliability Completion Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Make Attractor fail fast on deterministic failures, retry only transient failures, and emit complete terminal/preflight artifacts so failures are diagnosable without restart storms.

**Architecture:** This is a delta plan against the current `codex-state-isolation-watchdog` branch state. Keep the existing loop-restart policy primitives (`classifyFailureClass`, normalization, signature circuit breaker) and add the missing shared stage-retry gate, source-level provider error classification, robust preflight, and fan-in classification metadata. Preserve the current two-class taxonomy (`transient_infra` and `deterministic`) for compatibility, but implement deterministic-precedence aggregation at fan-in so mixed failures fail closed.

**Tech Stack:** Go 1.25, stdlib `testing`, existing engine integration harness, shell guardrail matrix.

---

## Current-State Baseline (Verified Before Plan Rewrite)

These are already implemented and must be reused, not reimplemented:

1. Failure-class constants + normalization + classifier in `internal/attractor/engine/loop_restart_policy.go`:
   - `failureClassTransientInfra`
   - `failureClassDeterministic`
   - `classifyFailureClass(...)`
   - `normalizedFailureClass(...)`
   - `normalizedFailureClassOrDefault(...)`
   - `restartFailureSignature(...)`
2. Loop-restart class gating + signature circuit breaker in `internal/attractor/engine/engine.go` (`runLoop` + `loopRestart`).
3. Fatal terminal outcome persistence path (`final.json`) in `internal/attractor/engine/engine.go` via `persistFatalOutcome(...)` + `persistTerminalOutcome(...)`.
4. Existing model-catalog preflight check in `internal/attractor/engine/run_with_config.go` via `validateProviderModelPairs(...)` and test `TestRunWithConfig_FailsFast_WhenCLIModelNotInCatalogForProvider` in `internal/attractor/engine/provider_preflight_test.go`.
5. Existing guardrail matrix script at `scripts/e2e-guardrail-matrix.sh` (needs hardening, not recreation).

Missing deltas this plan implements:

1. Shared stage retry policy helper (`shouldRetryOutcome`) and wiring into `executeWithRetry`.
2. Provider CLI failure classification at source (`runCLI`) with `failure_class`/`failure_signature` metadata.
3. Dedicated Anthropic invocation contract fix (`--verbose`) as a separate behavior change with its own test.
4. Deterministic provider CLI preflight in `RunWithConfig` with always-written `preflight_report.json`.
5. End-to-end integration test: provider-classified deterministic failure prevents stage retries.
6. Fan-in all-fail aggregated class/signature with deterministic precedence.
7. Guardrail script no-false-pass protection (`[no tests to run]`).

---

## Non-Negotiable TDD Execution Rules

1. Every red test must fail for a real assertion/behavior reason, never as an empty stub.
2. If a red step intentionally introduces compile-red (for a missing symbol), run it locally, then immediately implement green in the same task before committing.
3. Do not commit red states that break package compilation.
4. Every targeted `go test -run` must fail hard if output includes `[no tests to run]`.

Use this shell helper in each task block:

```bash
run_test_checked() {
  local label="$1"
  shift
  echo "$label"
  local out
  out="$($@ 2>&1)"
  printf '%s\n' "$out"
  if grep -q '\[no tests to run\]' <<<"$out"; then
    echo "FAIL: no tests executed"
    exit 1
  fi
}
```

---

### Task 1: Add Shared Stage Retry Policy Helper (Delta Only)

**Files:**
- Create: `internal/attractor/engine/failure_policy.go`
- Create: `internal/attractor/engine/failure_policy_test.go`
- Reuse (no logic rewrite): `internal/attractor/engine/loop_restart_policy.go`

**Step 1: Add red tests for retry gating behavior**

Create `failure_policy_test.go` with real assertions:

```go
func TestShouldRetryOutcome_ClassGated(t *testing.T) {
	cases := []struct {
		name    string
		out     runtime.Outcome
		class   string
		want    bool
	}{
		{"fail transient retries", runtime.Outcome{Status: runtime.StatusFail}, failureClassTransientInfra, true},
		{"fail deterministic does not retry", runtime.Outcome{Status: runtime.StatusFail}, failureClassDeterministic, false},
		{"retry transient retries", runtime.Outcome{Status: runtime.StatusRetry}, failureClassTransientInfra, true},
		{"retry deterministic does not retry", runtime.Outcome{Status: runtime.StatusRetry}, failureClassDeterministic, false},
		{"unknown class defaults fail-closed", runtime.Outcome{Status: runtime.StatusFail}, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldRetryOutcome(tc.out, tc.class); got != tc.want {
				t.Fatalf("shouldRetryOutcome(%q,%q)=%v want %v", tc.out.Status, tc.class, got, tc.want)
			}
		})
	}
}

func TestShouldRetryOutcome_NonFailureStatusesNeverRetry(t *testing.T) {
	for _, st := range []runtime.Status{runtime.StatusSuccess, runtime.StatusPartialSuccess, runtime.StatusSkipped} {
		if shouldRetryOutcome(runtime.Outcome{Status: st}, failureClassTransientInfra) {
			t.Fatalf("status=%q should never retry", st)
		}
	}
}
```

**Step 2: Run red test command (local red, no commit yet)**

```bash
run_test_checked "task1 red" go test ./internal/attractor/engine -run '^TestShouldRetryOutcome_ClassGated$|^TestShouldRetryOutcome_NonFailureStatusesNeverRetry$' -count=1
```

Expected: red (likely compile-red: `undefined: shouldRetryOutcome`).

**Step 3: Implement helper in new shared file**

Add to `failure_policy.go`:

```go
func shouldRetryOutcome(out runtime.Outcome, failureClass string) bool {
	if out.Status != runtime.StatusFail && out.Status != runtime.StatusRetry {
		return false
	}
	return normalizedFailureClassOrDefault(failureClass) == failureClassTransientInfra
}
```

**Step 4: Run green tests**

```bash
run_test_checked "task1 green" go test ./internal/attractor/engine -run '^TestShouldRetryOutcome_ClassGated$|^TestShouldRetryOutcome_NonFailureStatusesNeverRetry$' -count=1
```

Expected: pass.

**Step 5: Commit green**

```bash
git add internal/attractor/engine/failure_policy.go internal/attractor/engine/failure_policy_test.go
git commit -m "feat(engine): add shared class-gated stage retry helper"
```

---

### Task 2: Gate `executeWithRetry` Using Shared Failure Policy

**Files:**
- Modify: `internal/attractor/engine/engine.go`
- Create: `internal/attractor/engine/retry_failure_class_test.go`
- Reuse: `internal/attractor/engine/retry_policy_test.go`
- Reuse: `internal/attractor/engine/retry_on_retry_status_test.go`

**Step 1: Add red integration tests (non-empty, mock class hints explicitly)**

In `retry_failure_class_test.go`, add:

1. `TestRun_DeterministicFailure_DoesNotRetry`
   - handler returns `StatusFail` with `Meta["failure_class"]="deterministic"`.
   - graph sets `default_max_retry=3`.
   - assert no second attempt marker and no `stage_retry_sleep` event.
   - assert `stage_retry_blocked` event present in `progress.ndjson`.
2. `TestRun_TransientFailure_StillRetries`
   - first attempt returns `StatusFail` with `failure_class=transient_infra`; second returns success.
   - assert retry happened and final stage success.

**Step 2: Run red tests**

```bash
run_test_checked "task2 red" go test ./internal/attractor/engine -run '^TestRun_DeterministicFailure_DoesNotRetry$|^TestRun_TransientFailure_StillRetries$' -count=1
```

Expected: red on deterministic test (current code retries fail/retry regardless of class).

**Step 3: Wire class-gated retry into `executeWithRetry`**

In `engine.go` retry loop:

1. Compute class each attempt with existing `classifyFailureClass(out)`.
2. Replace unconditional retry path:
   - from: `if attempt < maxAttempts { ... sleep ... continue }`
   - to: `if attempt < maxAttempts && shouldRetryOutcome(out, failureClass) { ... }`
3. For blocked retries, append `stage_retry_blocked` progress event with:
   - `node_id`, `attempt`, `max`, `status`, `failure_class`, `failure_reason`.
4. Keep existing allow-partial and terminal canonicalization behavior unchanged.

**Step 4: Run focused green tests**

```bash
run_test_checked "task2 green new" go test ./internal/attractor/engine -run '^TestRun_DeterministicFailure_DoesNotRetry$|^TestRun_TransientFailure_StillRetries$' -count=1
run_test_checked "task2 green regressions" go test ./internal/attractor/engine -run '^TestRun_RetriesOnFail_ThenSucceeds$|^TestRun_RetriesOnRetryStatus$|^TestRun_AllowPartialAfterRetryExhaustion$' -count=1
```

Expected: pass.

**Step 5: Commit green**

```bash
git add internal/attractor/engine/engine.go internal/attractor/engine/retry_failure_class_test.go
git commit -m "fix(engine): gate stage retries by normalized failure class"
```

---

### Task 3: Add Provider CLI Failure Classification at Source

**Files:**
- Create: `internal/attractor/engine/provider_error_classification.go`
- Create: `internal/attractor/engine/provider_error_classification_test.go`
- Modify: `internal/attractor/engine/codergen_router.go`

**Step 1: Add red classifier tests with real assertions**

Add tests:

1. `TestClassifyProviderCLIError_AnthropicStreamJSONRequiresVerbose`
   - deterministic
   - signature starts with `provider_contract|anthropic|...`
2. `TestClassifyProviderCLIError_GeminiModelNotFound`
   - deterministic
   - signature starts with `provider_model_unavailable|google|...`
3. `TestClassifyProviderCLIError_CodexIdleTimeout_RunErrSignal`
   - transient
   - signature starts with `provider_timeout|openai|...`
4. `TestClassifyProviderCLIError_UnknownFallbackIsDeterministic`
   - deterministic fallback

**Step 2: Run red tests**

```bash
run_test_checked "task3 red" go test ./internal/attractor/engine -run '^TestClassifyProviderCLIError_AnthropicStreamJSONRequiresVerbose$|^TestClassifyProviderCLIError_GeminiModelNotFound$|^TestClassifyProviderCLIError_CodexIdleTimeout_RunErrSignal$|^TestClassifyProviderCLIError_UnknownFallbackIsDeterministic$' -count=1
```

Expected: red before implementation.

**Step 3: Implement classifier**

Implement in `provider_error_classification.go`:

```go
type providerCLIClassifiedError struct {
	FailureClass     string
	FailureSignature string
	FailureReason    string
}

func classifyProviderCLIError(provider string, stderr string, runErr error) providerCLIClassifiedError
```

Classification rules:

1. Provider-specific deterministic contract/model signatures first.
2. Transient infra hints (timeout, reset, 429, 5xx) second.
3. Deterministic fallback last.
4. Always return normalized class values used by shared policy.

**Step 4: Wire classifier in `runCLI` failure return path**

In `codergen_router.go`, when `runErr != nil`:

1. read `stderr.log` content.
2. call `classifyProviderCLIError(providerKey, stderr, runErr)`.
3. return `runtime.Outcome` with:
   - `FailureReason` from classifier
   - `Meta["failure_class"]`
   - `Meta["failure_signature"]`
   - `ContextUpdates["failure_class"]`

**Step 5: Run green tests**

```bash
run_test_checked "task3 green" go test ./internal/attractor/engine -run '^TestClassifyProviderCLIError_AnthropicStreamJSONRequiresVerbose$|^TestClassifyProviderCLIError_GeminiModelNotFound$|^TestClassifyProviderCLIError_CodexIdleTimeout_RunErrSignal$|^TestClassifyProviderCLIError_UnknownFallbackIsDeterministic$' -count=1
```

Expected: pass.

**Step 6: Commit green**

```bash
git add internal/attractor/engine/provider_error_classification.go internal/attractor/engine/provider_error_classification_test.go internal/attractor/engine/codergen_router.go
git commit -m "feat(engine): classify provider CLI failures and emit class/signature metadata"
```

---

### Task 4: Apply Anthropic Invocation Contract Fix as Separate Behavior Change

**Files:**
- Modify: `internal/attractor/engine/codergen_router.go`
- Modify: `internal/attractor/engine/codergen_cli_invocation_test.go`

**Step 1: Add red invocation test**

Add:

```go
func TestDefaultCLIInvocation_AnthropicIncludesVerboseForStreamJSON(t *testing.T) {
	_, args := defaultCLIInvocation("anthropic", "claude-opus", "/tmp/worktree")
	if !hasArg(args, "--verbose") {
		t.Fatalf("expected --verbose for anthropic stream-json invocation; args=%v", args)
	}
}
```

**Step 2: Run red tests**

```bash
run_test_checked "task4 red" go test ./internal/attractor/engine -run '^TestDefaultCLIInvocation_AnthropicIncludesVerboseForStreamJSON$' -count=1
```

Expected: red.

**Step 3: Implement invocation fix**

In `defaultCLIInvocation(...)` for provider `anthropic`, include `--verbose` in args.

**Step 4: Run green tests**

```bash
run_test_checked "task4 green" go test ./internal/attractor/engine -run '^TestDefaultCLIInvocation_AnthropicIncludesVerboseForStreamJSON$|^TestDefaultCLIInvocation_OpenAI_DoesNotUseDeprecatedAskForApproval$|^TestDefaultCLIInvocation_GoogleGeminiNonInteractive$' -count=1
```

Expected: pass.

**Step 5: Commit green**

```bash
git add internal/attractor/engine/codergen_router.go internal/attractor/engine/codergen_cli_invocation_test.go
git commit -m "fix(engine): align anthropic cli invocation with stream-json verbose contract"
```

---

### Task 5: Add Deterministic CLI Preflight + Always-Written `preflight_report.json`

**Files:**
- Create: `internal/attractor/engine/provider_preflight.go`
- Modify: `internal/attractor/engine/run_with_config.go`
- Modify: `internal/attractor/engine/provider_preflight_test.go`
- Optional: `internal/attractor/engine/run_with_config_integration_test.go`

**Step 1: Add red tests (non-empty)**

In `provider_preflight_test.go`, add:

1. `TestRunWithConfig_PreflightFails_WhenProviderCLIBinaryMissing`
   - configure CLI backend provider in graph; point binary env var to nonexistent path.
   - assert deterministic preflight error and report written.
2. `TestRunWithConfig_PreflightFails_WhenAnthropicCapabilityMissingVerbose`
   - use fake `claude` script where capability probe indicates missing `--verbose` support.
   - assert deterministic preflight error and report written.
3. `TestRunWithConfig_WritesPreflightReport_Always`
   - assert `preflight_report.json` exists for both pass and fail preflight outcomes.
4. Reuse existing `TestRunWithConfig_FailsFast_WhenCLIModelNotInCatalogForProvider` as regression.

**Step 2: Run red tests**

```bash
run_test_checked "task5 red" go test ./internal/attractor/engine -run '^TestRunWithConfig_PreflightFails_WhenProviderCLIBinaryMissing$|^TestRunWithConfig_PreflightFails_WhenAnthropicCapabilityMissingVerbose$|^TestRunWithConfig_WritesPreflightReport_Always$' -count=1
```

Expected: red before preflight implementation.

**Step 3: Implement preflight model + report writer**

In `provider_preflight.go`:

1. Define report schema:
   - checks list (provider, check_name, status, detail)
   - summary (pass/fail counts)
   - timestamp
2. Implement writer used on all preflight paths.
3. Implement deterministic error type/message with class marker.

**Step 4: Integrate preflight into `RunWithConfig` with correct ordering**

In `run_with_config.go`, execute in this order:

1. `Prepare(...)`
2. provider backend presence checks
3. `opts.applyDefaults()`
4. model catalog resolve/load + existing provider/model validation
5. `runProviderCLIPreflight(...)` (new)
6. CXDB health/binary/context creation
7. engine run

Constraints:

1. Preflight must run before CXDB health.
2. Preflight must run after model catalog resolution (needs provider/model context).
3. `preflight_report.json` must be persisted for both pass and fail preflight results.

**Step 5: Capability probing strategy (idiomatic + less brittle)**

Do not rely on one exact help-text sentence.

For each used CLI provider:

1. Resolve executable path and verify executable exists.
2. Run command-specific lightweight probe command (non-interactive):
   - OpenAI: `codex exec --help`
   - Anthropic: `claude --help`
   - Google: `gemini --help`
3. Validate required option tokens with tolerant matching over token set, not full-line string matching.
4. Emit deterministic failure with provider + missing capability when required token absent.

**Step 6: Run green tests**

```bash
run_test_checked "task5 green preflight" go test ./internal/attractor/engine -run '^TestRunWithConfig_PreflightFails_WhenProviderCLIBinaryMissing$|^TestRunWithConfig_PreflightFails_WhenAnthropicCapabilityMissingVerbose$|^TestRunWithConfig_WritesPreflightReport_Always$|^TestRunWithConfig_FailsFast_WhenCLIModelNotInCatalogForProvider$' -count=1
```

Expected: pass.

**Step 7: Commit green**

```bash
git add internal/attractor/engine/provider_preflight.go internal/attractor/engine/run_with_config.go internal/attractor/engine/provider_preflight_test.go internal/attractor/engine/run_with_config_integration_test.go
git commit -m "feat(engine): add deterministic cli preflight with always-written preflight report"
```

---

### Task 6: Add End-to-End Bridge Test (Source Classification -> Stage Retry Gate)

**Files:**
- Create: `internal/attractor/engine/retry_classification_integration_test.go`
- Reuse: `internal/attractor/engine/codergen_process_test.go` helpers (fake CLI script patterns)

**Step 1: Add red integration test**

Add test `TestRunWithConfig_CLIDeterministicFailure_DoesNotConsumeRetryBudget`:

1. configure anthropic CLI backend with fake `claude` script that always exits 1 and prints deterministic model/contract error.
2. graph node has `max_retries=3`.
3. assert exactly one invocation of fake CLI (marker file or counter file).
4. assert progress includes `stage_retry_blocked` and omits `stage_retry_sleep`.

**Step 2: Run red test**

```bash
run_test_checked "task6 red" go test ./internal/attractor/engine -run '^TestRunWithConfig_CLIDeterministicFailure_DoesNotConsumeRetryBudget$' -count=1
```

Expected: red until both classification and retry-gating wiring are complete.

**Step 3: Implement minimal wiring fixes discovered by this test**

Likely fixes (if still missing after tasks 2/3):

1. Ensure `runCLI` populates `Meta.failure_class` for all CLI failure paths.
2. Ensure `executeWithRetry` always uses `classifyFailureClass(out)` before deciding retry.

**Step 4: Run green test**

```bash
run_test_checked "task6 green" go test ./internal/attractor/engine -run '^TestRunWithConfig_CLIDeterministicFailure_DoesNotConsumeRetryBudget$' -count=1
```

Expected: pass.

**Step 5: Commit green**

```bash
git add internal/attractor/engine/retry_classification_integration_test.go internal/attractor/engine/engine.go internal/attractor/engine/codergen_router.go
git commit -m "test(engine): verify classified deterministic cli failures block stage retries end-to-end"
```

---

### Task 7: Add Fan-In All-Fail Classification with Deterministic Precedence

**Files:**
- Modify: `internal/attractor/engine/parallel_handlers.go`
- Create: `internal/attractor/engine/fanin_failure_class_test.go`
- Optionally update: `internal/attractor/engine/parallel_guardrails_test.go`

**Policy decision (explicit):**

For all-fail fan-in outcomes:

1. If any branch class is deterministic, aggregate class is deterministic.
2. Else if all failing branches are transient, aggregate class is transient.
3. Empty/unknown branch class is treated as deterministic (fail closed).

Rationale: this prevents retry storms when mixed deterministic/transient failures occur.

**Step 1: Add red tests**

Add tests:

1. `TestFanIn_AllParallelBranchesFail_MixedClasses_AggregatesDeterministic`
2. `TestFanIn_AllParallelBranchesFail_AllTransient_AggregatesTransient`
3. `TestFanIn_AllParallelBranchesFail_UnknownClass_AggregatesDeterministic`

Include assertions for both:

1. `Meta["failure_class"]`
2. `Meta["failure_signature"]` prefixed with `parallel_all_failed|...`

**Step 2: Run red tests**

```bash
run_test_checked "task7 red" go test ./internal/attractor/engine -run '^TestFanIn_AllParallelBranchesFail_MixedClasses_AggregatesDeterministic$|^TestFanIn_AllParallelBranchesFail_AllTransient_AggregatesTransient$|^TestFanIn_AllParallelBranchesFail_UnknownClass_AggregatesDeterministic$' -count=1
```

Expected: red before fan-in metadata implementation.

**Step 3: Implement aggregate classifier in fan-in path**

In `parallel_handlers.go` when all branches fail:

1. aggregate class using branch outcomes/hints.
2. set outcome metadata:
   - `failure_class`
   - `failure_signature`.
3. set `ContextUpdates["failure_class"]`.

**Step 4: Run green tests**

```bash
run_test_checked "task7 green" go test ./internal/attractor/engine -run '^TestFanIn_AllParallelBranchesFail_MixedClasses_AggregatesDeterministic$|^TestFanIn_AllParallelBranchesFail_AllTransient_AggregatesTransient$|^TestFanIn_AllParallelBranchesFail_UnknownClass_AggregatesDeterministic$' -count=1
```

Expected: pass.

**Step 5: Commit green**

```bash
git add internal/attractor/engine/parallel_handlers.go internal/attractor/engine/fanin_failure_class_test.go internal/attractor/engine/parallel_guardrails_test.go
git commit -m "fix(engine): classify fan-in all-fail outcomes with deterministic-precedence policy"
```

---

### Task 8: Harden Guardrail Matrix + Update Reliability Runbook

**Files:**
- Modify: `scripts/e2e-guardrail-matrix.sh`
- Modify: `docs/strongdm/attractor/README.md`

**Step 1: Add `run_test_checked` helper to script**

Apply same no-false-pass guard in matrix script so targeted commands cannot silently pass with zero tests.

**Step 2: Extend matrix checks**

Include targeted checks for:

1. `TestShouldRetryOutcome_ClassGated`
2. `TestRun_DeterministicFailure_DoesNotRetry`
3. `TestClassifyProviderCLIError_AnthropicStreamJSONRequiresVerbose`
4. `TestRunWithConfig_WritesPreflightReport_Always`
5. `TestRunWithConfig_CLIDeterministicFailure_DoesNotConsumeRetryBudget`
6. `TestFanIn_AllParallelBranchesFail_MixedClasses_AggregatesDeterministic`

**Step 3: Update runbook docs**

In `docs/strongdm/attractor/README.md`, document final behavior:

1. Stage retry is class-gated by `shouldRetryOutcome`.
2. Provider CLI failures are source-classified and surfaced in stage outcome metadata.
3. CLI preflight runs before CXDB health and always writes `preflight_report.json`.
4. Fan-in all-fail outcomes include aggregated class/signature.
5. Deterministic failures block retries and loop-restart where applicable.

**Step 4: Run script and focused docs-related checks**

```bash
bash scripts/e2e-guardrail-matrix.sh
```

Expected: pass.

**Step 5: Commit green**

```bash
git add scripts/e2e-guardrail-matrix.sh docs/strongdm/attractor/README.md
git commit -m "docs+tests: enforce no-false-pass reliability matrix and document class-gated semantics"
```

---

### Task 9: Full Verification + Real DTTF Validation Run

**Files:**
- No new source files by default; this is validation.

**Step 1: Run full test gates**

```bash
go test ./cmd/kilroy -count=1
go test ./internal/attractor/runtime -count=1
go test ./internal/attractor/engine -count=1
go test ./internal/llm/providers/... -count=1
bash scripts/e2e-guardrail-matrix.sh
```

Expected: all pass.

**Step 2: Re-run DTTF from clean logs root (detached)**

Use detached launch and new run root.

```bash
RUN_ROOT="/tmp/kilroy-dttf-real-cxdb-$(date -u +%Y%m%dT%H%M%SZ)-postfix-v2"
mkdir -p "$RUN_ROOT"
setsid -f bash -lc 'cd /home/user/code/kilroy-wt-state-isolation-watchdog && ./kilroy attractor run --dot docs/strongdm/attractor/examples/dttf.dot --config docs/strongdm/attractor/examples/run.dttf.real-cxdb.yaml --logs-root "'"$RUN_ROOT"'"/logs >> "'"$RUN_ROOT"'"/run.out" 2>&1'
```

**Step 3: Validate terminal artifacts and retry behavior from logs**

Verify:

1. `final.json` exists in logs root.
2. if run fails deterministically, reason is explicit and no restart storm occurs.
3. progress includes expected `stage_retry_blocked` / `loop_restart_blocked` semantics.
4. no repeated deterministic signature loops beyond circuit-breaker threshold.

**Step 4: Commit only if code/docs changed during validation**

```bash
git add -A
git commit -m "test(attractor): validate reliability fixes against real dttf run with clean logs root"
```

---

## Required Green Exit Criteria

Implementation is complete only when all are true:

1. `shouldRetryOutcome` exists and its unit tests pass.
2. Deterministic stage failures do not consume retry budget; transient failures still retry.
3. CLI provider failures are classified at source and include `failure_class` + `failure_signature` in outcome metadata.
4. Anthropic CLI invocation includes `--verbose` and has dedicated regression coverage.
5. `RunWithConfig` executes deterministic provider CLI preflight before CXDB health and always writes `preflight_report.json`.
6. Integration path (provider-classified deterministic failure -> stage retry blocked) is covered by a passing test.
7. Fan-in all-fail outcomes emit aggregate class/signature using deterministic-precedence policy.
8. `scripts/e2e-guardrail-matrix.sh` fails fast on `[no tests to run]` and passes all included checks.
9. Full package test suites pass.
10. Real DTTF rerun from a clean logs root shows stable behavior without deterministic restart churn.

---

## Notes on Scope and Future Extension

1. This plan keeps the current binary failure taxonomy intentionally (`transient_infra`, `deterministic`) for compatibility and to minimize blast radius.
2. If future requirements need richer classes (auth/quota/rate_limit/etc.), add them as a separate migration with explicit compatibility behavior in `normalizedFailureClass(...)` and retry policy semantics.
3. This plan deliberately prioritizes fail-closed semantics for unknown classes.
