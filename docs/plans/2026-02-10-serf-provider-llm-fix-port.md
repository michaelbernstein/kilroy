# Serf Provider/LLM Fix Port Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Port the provider/LLM bug fixes from `serf` (`wip/unified-llm-spec-coverage`) into `kilroy` with local code changes and regression tests, not blind git cherry-picks.

**Architecture:** Treat each upstream fix commit as a behavioral spec, then implement the equivalent behavior in `kilroy` idiomatically. Keep each fix in a small TDD loop (failing test -> minimal implementation -> pass -> commit), and validate interactions with `attractor` failover/error classification to avoid local regressions. Preserve already-landed equivalent fixes and avoid duplicate churn.

**Tech Stack:** Go (`go test`), `internal/llm/*` adapters and error types, `internal/attractor/engine/*` failover/classification tests, git local branch workflow.

---

## Scope Ledger (Upstream -> Kilroy Status)

- `d9fa726` Normalize finish reasons: **missing in kilroy**
- `a623418` Add `ContentFilterError` / `QuotaExceededError`: **missing in kilroy**
- `c25a9b0` Message-based error classification for 400/422: **missing in kilroy**
- `e35977f` Anthropic complete default `max_tokens=4096`: **missing in kilroy** (`2048` today)
- `3227ac2` Anthropic tool `cache_control` <=4 blocks: **already present in kilroy** (equivalent local implementation + tests)
- `bf2d57a` Google gRPC error mapping coverage test: **coverage missing in kilroy** (behavior should be locked with tests)

### Task 1: Add Canonical Finish-Reason Normalization Primitives

**Files:**
- Modify: `internal/llm/types.go`
- Create: `internal/llm/types_test.go`
- Test: `internal/llm/types_test.go`

**Step 1: Write the failing test**

Create `internal/llm/types_test.go`:

```go
package llm

import "testing"

func TestNormalizeFinishReason(t *testing.T) {
	cases := []struct {
		provider string
		raw      string
		want     string
	}{
		{"openai", "stop", "stop"},
		{"openai", "length", "length"},
		{"openai", "tool_calls", "tool_calls"},
		{"openai", "content_filter", "content_filter"},
		{"anthropic", "end_turn", "stop"},
		{"anthropic", "stop_sequence", "stop"},
		{"anthropic", "max_tokens", "length"},
		{"anthropic", "tool_use", "tool_calls"},
		{"google", "STOP", "stop"},
		{"google", "MAX_TOKENS", "length"},
		{"google", "SAFETY", "content_filter"},
		{"google", "RECITATION", "content_filter"},
		{"openai", "weird_value", "other"},
		{"anthropic", "unknown", "other"},
		{"google", "BLOCKLIST", "other"},
		{"openai", "", "stop"},
	}
	for _, tc := range cases {
		t.Run(tc.provider+"/"+tc.raw, func(t *testing.T) {
			got := NormalizeFinishReason(tc.provider, tc.raw)
			if got.Reason != tc.want {
				t.Fatalf("NormalizeFinishReason(%q, %q).Reason=%q want %q", tc.provider, tc.raw, got.Reason, tc.want)
			}
			if tc.raw != "" && got.Raw != tc.raw {
				t.Fatalf("NormalizeFinishReason(%q, %q).Raw=%q want %q", tc.provider, tc.raw, got.Raw, tc.raw)
			}
		})
	}
}
```

**Step 2: Run test to verify it fails**

Run: `go test ./internal/llm -run TestNormalizeFinishReason -count=1`
Expected: FAIL with `undefined: NormalizeFinishReason`.

**Step 3: Write minimal implementation**

Add canonical constants and helper to `internal/llm/types.go`:

```go
const (
	FinishReasonStop          = "stop"
	FinishReasonLength        = "length"
	FinishReasonToolCalls     = "tool_calls"
	FinishReasonContentFilter = "content_filter"
	FinishReasonError         = "error"
	FinishReasonOther         = "other"
)

func NormalizeFinishReason(provider, raw string) FinishReason {
	if raw == "" {
		return FinishReason{Reason: FinishReasonStop}
	}
	reason := normalizeFinish(provider, raw)
	return FinishReason{Reason: reason, Raw: raw}
}

func normalizeFinish(provider, raw string) string {
	// provider-specific mapping...
}
```

**Step 4: Run test to verify it passes**

