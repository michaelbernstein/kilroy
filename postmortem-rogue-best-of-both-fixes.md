# Rogue Runs (Fast + Slow): Best-of-Both Fix Plan

This plan consolidates fixes from both rogue-fast and rogue-slow postmortems, then cross-checks them against `coding-agent-loop-spec`, `attractor-spec`, and `unified-llm-spec`.

## Core invariants (formal and testable)

- **Parent liveness invariant:** while any active fanout branch emits progress for the current run generation, parent watchdog idle time must reset.
  - Progress sources (explicit): `stage_attempt_start`, `stage_attempt_end`, `stage_progress`, `stage_heartbeat`, and branch completion events.
- **Attempt ownership invariant:** a stage attempt may read only status produced by the same `(run_id, node_id, attempt_id)` tuple.
- **Heartbeat lifecycle invariant:** no heartbeat may be emitted after `stage_attempt_end` for the same attempt tuple.
- **Cancellation convergence invariant:** after run-level cancellation is observed, branch loops must stop within `max(2 scheduler iterations, 5s wall time)`.
- **Failure-causality invariant:** routing/check nodes must preserve upstream `failure_reason`; if classification is added, preserve both raw and classified forms.
- **Terminal artifact invariant:** all controllable terminal paths (success/fail/cancel/watchdog/internal fatal) must persist top-level terminal outcome artifacts.

## Spec alignment guardrails (idiomatic Attractor/Kilroy path)

- Preserve canonical stage status contract: `{logs_root}/{node_id}/status.json` is authoritative for routing.
- Keep edge-routing semantics unchanged: condition evaluation, retry-target fallback behavior, deterministic tie-breaks.
- Preserve parallel isolation and fan-in behavior: branch-local isolation and single winner integration.
- Keep provider behavior config-driven; avoid provider-specific hardcoding in engine logic.
- Keep condition matching semantics stable: route conditions still match raw outcome/context fields (do not silently change comparison semantics).
- Status outcome parsing policy: accept legacy case variants on read, normalize to lowercase internally, and emit canonical lowercase on write.

## P0 (must fix first)

- Make watchdog liveness fanout-aware.
  - Done when: active child-branch events reset parent watchdog idle timer; no false `stall_watchdog_timeout` while branches are active.
- Add API `agent_loop` progress plumbing with explicit event parity.
  - Done when: long-running API stages emit periodic progress via explicit stage progress events mapped from loop/tool milestones (start/delta/end), not just final completion.
- Stop CLI heartbeat leaks with attempt scoping.
  - Done when: zero heartbeats are observed after attempt end for the same `(node_id, attempt_id)`.
- Add run-level cancellation guards in subgraph execution with explicit policy interaction.
  - Policy: run-level cancel always preempts branch execution regardless of branch `error_policy`; `error_policy` still governs branch-local failure handling while run is live.
  - Done when: canceled contexts terminate branch loops within cancellation convergence SLO.
- Add deterministic subgraph cycle breaker (implementation parity), without changing DOT routing semantics.
  - Done when: repeated deterministic failure signatures in subgraph path abort at configured threshold and emit explicit cycle-break event.
- Preserve failure causality through routing nodes.
  - Done when: upstream raw `failure_reason` survives check/conditional traversal and terminal outcomes.
- Harden status ingestion with explicit precedence, ownership checks, and diagnostics.
  - Precedence rule:
  - Read canonical `{logs_root}/{node_id}/status.json` first.
  - If canonical is absent, read `.ai/status.json` only as legacy fallback.
  - Accept fallback only if ownership matches current stage/attempt (when ownership fields are present).
  - Never overwrite an existing canonical status with fallback data.
  - If fallback accepted, copy atomically to canonical path with provenance marker.
  - Done when: status path selection is deterministic and fully traceable in logs.
- Guarantee top-level terminalization on all controllable paths.
  - Done when: terminal artifact exists for success/fail/watchdog cancel/context cancel/internal fatal exits.
  - Note: uncatchable hard kill (`SIGKILL`) remains best-effort.

## P1 (high-value hardening)

- Separate cancellation/stall classifications from deterministic provider/API failure classes.
  - Done when: terminal artifacts and telemetry distinguish cancel/timeouts from deterministic API failures.
