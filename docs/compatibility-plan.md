# MCP Compatibility Qualification Milestone

Status: authoritative current milestone

## Objective

Qualify the remaining deployed MCP protocol layers required for the first full
MVP while preserving one shell-facing command, result, error, and daemon
contract across protocol eras.

The milestone proceeds newest to oldest:

1. establish and retain the modern `2026-07-28` baseline;
2. qualify legacy initialized MCP over stdio and Streamable HTTP; and
3. qualify legacy HTTP+SSE associated with `2024-11-05`.

The official Go SDK remains the default owner of negotiation, initialization,
pagination, calls, shutdown, authorization, and transport behavior. Protocol
age alone does not justify local protocol implementations or parallel adapter
stacks.

## Current baseline

Modern stdio and stateless Streamable HTTP work in direct and daemon-backed
modes. The public CLI supports server and tool discovery, focused schema help,
projected top-level arguments, raw JSON overlays, exact JSON calls, structured
results and errors, and retained daemon sessions. Cold Streamable HTTP calls
prime the SDK's schema cache through its public `Tools` iterator so SDK-owned
features such as `x-mcp-header` work without local header logic.

Legacy initialized stdio and stateful Streamable HTTP are qualified against the
isolated v1.6.1 SDK fixture. Both initialized at `2025-11-25`; retained daemon
sessions preserved process/session-local state across CLI processes, fresh
direct sessions remained isolated, and reload retired both instances. Exact
commands and outputs are recorded in
`docs/notes/legacy-initialized-validation.md`.

## Implementation sequence

### 1. Establish the fixture matrix

Use official SDK examples, conformance fixtures, or minimal SDK-backed fixtures
for every combination under test. Record the exact server revision, transport,
startup command, and SDK behavior. Prefer existing fixtures; add local MCP logic
only for a confirmed fixture or SDK gap.

### 2. Qualify legacy initialized stdio

Exercise discovery, focused help, projected and exact calls, actionable tool
errors, clean shutdown, and retained state across separate daemon-backed CLI
processes. Confirm that SDK negotiation selects the intended legacy revision and
that Wirecmd exposes no era-specific public syntax or result shape.

### 3. Qualify legacy initialized Streamable HTTP

Exercise the same observable contract in direct and daemon-backed modes,
including pagination, session retention where the upstream requires it,
cancellation, connection failure, and schema-dependent SDK transport behavior.
Do not infer support from modern stateless HTTP success.

### 4. Qualify legacy HTTP+SSE

First verify whether the pinned SDK exposes a supported high-level client
transport for the target revision. If it does, integrate that transport through
the existing semantic execution boundary. If it does not, preserve the exact
SDK evidence and ask before adding a dependency, changing the configuration
contract, or implementing a narrow transport shim.

### 5. Run cross-era contract checks

For every qualified combination, run the same discovery and invocation cases
through direct and daemon-backed execution where both are meaningful. Compare
stdout envelopes, stderr discipline, error categories, exit codes, argument
representation, continuity, and cleanup. Era-specific facts may appear in
diagnostics but must not create separate agent-facing command families.

## Acceptance criteria

The milestone passes when:

- each of the three compatibility layers has a recorded, reproducible fixture
  and observed protocol revision;
- supported stdio and HTTP combinations can discover and invoke tools using the
  existing public CLI contract;
- stateful combinations retain continuity across separate daemon-backed CLI
  processes and retire cleanly on reload or shutdown;
- direct and daemon-backed modes remain behaviorally equivalent where retained
  state does not intentionally differ;
- SDK-owned negotiation, pagination, transport behavior, and lifecycle are not
  duplicated locally;
- unsupported combinations fail with deterministic structured errors and an
  actionable recovery path; and
- full tests, race tests, vet, module verification, diff checks, and real
  fixture qualification pass.

If the pinned SDK cannot support a required layer through a credible public
API, stop with the evidence and deliberate the smallest alternative before
changing dependencies, configuration, or architecture.

## Explicitly deferred

- OAuth and browser-based authorization flows.
- Typed HTTP query, header, and credential configuration.
- Automatic configuration discovery, templates, and additional secret
  providers.
- Detached daemon startup, service-manager integration, watchers, idle
  eviction, automatic recovery, and pool widths above one.
- Resources, prompts, subscriptions, Tasks, sampling, and richer elicitation.
- MCP server/proxy mode and non-MCP upstream adapters.
- Packaging and release automation.

These remain valid future slices but are not prerequisites for determining
whether the first MVP can span the intended deployed MCP protocol eras.
