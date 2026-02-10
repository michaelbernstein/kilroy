# Rogue-Fast Run Hardening Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Eliminate known rogue-fast run failure modes by fixing Kimi tool-call handling, hardening failover/preflight behavior, adding toolchain and timeout guardrails, and making retries cheaper and safer.

**Architecture:** Start with an explicit investigation stage to reproduce and localize the Kimi adapter/tool-call corruption. Land fixes in small, test-first slices, then harden runtime policy and graph behavior (`run.yaml` + `.dot`) so failures are detected early and loops do not silently burn cost.

**Tech Stack:** Go (`internal/llm/providers/anthropic`, `internal/agent`, `internal/attractor/engine`), DOT graph (`demo/rogue/rogue_fast.dot`), run config (`demo/rogue/run.yaml`), Go tests, attractor run artifacts (`progress.ndjson`, `events.ndjson`, `preflight_report.json`).

---

## Risk-to-Task Map

- Kimi tool-call payload corruption -> Tasks 1, 2, 3
- Preflight not validating failover/tool-path realism -> Task 4
- Implicit OpenAI failover surprises -> Task 5
- Missing `wasm-pack` discovered too late -> Task 6
- Shell default timeout too short for compile-heavy nodes -> Task 7
- Big turn budgets can still burn calls in loops -> Task 8
- Retry loops keep stale context (no restart) -> Task 8

---

### Task 1: Investigation Stage - Reproduce and Localize Kimi Tool-Call Corruption

**Files:**
- Modify: `internal/llm/providers/anthropic/adapter_test.go`
- Create: `internal/llm/providers/anthropic/testdata/kimi_tool_call_sequences.ndjson`
- Create: `docs/plans/2026-02-10-kimi-tool-call-investigation-notes.md`

**Step 1: Add failing adapter regression tests for real-world stream patterns**

Add tests that replay Kimi-like SSE sequences:
- `content_block_start.tool_use` includes non-empty `input`
- followed by one or more `content_block_delta.input_json_delta.partial_json`
- verify final `ToolCallData.Arguments` is valid JSON and not duplicated/concatenated

Test names:
- `TestAdapter_Stream_ToolUse_StartInputPlusDelta_NoDuplicateJSON`
- `TestAdapter_Stream_ToolUse_DeltaOnly_ValidJSON`

**Step 2: Run only the new failing tests**

Run: `go test ./internal/llm/providers/anthropic -run 'ToolUse_StartInputPlusDelta_NoDuplicateJSON|ToolUse_DeltaOnly_ValidJSON' -count=1`

Expected: first test fails on malformed/duplicated args or invalid JSON.

**Step 3: Add event-level assertions for tool-call IDs and finish reasons**

In the same tests, assert:
- tool-call IDs are stable across start/delta/end events
- finish reason resolves to `tool_calls` when tool calls exist

**Step 4: Capture and summarize exact failure mechanism**

Write a short investigation note with:
- failing sequence
- current adapter behavior
- expected behavior
- minimal fix options (A/B) with tradeoffs

Path: `docs/plans/2026-02-10-kimi-tool-call-investigation-notes.md`

**Step 5: Commit investigation assets**

```bash
git add internal/llm/providers/anthropic/adapter_test.go internal/llm/providers/anthropic/testdata/kimi_tool_call_sequences.ndjson docs/plans/2026-02-10-kimi-tool-call-investigation-notes.md
git commit -m "test(kimi): reproduce tool-call argument corruption from mixed start-input and input_json_delta"
```

---

### Task 2: Fix Kimi/Anthropic Adapter Tool-Arg Assembly (Based on Investigation)

**Files:**
- Modify: `internal/llm/providers/anthropic/adapter.go`
- Modify: `internal/llm/providers/anthropic/adapter_test.go`

**Step 1: Write the failing test for the chosen fix path**

If Task 1 produced two candidate fixes, codify the selected one as a single precise failing test first.

**Step 2: Implement minimal adapter change**

Implement only the smallest fix that satisfies Task 1 findings. Expected shape:
- track per-block arg source state (`from_start_input` vs `from_delta`)
- when first `input_json_delta` arrives after preloaded `input`, do not concatenate incompatible representations
- preserve tool-call ID/name integrity

