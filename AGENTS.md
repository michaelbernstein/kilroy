# CRITICAL NOTE: Kilroy Attractor Metaspec Execution

When you are executing `docs/strongdm/attractor/kilroy-metaspec.md`, you MUST:

- Find the next requirement that is not yet implemented, and implement it.
- Continue iterating without stopping until the implementation is comprehensive and complete.
- Do not ask the user questions while executing the metaspec. Use local inspection, tool calls, and web lookups to resolve uncertainty and proceed.
- Defer setting up/running CXDB locally until after the Attractor implementation work is complete.

## Open Files In Editors (CLI)

- Cursor: `cursor -r -g "path/to/file:line[:col]"`
- VS Code: `code -r -g "path/to/file:line[:col]"`

## Long-Running Attractor Launch

- Prefer built-in detached mode so the launcher exits safely while the run continues:
  `./kilroy attractor run --detach --graph <graph.dot> --config <run_config.json> --run-id <run_id> --logs-root <logs_root>`
- Monitor progress and completion:
  - `tail -f <logs_root>/progress.ndjson`
  - `cat <logs_root>/final.json`
