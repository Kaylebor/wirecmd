# Exploratory Design Notes

Status: non-authoritative working notes

Nothing in this file is a committed requirement. Ideas must be validated and
explicitly promoted into `docs/product-thesis.md` or
`docs/validation-plan.md` before they direct implementation.

This file condenses an exploratory design discussion dated 18 August 2026. The
original discussion ranged well beyond the first validation milestone and
included overlapping alternatives and repeated section numbering.

## Original concept

Expose configured MCP servers through a predictable shell-native CLI so a
harness needs only shell access and a Skill explaining discovery. The CLI is an
MCP client, not primarily an MCP server. Humans, agents, scripts, CI, editors,
and other local tools should be able to share the interface.

Candidate discovery shape:

```text
mcp list
mcp <server> --help
mcp <server> <tool> --help
```

Candidate lossless invocation paths:

```text
mcp <server> <tool> --json '{...}'
echo '{...}' | mcp <server> <tool> --stdin
```

Possible result modes included human rendering, a normalized JSON envelope,
structured data only, and minimally transformed raw MCP results.

## Configuration ideas

- A global XDG configuration plus project configuration discovered by walking
  upward from the caller's directory.
- Broad-to-specific merging with provenance retained for diagnostics.
- Separate `CWD`, per-file `CONFIG_DIR`, and an effective semantic `ROOT`.
- Explicit root override and diagnostics such as effective configuration,
  sources, and explanation of a value.
- KDL 2 as a possible canonical format, normalized behind an internal model.
- Static configuration topology with templates limited to explicitly enabled
  runtime values.
- Generic literal and secret-bearing value sources, starting with environment
  lookup, plus arbitrary vendor HTTP headers and query parameters.

These ideas are attractive but mostly deferred until the validation spike shows
that the product interaction model is worth supporting.

## Daemon and lifecycle ideas

- One dual-purpose executable with foreground and detached daemon commands.
- A stateless local request boundary with upstream connections, processes,
  negotiated state, and inventories retained internally.
- Managed instance identity based on server alias, scope, auth identity, and an
  effective configuration fingerprint.
- Global, workspace, exact-CWD, or custom scope.
- Idle cleanup that does not pretend lost semantic state survived.
- Optional pools with per-instance concurrency and affinity, initially limited
  to one instance unless evidence supports more.
- Private per-user local IPC and a CLI/daemon compatibility handshake.
- Explicit reload instead of filesystem watchers in an initial version.

The daemon was intended as an optional accelerator and lifecycle broker. It
should not become the public capability protocol.

## Authentication and interaction ideas

- Canonical commands are deterministic and non-interactive.
- Ordinary calls return structured authentication or input-required states
  rather than prompting or opening a browser.
- An explicit interactive command may own an OAuth flow when no daemon exists.
- With a daemon, the daemon can retain callback and token-refresh state after an
  ephemeral shell command exits.
- Resolved secrets remain semantically distinct for redaction, logging,
  fingerprinting, and process injection.

## Protocol research snapshot

The discussion identified three broad compatibility layers:

1. modern stateless MCP beginning with protocol revision `2026-07-28`;
2. legacy initialized MCP over stdio or Streamable HTTP for the 2025 revisions;
3. legacy HTTP+SSE associated with `2024-11-05`.

The official Go SDK was preferred so revision negotiation, transport behavior,
authorization, and future protocol fixes would not be reimplemented locally.
Tool listing and calls were treated as the important common denominator.

This is a dated research snapshot. Exact revisions, SDK versions, and support
claims must be checked against current primary sources before implementation.

## Other explored possibilities

- Schema-derived flags with the exact JSON path as the authority.
- Preservation of schema metadata for future shell completion.
- Skill installation associated with configured capability sources.
- Normalization of legacy elicitation and modern multi-round-trip input into a
  common agent-actionable representation.
- Resources and prompts behind the same semantic client boundary.
- A future out-of-process or WASM extension mechanism rather than native Go
  plugins.
- Optional task UX, subscriptions, completion, and extension passthrough.
- Optional compatibility mode exposing aggregated capabilities as an MCP
  server.

## Anti-overengineering reminders from the discussion

- Do not add a configuration trust database or approval framework without a
  concrete need.
- Do not make configuration templates into a programming language.
- Do not parse or reinterpret opaque configuration files materialized for an
  upstream server.
- Do not build automatic config watchers or dependency graphs initially.
- Do not sanitize the inherited environment beyond applying isolated
  per-process overrides.
- Do not attempt to prove explicitly pooled instances are interchangeable.
- Do not build elaborate CLI flag collision machinery for hypothetical schemas.
- Do not duplicate protocol behavior already owned by the official SDK.
- Fail visibly when required semantic continuity is lost.
- Do not pre-build recovery, autoscaling, policy, or extension systems.

## Open questions

- What should the executable and project be called if MCP is only the first
  upstream adapter?
- How small can the universal Skill remain while still producing reliable
  discovery and recovery?
- Does schema-derived help work better for agents as conventional flags,
  TypeScript-like signatures, JSON Schema, examples, or a layered combination?
- What normalized result and error envelope yields reliable recovery without
  hiding useful upstream details?
- Is hierarchical configuration important to the first successful agent trial,
  or only to realistic multi-project adoption?
- Which existing CLI provides the fairest baseline for the spike?