**Step 3: Run focused provider tests**

Run: `go test ./internal/llm/providers/anthropic -run 'ToolUse|Stream|Kimi|input_json_delta' -count=1`

Expected: all targeted tests pass.

**Step 4: Run full provider package tests**

Run: `go test ./internal/llm/providers/anthropic -count=1`

Expected: pass.

**Step 5: Commit adapter fix**

```bash
git add internal/llm/providers/anthropic/adapter.go internal/llm/providers/anthropic/adapter_test.go
git commit -m "fix(kimi): prevent tool-call argument corruption when stream mixes start input and input_json_delta"
```

---

### Task 3: End-to-End Regression for Agent Loop Tool Round-Trips

**Files:**
- Modify: `internal/attractor/engine/kimi_zai_api_integration_test.go`
- Modify: `internal/agent/session_dod_test.go`

**Step 1: Add failing integration test covering tool round-trip continuity**

Add a test that simulates:
- assistant emits tool calls
- tool results are returned
- follow-up request is accepted (no missing `tool_call_id` error)

Test names:
- `TestKimiAgentLoop_ToolRoundTrip_DoesNotDropToolResponses`

**Step 2: Run targeted failing test**

Run: `go test ./internal/attractor/engine -run 'KimiAgentLoop_ToolRoundTrip_DoesNotDropToolResponses' -count=1`

Expected: fails pre-fix or without Task 2 changes; passes after Task 2.

**Step 3: Add defensive session-level test for malformed tool args handling**

Ensure malformed args still produce a corresponding tool result for each emitted tool call ID.

**Step 4: Run package tests**

Run:
- `go test ./internal/agent -run 'Tool|Session' -count=1`
- `go test ./internal/attractor/engine -run 'Kimi|Zai|Failover' -count=1`

**Step 5: Commit end-to-end regression coverage**

```bash
git add internal/attractor/engine/kimi_zai_api_integration_test.go internal/agent/session_dod_test.go
git commit -m "test(agent-loop): add regression coverage for kimi tool-call continuity and tool-result pairing"
```

---

### Task 4: Harden Preflight to Cover Failover Targets and Tool-Path Reality

**Files:**
- Modify: `internal/attractor/engine/provider_preflight.go`
- Modify: `internal/attractor/engine/provider_preflight_test.go`
- Modify: `internal/attractor/engine/config.go` (only if a new preflight knob is added)
- Modify: `internal/attractor/engine/config_test.go` (if config schema changes)

**Step 1: Add failing tests for failover target probing**

Add tests asserting preflight probes include runtime failover API providers/models, not only providers directly present on box nodes.

Test names:
- `TestProviderPreflight_PromptProbe_IncludesFailoverTargets`
- `TestProviderPreflight_PromptProbe_FailoverModelSelectionMatchesRuntime`

**Step 2: Implement failover-aware probe target expansion**

In preflight provider/model collection, include closure over configured failover chain for used API providers.

**Step 3: Add/extend probe mode for tool-capability realism**

At minimum, ensure `agent_loop` probes for providers that run `agent_loop` nodes include tool definitions and verify successful completion for both transports.

**Step 4: Run preflight tests**

Run: `go test ./internal/attractor/engine -run 'ProviderPreflight|PromptProbe' -count=1`

Expected: pass, with new failover-aware assertions.

**Step 5: Commit preflight hardening**

```bash
git add internal/attractor/engine/provider_preflight.go internal/attractor/engine/provider_preflight_test.go internal/attractor/engine/config.go internal/attractor/engine/config_test.go
git commit -m "fix(preflight): probe failover targets and strengthen agent-loop tool-path coverage"
```

---

### Task 5: Make Failover Policy Explicit (No Surprise OpenAI)

**Files:**
- Modify: `internal/attractor/engine/provider_runtime.go`
- Modify: `internal/attractor/engine/provider_runtime_test.go`
- Modify: `demo/rogue/run.yaml`

**Step 1: Add failing tests for explicit empty failover override semantics**

Current behavior treats `failover: []` as absent and falls back to builtins. Add tests requiring:
- `failover: []` means no failover for that provider
- omitted `failover` keeps builtin default behavior

**Step 2: Implement nil-vs-empty failover semantics**

