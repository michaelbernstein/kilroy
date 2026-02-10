# Rogue-Fast Hardening Validation (2026-02-10)

## Scope

Validate implementation of Tasks 4-9 from `docs/plans/2026-02-10-rogue-fast-run-hardening.md` on branch `plan/rogue-fast-hardening-20260210`.

## Code and Graph Validation

- DOT parse check: `dot -Tsvg demo/rogue/rogue_fast.dot -o /dev/null` -> pass
- Attractor graph validate: `go run ./cmd/kilroy attractor validate --graph demo/rogue/rogue_fast.dot` -> pass (`ok: rogue_fast.dot`)

## Test Validation

- `go test ./internal/agent -count=1` -> pass
- `go test ./internal/attractor/engine -count=1` -> pass
- `go test ./internal/attractor/engine -run 'ProviderRuntime|LoadRunConfig|Failover|ProviderPreflight|PromptProbe|CodergenRouter|AgentLoop|timeout|ToolHandler|Conditional|EdgeSelection|LoopRestart|DeterministicFailureCycle|NextHop' -count=1` -> pass
- `go test ./internal/agent -run 'ShellTool_CapsTimeout|Loop|MalformedTool|MaxTurns' -count=1` -> pass

## Task-by-Task Outcome

- Task 4 (failover-aware preflight prompt probing): pass
- Task 5 (explicit failover policy, nil-vs-empty semantics, rogue run chain): pass
- Task 6 (early toolchain readiness gate + skill guidance): pass
- Task 7 (node/graph command timeout propagation): pass
- Task 8 (fail-fast malformed tool-call loops + loop_restart on long back-edges): pass
- Task 9 (validation documentation): pass (this document)

## Production Run Validation Status

- Fresh production rogue-fast run: **not executed in this turn**
- Reason: no explicit user-approved production command was provided in this step.
- Follow-up needed after merge/rebase:
  - run one fresh production rogue-fast execution with approved exact command
  - record `run_id`, provider usage, loop_restart events, and failure/success checks

## Residual Risks

- Runtime behavior against live providers (latency, provider-specific transport quirks, real failover behavior) remains unverified until a fresh production run is executed.
