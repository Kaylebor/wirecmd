# MCP Compatibility Qualification Record

Status: authoritative completed supported-protocol qualification; legacy HTTP+SSE deferred at the SDK boundary

## Objective

Record the qualified legacy initialized stdio and Streamable HTTP protocol
layers that the pinned official SDK supports today, while preserving one
shell-facing command, result, error, and daemon contract across those eras.
Legacy HTTP+SSE remains a full-MVP target, but is deferred until the official
SDK exposes the required stable client API.

Qualification covered the deployed layers newest to oldest:

1. establish and retain the modern `2026-07-28` baseline;
2. qualify legacy initialized MCP over stdio and Streamable HTTP; and
3. defer legacy HTTP+SSE associated with `2024-11-05` pending a stable SDK
   release containing `ClientSessionOptions.ProtocolVersion`.

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

Legacy HTTP+SSE is not a configured or implemented transport in this release.
The v1.7.0 SDK cannot connect to the isolated v1.6.1 historical SSE handler:
the handler rejects the initial modern `server/discover` request with HTTP 400,
closing the connection before legacy initialization can run. The pinned stable
SDK has no public protocol-selection option. The removed implementation and
upstream tracking evidence are recorded in `docs/notes/legacy-sse-validation.md`.
Do not add a local shim or restore the public transport surface. Re-evaluate
only after a stable official SDK release contains
`ClientSessionOptions.ProtocolVersion` from upstream PR #1127.

## Qualification result

The supported compatibility qualification is complete. Modern MCP, legacy
initialized stdio, and legacy initialized Streamable HTTP use the same direct
and daemon-backed shell contract, with the recorded fixtures demonstrating
discovery, invocation, retained state where applicable, and clean reload.
Legacy HTTP+SSE remains deferred at the SDK boundary and is not exposed in
configuration or runtime behavior. Automatic configuration discovery is now
tracked by the authoritative [discovery plan](discovery-plan.md).

## Reproduction sequence

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

### 4. Re-evaluate deferred legacy HTTP+SSE

After a stable official SDK release contains
`ClientSessionOptions.ProtocolVersion` from PR #1127, first update the SDK by
the normal dependency decision process. Then requalify the historical fixture
with the legacy revision explicitly requested through the SDK. Do not restore
an SSE configuration or runtime path before that qualification succeeds.

### 5. Run cross-era contract checks

For every qualified combination, run the same discovery and invocation cases
through direct and daemon-backed execution where both are meaningful. Compare
stdout envelopes, stderr discipline, error categories, exit codes, argument
representation, continuity, and cleanup. Era-specific facts may appear in
diagnostics but must not create separate agent-facing command families.

## Acceptance criteria

The completed supported qualification passes because:

- legacy initialized stdio and Streamable HTTP have recorded, reproducible
  fixtures and observed protocol revisions;
- those supported combinations can discover and invoke tools using the existing
  public CLI contract;
- stateful combinations retain continuity across separate daemon-backed CLI
  processes and retire cleanly on reload or shutdown;
- direct and daemon-backed modes remain behaviorally equivalent where retained
  state does not intentionally differ;
- SDK-owned negotiation, pagination, transport behavior, and lifecycle are not
  duplicated locally;
- unsupported combinations fail with deterministic structured errors and an
  actionable recovery path; and
- full tests, race tests, vet, module verification, diff checks, and their real
  fixture qualification pass.

If the pinned SDK cannot support a required layer through a credible public
API, stop with the evidence and deliberate the smallest alternative before
changing dependencies, configuration, or architecture.

## Explicitly deferred

- OAuth and browser-based authorization flows.
- Static typed HTTP query, header, and credential configuration is specified
  by the subsequent
  [HTTP values plan](http-values-plan.md).
- Legacy HTTP+SSE, until a stable official Go SDK release includes
  `ClientSessionOptions.ProtocolVersion` (PR #1127); then requalify against the
  historical fixture before exposing it in configuration or runtime behavior.
- Templates and additional secret providers.
- Detached daemon startup, service-manager integration, watchers, idle
  eviction, automatic recovery, and pool widths above one.
- Resources, prompts, subscriptions, Tasks, sampling, and richer elicitation.
- MCP server/proxy mode and non-MCP upstream adapters.
- Packaging and release automation.

These remain valid future slices but are not prerequisites for the completed
supported-protocol qualification or the current configuration-discovery
milestone.