In runtime resolution:
- if `pc.Failover != nil`, use configured list exactly (even empty)
- else fallback to builtin failover

**Step 3: Configure rogue run failover explicitly**

In `demo/rogue/run.yaml`:
- `llm.providers.kimi.failover: [zai]`
- `llm.providers.zai.failover: []`

**Step 4: Run runtime/config tests**

Run: `go test ./internal/attractor/engine -run 'ProviderRuntime|LoadRunConfig|Failover' -count=1`

**Step 5: Commit explicit failover policy**

```bash
git add internal/attractor/engine/provider_runtime.go internal/attractor/engine/provider_runtime_test.go demo/rogue/run.yaml
git commit -m "feat(failover): honor explicit empty failover overrides and pin rogue run failover chain"
```

---

### Task 6: Add Toolchain Readiness Gate for `wasm-pack` and Build Tools

**Files:**
- Modify: `demo/rogue/rogue_fast.dot`
- Modify: `skills/english-to-dotfile/SKILL.md`

**Step 1: Add early toolchain check nodes in `rogue_fast.dot`**

Add `shape=parallelogram` tool node near start:
- command verifies `cargo` and `wasm-pack` presence
- fail fast with actionable error text before analysis/implementation spend

Suggested check command:
```bash
command -v cargo >/dev/null && command -v wasm-pack >/dev/null
```

**Step 2: Add retry/failure routing for toolchain gate**

- success -> existing flow
- fail -> explicit remediation node or exit with reason

**Step 3: Update dotfile skill guidance**

Add anti-pattern guidance: include explicit toolchain gate when deliverable requires non-default tooling (e.g., `wasm-pack`, `playwright`, platform SDKs).

**Step 4: Validate graph structure**

Run parser/engine tests relevant to node shape handling:
- `go test ./internal/attractor/engine -run 'ToolHandler|Conditional|EdgeSelection' -count=1`

**Step 5: Commit toolchain gate changes**

```bash
git add demo/rogue/rogue_fast.dot skills/english-to-dotfile/SKILL.md
git commit -m "chore(rogue): add early toolchain readiness gate and dotfile guidance"
```

---

### Task 7: Make Command Timeout Policy Configurable Per Node/Graph

**Files:**
- Modify: `internal/attractor/engine/codergen_router.go`
- Modify: `internal/attractor/engine/codergen_router_test.go` (or add new tests in `codergen_failover_test.go` if that is where session config assertions live)
- Modify: `demo/rogue/rogue_fast.dot`

**Step 1: Add failing tests for timeout propagation to session config**

Test cases:
- node attr `default_command_timeout_ms` overrides session default
- node attr `max_command_timeout_ms` overrides cap
- graph-level fallback applies when node attr absent

**Step 2: Implement attr parsing in agent-loop setup**

In `runAPI`/agent-loop session config wiring:
- read node attrs first
- fallback to graph attrs
- set `SessionConfig.DefaultCommandTimeoutMS` / `MaxCommandTimeoutMS`

**Step 3: Set practical timeout defaults in `rogue_fast.dot`**

Set graph attrs for compile-heavy flow, e.g.:
- `default_command_timeout_ms=60000`
- `max_command_timeout_ms=600000`

**Step 4: Run focused tests**

Run:
- `go test ./internal/attractor/engine -run 'CodergenRouter|AgentLoop|timeout' -count=1`
- `go test ./internal/agent -run 'ShellTool_CapsTimeout' -count=1`

**Step 5: Commit timeout policy wiring**

```bash
git add internal/attractor/engine/codergen_router.go internal/attractor/engine/codergen_router_test.go demo/rogue/rogue_fast.dot
git commit -m "feat(agent-loop): make shell timeout policy configurable via node/graph attrs"
```

---

### Task 8: Reduce Loop Spend and Restart Stale Retry Cycles

**Files:**
- Modify: `internal/agent/session.go`
- Modify: `internal/agent/session_dod_test.go`
- Modify: `demo/rogue/rogue_fast.dot`
- Modify: `skills/english-to-dotfile/SKILL.md`

**Step 1: Add failing tests for repeated malformed tool-call loop termination**

Add tests where identical malformed tool calls repeat and assert session returns deterministic failure before exhausting large turn budgets.