Run: `go test ./internal/llm -run TestNormalizeFinishReason -count=1`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/llm/types.go internal/llm/types_test.go
git commit -m "fix(llm): add canonical finish reason normalization for provider-specific raw values"
```

### Task 2: Port Anthropic Finish-Reason + Default Max Tokens Fixes

**Files:**
- Modify: `internal/llm/providers/anthropic/adapter.go`
- Modify: `internal/llm/providers/anthropic/adapter_test.go`
- Test: `internal/llm/providers/anthropic/adapter_test.go`

**Step 1: Write failing tests**

Add tests in `internal/llm/providers/anthropic/adapter_test.go`:

```go
func TestAdapter_Complete_DefaultMaxTokens_Is4096(t *testing.T) { /* assert request body max_tokens == 4096 */ }
func TestAdapter_Complete_FinishReason_Normalized(t *testing.T) { /* end_turn -> stop, stop_sequence -> stop, max_tokens -> length */ }
func TestAdapter_Complete_FinishReason_ToolUse_Normalized(t *testing.T) { /* tool_use -> tool_calls with Raw=tool_use */ }
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/llm/providers/anthropic -run 'TestAdapter_Complete_DefaultMaxTokens_Is4096|TestAdapter_Complete_FinishReason_' -count=1`
Expected: FAIL with `max_tokens` default mismatch and/or finish reason mismatch.

**Step 3: Write minimal implementation**

Update `internal/llm/providers/anthropic/adapter.go`:

```go
// Complete path default
maxTokens := 4096

// Stream message_delta path
finish = llm.NormalizeFinishReason("anthropic", sr)

// Tool-use finish
r.Finish = llm.FinishReason{Reason: "tool_calls", Raw: "tool_use"}

// Complete response mapping
sr, _ := raw["stop_reason"].(string)
r.Finish = llm.NormalizeFinishReason("anthropic", sr)
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/llm/providers/anthropic -count=1`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/llm/providers/anthropic/adapter.go internal/llm/providers/anthropic/adapter_test.go
git commit -m "fix(anthropic): normalize finish reasons and align complete default max_tokens to 4096"
```

### Task 3: Port Google Finish-Reason Normalization + gRPC Coverage

**Files:**
- Modify: `internal/llm/providers/google/adapter.go`
- Modify: `internal/llm/providers/google/adapter_test.go`
- Test: `internal/llm/providers/google/adapter_test.go`

**Step 1: Write failing tests**

Add tests:

```go
func TestAdapter_Complete_FinishReason_Normalized(t *testing.T) { /* STOP -> stop, MAX_TOKENS -> length, SAFETY/RECITATION -> content_filter */ }
func TestAdapter_Complete_GRPCStatusMapping(t *testing.T) { /* lock 429/404/400/403 mappings from Gemini error envelope */ }
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/llm/providers/google -run 'TestAdapter_Complete_FinishReason_Normalized|TestAdapter_Complete_GRPCStatusMapping' -count=1`
Expected: FAIL because finish reasons are still raw provider values.

**Step 3: Write minimal implementation**

In `internal/llm/providers/google/adapter.go`, replace raw assignment with normalized mapping:

```go
finish = llm.NormalizeFinishReason("google", fr)
// ...
r.Finish = llm.NormalizeFinishReason("google", fr)
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/llm/providers/google -count=1`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/llm/providers/google/adapter.go internal/llm/providers/google/adapter_test.go
git commit -m "fix(google): normalize finish reasons and add grpc-envelope error mapping regression tests"
```

### Task 4: Port Error-Hierarchy and Message-Based 400/422 Classification

**Files:**
- Modify: `internal/llm/errors.go`
- Modify: `internal/llm/errors_test.go`
- Test: `internal/llm/errors_test.go`

**Step 1: Write failing tests**

Add tests in `internal/llm/errors_test.go`:

```go
func TestContentFilterError_ImplementsErrorInterface(t *testing.T) {}
func TestQuotaExceededError_ImplementsErrorInterface(t *testing.T) {}
func TestErrorFromHTTPStatus_MessageBasedClassification(t *testing.T) {
	// 400/422 + message hints -> ContentFilter/ContextLength/QuotaExceeded/NotFound/Auth
	// 401/404/429 remain status-driven and are not overridden by message.
}
```

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/llm -run 'TestContentFilterError_ImplementsErrorInterface|TestQuotaExceededError_ImplementsErrorInterface|TestErrorFromHTTPStatus_MessageBasedClassification' -count=1`
Expected: FAIL with missing types and classifier.

**Step 3: Write minimal implementation**

Update `internal/llm/errors.go`:

```go
type ContentFilterError struct{ httpErrorBase }
type QuotaExceededError struct{ httpErrorBase }

func ErrorFromHTTPStatus(...) error {
	switch statusCode {
	case 400, 422:
		base.retryable = false
		if err := classifyByMessage(base); err != nil {
			return err
		}
		return &InvalidRequestError{base}
	// ...
	}
}

func classifyByMessage(base httpErrorBase) error {
	lower := strings.ToLower(base.message)
	switch {
	case strings.Contains(lower, "content filter") || strings.Contains(lower, "safety"):
		return &ContentFilterError{base}
	case strings.Contains(lower, "context length") || strings.Contains(lower, "too many tokens"):
		return &ContextLengthError{base}
	case strings.Contains(lower, "quota") || strings.Contains(lower, "billing"):
		return &QuotaExceededError{base}
	case strings.Contains(lower, "not found") || strings.Contains(lower, "does not exist"):
		return &NotFoundError{base}
	case strings.Contains(lower, "unauthorized") || strings.Contains(lower, "invalid key"):
		return &AuthenticationError{base}
	}
	return nil
}
```

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/llm -count=1`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/llm/errors.go internal/llm/errors_test.go
git commit -m "fix(llm-errors): add content-filter/quota error types and classify ambiguous 400/422 responses by message"
```

