# Native LSP navigation roadmap

Status: non-authoritative working note; accepted direction recorded in
[`docs/lsp-plan.md`](../lsp-plan.md)

This note captures the implementation shape discussed after the completed
single-provider definition milestone. It is intentionally not a second
contract.

## Current direction

- Keep the LSP process entirely user-configured and server-neutral.
- Replace the alpha single-definition assumption with repeatable selectors.
- Let selectors match workspace-relative paths by `language-id` and optional
  `**`-style patterns; omitted patterns use `**/*`.
- Fan out navigation requests to every matching capable provider, preserve
  provider/result order and duplicates, and attribute each location.
- Represent partial provider failures structurally rather than producing noisy
  client stderr diagnostics.
- Add declaration, type-definition, implementation, references, and contextual
  status alongside the existing definition command.
- Keep `implementation-id` optional descriptive metadata; observed runtime
  identity comes from initialization when available.

## Deliberately deferred

Dynamic completion, mutating operations, arbitrary initialization options,
language catalogs, executable discovery, manual provider overrides, unsaved
buffers, workspace settings, dynamic registration, and server-applied edits
remain outside this slice.

## Configuration spelling

Current MCP definitions use `mcp "name"`. Historical validation notes may show
`server "name"`; those examples predate the alpha rename and remain unchanged
to preserve their evidence.