- Normalize failure signatures for cycle-break decisions without mutating route-visible raw reasons.
  - Done when: engine stores both `failure_reason_raw` and normalized signature key; condition expressions remain compatible.
- Enforce strict model/fallback policy from run config.
  - Done when: pinned provider/model/no-failover configs block implicit fallback and emit explicit policy-violation diagnostics.
- Improve provider/tool adaptation (especially `apply_patch` contract handling in openai-family API profiles).
  - Done when: adapter behavior is deterministic and contract violations are surfaced as actionable errors.
- Add parent rollup telemetry for branch health.
  - Done when: operators can see branch-level liveness/failure summaries from parent stream without drilling into branch directories.

## P2 (validation and prevention)

- Fanout watchdog false-timeout regression.
  - Level: integration.
  - Matrix: `error_policy=fail_fast` and `error_policy=continue`.
- Stale heartbeat leak regression.
  - Level: unit + integration.
- Subgraph cancellation convergence regression.
  - Level: integration (timing-bound assertions).
- Subgraph deterministic cycle-break regression.
  - Level: integration.
- Status ingestion precedence/ownership regression (`status.json` vs `.ai/status.json`).
  - Level: unit + integration.
- Failure propagation through check/conditional nodes regression.
  - Level: integration.
- Terminal artifact persistence regression for all controllable terminal paths.
  - Level: integration/e2e.
- Model pin/no-failover enforcement regression.
  - Level: integration.
- True-positive watchdog timeout regression (no top-level and no branch activity).
  - Level: integration.

## Required observability

- Branch-to-parent liveness events/counters with run generation and branch identifiers.
- Attempt identifiers on all lifecycle events: `stage_attempt_start`, `stage_attempt_end`, `stage_heartbeat`, `stage_progress`.
- Status-ingestion decision events: searched paths, selected source, parse outcome, ownership validation result, canonical copy outcome.
- Subgraph cancellation-exit event including elapsed convergence time and stop node.
- Deterministic cycle-break event including signature, count, and threshold.
- Terminalization event including final status, reason/class, and artifact path.

## Spec delta proposals required (document separately)

These items are currently implementation concepts and should be codified in spec docs to avoid drift:

- Deterministic traversal-level cycle-break semantics for subgraph/main loop parity.
- Top-level terminal artifact contract (`final.json` or equivalent) and required fields.
- Optional failure classification taxonomy (if `failure_class` is retained).
- Legacy `.ai/status.json` compatibility contract and deprecation path.
- Explicit run-config failover policy semantics when provider/model is pinned.
- Outcome casing canonicalization rule (resolve uppercase/lowercase inconsistency in attractor docs).

## Suggested implementation order (risk-aware)

- If rogue-trigger frequency is non-trivial, ship minimal behavioral hotfixes first:
  - subgraph cancellation guards,
  - heartbeat lifecycle scoping,
  - fanout-aware watchdog liveness.
- Then land observability for stronger diagnosis and proof.
- Then land status-ingestion hardening + failure-causality preservation.
- Then classification/signature/tool-adaptation hardening.
- Then complete full regression matrix and release gates.

## Primary touchpoints

- `internal/attractor/engine/parallel_handlers.go`
- `internal/attractor/engine/subgraph.go`
- `internal/attractor/engine/engine.go`
- `internal/attractor/engine/codergen_router.go`
- `internal/attractor/engine/handlers.go`
- `internal/attractor/runtime/status.go`
- `internal/attractor/engine/engine_stall_watchdog_test.go`
- `internal/attractor/engine/parallel_guardrails_test.go`
- `internal/attractor/engine/parallel_test.go`
- `internal/attractor/engine/codergen_heartbeat_test.go`
- `internal/attractor/runtime/status_test.go`

## Release gates

- No stale-heartbeat events after attempt completion in canaries.
- No fanout false-timeouts in canaries with active branches.
- Deterministic loops terminate within configured thresholds.
- Cancellation convergence SLO is met.
- Every controllably terminated run persists terminal artifact with correct status/reason.
- No regressions in canonical status/routing semantics.
- No implicit provider/model fallback when config forbids it.
