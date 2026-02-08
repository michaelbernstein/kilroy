# Attractor Reliability Troubleshooting

This guide covers reliability guardrails in Attractor and where to inspect evidence when runs fail.

## Failure Classes

Attractor classifies failures into a minimal v1 taxonomy:

- `transient_infra`: temporary transport/infrastructure failures (timeouts, connection reset, rate limits, temporary unavailable).
- `deterministic`: contract/configuration/input failures that should not be retried as-is.

Classification is fail-closed. Unknown/unset classes are treated as `deterministic`.

Canonical metadata keys:

- `failure_class`
- `failure_signature`

These keys are attached where policy decisions consume outcomes (CLI failures, fan-in all-fail aggregation, retry/restart decision points).

## Retry and Restart Policy

Stage retries and `loop_restart` share one failure-policy layer:

- Deterministic failure:
  - stage retry blocked immediately
  - `loop_restart` blocked
- Transient failure:
  - stage retry allowed (subject to `max_retries`)
  - `loop_restart` allowed (subject to limits below)

`loop_restart` protections:

- `max_restarts`: hard cap on total restarts.
- `restart_signature_limit` (default `3`): circuit breaker for repeated identical transient signatures.

When the circuit breaker trips, terminal failure reason includes class/signature/count/threshold context.

## Fatal-Path Finalization Guarantees

Attractor now centralizes terminal finalization. Both success and failure terminal paths write:

- `final.json`
- `run.tgz` (best-effort)

Failure `final.json` includes:

- `status=fail`
- non-empty `failure_reason`

Terminal CXDB turn behavior:

- success: `com.kilroy.attractor.RunCompleted`
- failure: `com.kilroy.attractor.RunFailed`

## Provider CLI Contract Preflight

Before run/resume execution, provider CLI contracts are preflighted:

- executable presence is required for each CLI-backed provider used by the graph
- selected capabilities are probed (for example Anthropic `--verbose`)
- contract mismatches fail fast before graph execution

Anthropic `--verbose` handling:

- appended only when preflight indicates support
- omitted when unsupported, with warning

## Relative State Path Normalization

For CLI subprocesses, these env vars are normalized to absolute paths when relative:

- `CODEX_HOME`
- `CLAUDE_CONFIG_DIR`
- `GEMINI_CONFIG_DIR`

Applied overrides are recorded in stage `cli_invocation.json` under `env_path_overrides`.

## Evidence Checklist

When debugging a failure, inspect in this order:

1. `<logs_root>/final.json`
2. `<logs_root>/<node_id>/status.json`
3. `<logs_root>/<node_id>/cli_invocation.json`, `stdout.log`, `stderr.log`
4. `<logs_root>/<parallel_node>/parallel_results.json` (fan-out/fan-in cases)
5. `<logs_root>/checkpoint.json`
6. `<logs_root>/manifest.json`

Look for:

- `failure_reason` text that preserves provider/tool stderr signal
- `failure_class`/`failure_signature` metadata
- loop restart progress and signature-limit trip details

## Common Remediation

- Deterministic preflight failure:
  - install/fix provider CLI binary path
  - align CLI version with expected flags/capabilities
- Deterministic runtime failure:
  - fix configuration/contract mismatch before retrying
- Transient loop/circuit failures:
  - validate upstream stability (network/provider)
  - adjust `max_retries`, `max_restarts`, `restart_signature_limit` only with clear operational justification