Suggested test name:
- `TestSession_RepeatedMalformedToolCalls_FailsFast`

**Step 2: Implement bounded repeated-tool-error guard**

Add session-level guard (configurable threshold) that stops on repeated identical tool-call failures instead of only injecting steering forever.

**Step 3: Add `loop_restart=true` to long backward retry edges in rogue graph**

At minimum on edges that jump back to earlier heavy nodes, e.g.:
- `check_analysis -> impl_analysis`
- `check_architecture -> impl_architecture`
- `check_scaffold -> impl_scaffold`
- `check_integration -> impl_integration`
- `check_qa -> impl_qa`
- `check_review -> impl_integration`

**Step 4: Update skill guidance for loop restarts**

Add explicit recommendation: use `loop_restart=true` when retry edge jumps to significantly earlier nodes or when context drift is likely.

**Step 5: Run tests**

Run:
- `go test ./internal/agent -run 'Loop|MalformedTool|MaxTurns' -count=1`
- `go test ./internal/attractor/engine -run 'LoopRestart|DeterministicFailureCycle|NextHop' -count=1`

**Step 6: Commit loop-spend hardening**

```bash
git add internal/agent/session.go internal/agent/session_dod_test.go demo/rogue/rogue_fast.dot skills/english-to-dotfile/SKILL.md
git commit -m "fix(reliability): fail fast on repeated malformed tool loops and restart long retry cycles"
```

---

### Task 9: End-to-End Validation in Fresh Rogue-Fast Run

**Files:**
- Modify: `docs/plans/2026-02-10-rogue-fast-run-hardening.md` (append execution results)
- Create: `docs/plans/2026-02-10-rogue-fast-run-hardening-validation.md`

**Step 1: Rebuild binary from HEAD**

Run:
```bash
go build -o ./kilroy ./cmd/kilroy
```

**Step 2: Launch fresh rogue-fast run with updated graph/config**

Use approved production command format (no unapproved flag changes).

**Step 3: Validate early gate behavior**

Confirm toolchain check fails fast if `wasm-pack` missing, otherwise proceeds.

**Step 4: Validate failover behavior and provider usage**

Inspect run artifacts:
- no implicit OpenAI failover unless explicitly configured
- ZAI calls remain on `glm-4.7`

**Step 5: Validate Kimi tool-call continuity**

Inspect stage `events.ndjson` for:
- no repeated `invalid tool arguments JSON`
- no Kimi “missing tool_call_id response” errors

**Step 6: Validate retry-loop behavior**

Inspect `progress.ndjson`:
- retry edges with `loop_restart=true` show restart behavior
- no long repeated deterministic loops with escalating spend

**Step 7: Capture validation report**

Write summary with run_id, pass/fail checks, and any residual risks.

**Step 8: Final commit for validation docs (if needed)**

```bash
git add docs/plans/2026-02-10-rogue-fast-run-hardening-validation.md docs/plans/2026-02-10-rogue-fast-run-hardening.md
git commit -m "docs(validation): record rogue-fast hardening run outcomes"
```

---

## Final Verification Checklist

Run before merge:

```bash
go test ./internal/llm/providers/anthropic -count=1
go test ./internal/agent -count=1
go test ./internal/attractor/engine -count=1
```

Run one fresh rogue-fast execution and verify artifacts:
- no turn-limit-driven cross-provider failover
- no Kimi tool-call continuity failures
- no unsupported ZAI model IDs
- expected failover chain exactly matches `demo/rogue/run.yaml`
- toolchain failures surface in the first gate node, not late integration

---

## Execution Results (2026-02-10)

Implemented and validated Tasks 4-8 in this worktree with full package tests:

- `go test ./internal/agent -count=1` -> pass
- `go test ./internal/attractor/engine -count=1` -> pass
- `dot -Tsvg demo/rogue/rogue_fast.dot -o /dev/null` -> pass
- `go run ./cmd/kilroy attractor validate --graph demo/rogue/rogue_fast.dot` -> pass

Validation report:

- `docs/plans/2026-02-10-rogue-fast-run-hardening-validation.md`

Fresh production run status:

- Not executed in this turn (pending explicit exact production command approval).