### Task 5: Align Attractor Failover Behavior for New Error Types

**Files:**
- Modify: `internal/attractor/engine/codergen_router.go`
- Modify: `internal/attractor/engine/codergen_failover_test.go`
- Modify: `internal/attractor/engine/provider_error_classification_test.go`
- Test: `internal/attractor/engine/codergen_failover_test.go`
- Test: `internal/attractor/engine/provider_error_classification_test.go`

**Step 1: Write failing tests**

Add explicit behavior tests:

```go
func TestShouldFailoverLLMError_ContentFilterDoesNotFailover(t *testing.T) {
	err := llm.ErrorFromHTTPStatus("openai", 400, "blocked by content filter policy", nil, nil)
	if shouldFailoverLLMError(err) { t.Fatalf("content filter should not fail over") }
}

func TestShouldFailoverLLMError_QuotaExceededDoesFailover(t *testing.T) {
	err := llm.ErrorFromHTTPStatus("openai", 400, "quota exceeded for account", nil, nil)
	if !shouldFailoverLLMError(err) { t.Fatalf("quota exhausted should fail over") }
}
```

Add classification assertions in `provider_error_classification_test.go` that these remain deterministic API failures.

**Step 2: Run tests to verify they fail**

Run: `go test ./internal/attractor/engine -run 'TestShouldFailoverLLMError_|TestClassifyAPIError' -count=1`
Expected: FAIL on content-filter failover policy mismatch.

**Step 3: Write minimal implementation**

Update `shouldFailoverLLMError` in `internal/attractor/engine/codergen_router.go`:

```go
var cfe *llm.ContentFilterError
if errors.As(err, &cfe) {
	return false
}
```

Keep quota as failover-eligible (no extra exclusion), matching operational intent: quota is provider/account-specific; content filters are request-policy deterministic.

**Step 4: Run tests to verify they pass**

Run: `go test ./internal/attractor/engine -run 'TestShouldFailoverLLMError_|TestClassifyAPIError' -count=1`
Expected: PASS.

**Step 5: Commit**

```bash
git add internal/attractor/engine/codergen_router.go internal/attractor/engine/codergen_failover_test.go internal/attractor/engine/provider_error_classification_test.go
git commit -m "fix(attractor-failover): keep content-filter failures deterministic while allowing quota-triggered provider failover"
```

### Task 6: Verify Already-Landed Cache-Control Fix and Prevent Regressions

**Files:**
- Modify: `internal/llm/providers/anthropic/adapter_test.go` (only if additional guard test needed)
- Test: `internal/llm/providers/anthropic/adapter_test.go`

**Step 1: Run existing regression test**

Run: `go test ./internal/llm/providers/anthropic -run 'large_toolset_uses_sparse_cache_control_breakpoints' -count=1`
Expected: PASS (proves equivalent of upstream `3227ac2` is already in `kilroy`).

**Step 2: Optional guard test hardening**

If needed, add a direct assertion that tool cache checkpoints are exactly one block on tool definitions, not one per tool.

**Step 3: Commit (only if test changed)**

```bash
git add internal/llm/providers/anthropic/adapter_test.go
git commit -m "test(anthropic): harden sparse cache_control breakpoint regression coverage for large toolsets"
```

### Task 7: Full Verification and Integration Safety Check

**Files:**
- No source changes expected; test-only run

**Step 1: Run focused provider/LLM suite**

Run: `go test ./internal/llm/... -count=1`
Expected: PASS.

**Step 2: Run attractor suites affected by error semantics**

Run: `go test ./internal/attractor/engine -run 'TestClassifyAPIError|TestShouldFailoverLLMError_' -count=1`
Expected: PASS.

**Step 3: Run broad smoke tests before merge to local `main`**

Run: `go test ./cmd/... ./internal/... -count=1`
Expected: PASS (or document known unrelated failures).

**Step 4: Commit test evidence note**

If you keep a port ledger, append test run outcomes and exact git SHAs before merge.

---

## Branch and Merge Workflow (Local-First)

1. Implement on current branch: `plan/serf-provider-llm-fixes-2026-02-10` or a follow-up implementation branch branched from it.
2. Use one commit per task above, in order.
3. After Task 7 passes, fast-forward merge into local `main`.
4. Run final smoke once on local `main`.
5. Push `main` to `origin/main` only after local verification is green.

## Why Code-Port (Not Cherry-Pick)

- `kilroy` has local architecture deltas (provider aliasing, attractor failover policy, existing Anthropic cache-control helpers), so direct cherry-picks risk conflicts and subtle behavior regressions.
- Test-first local implementation preserves upstream intent while matching current code patterns and constraints.
