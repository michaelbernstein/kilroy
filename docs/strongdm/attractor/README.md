# Attractor 

This repository contains nlspecs to build your own version of Attractor to create your own software factory.

Although bringing your own agentic loop and unified LLM SDK is not required to build your own Attractor, we highly recommend controlling the stack so you have a strong foundation.

## Specs

- [Attractor Specification](./attractor-spec.md)
- [Coding Agent Loop Specification](./coding-agent-loop-spec.md)
- [Unified LLM Client Specification](./unified-llm-spec.md)

## Runbook Notes

- Canonical `status.json` contract:
  - `status` values `fail` and `retry` must include a non-empty `failure_reason`.
  - Legacy worktree payloads that only provide `outcome` + `details` are normalized by the runtime decoder, but emitters should still write canonical `failure_reason` directly.
- OpenAI codex CLI invocation:
  - Default args use `codex exec --json --sandbox workspace-write ...`.
  - Deprecated `--ask-for-approval` is intentionally not used.
- Codex schema behavior:
  - Structured output schema is strict (`required: ["final","summary"]`, `additionalProperties: false`).
  - If codex rejects schema validation (`invalid_json_schema`-class errors), Attractor retries once without `--output-schema` and records fallback metadata in stage artifacts.
- Loop safety:
  - `failure_class` defaults fail-closed to `deterministic` when unknown.
  - Stage retry and `loop_restart` both use the same class-aware policy:
    - `transient_infra`: retries/restarts are allowed.
    - `deterministic`: retries/restarts are blocked.
  - `loop_restart` adds signature circuit-breaking via `restart_signature_limit` (default `3`) to stop repeated identical transient failures.
  - Set graph-level `max_restarts` to cap total restart attempts.
- Fatal-path terminal artifacts:
  - `final.json` is written for both success and failure outcomes.
  - Failure outcomes include `failure_reason` in `final.json`.
  - Terminal CXDB turns are emitted as `RunCompleted` (success) or `RunFailed` (failure).
- Provider CLI preflight:
  - Run and resume flows preflight required provider CLIs before execution.
  - Preflight validates executable availability and probes selected capabilities (for example Anthropic `--verbose` support).
  - Relative CLI state env paths (`CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `GEMINI_CONFIG_DIR`) are normalized to absolute paths before subprocess launch.
  - Invocation artifacts include `env_path_overrides` when normalization is applied.

See [Reliability Troubleshooting](./reliability-troubleshooting.md) for detailed diagnostics and remediation steps.
