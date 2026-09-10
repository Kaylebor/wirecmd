# Current Roadmap

Status: authoritative current-state handoff after v0.2.0; future items are a
decision queue, not accepted implementation milestones

## Purpose

This document gives a fresh contributor the current sequence, rationale, and
decision boundaries without requiring conversational history. Detailed feature
contracts remain in their linked authoritative plans. Items below preserve the
order in which open areas should be discussed; they do not authorize an
unspecified design or commit the project to every candidate.

The governing documents are the [product thesis](product-thesis.md),
[LSP plan](lsp-plan.md), [MCP compatibility plan](compatibility-plan.md),
[MCP resources plan](resources-plan.md),
[release-readiness contract](release-readiness.md), and
[macOS qualification plan](macos-plan.md). `AGENTS.md` is the complete index of
authoritative milestone documents and their precedence.

## Current baseline

Wirecmd v0.2.0 is a shell-native capability client and local daemon. MCP is its
first upstream adapter rather than the harness-facing abstraction. The shipped
surface includes:

- explicit `wirecmd mcp` discovery, focused help, projected and exact-JSON tool
  calls, and deterministic structured results;
- stdio and Streamable HTTP MCP through the official Go SDK, including
  transparent OAuth and encrypted credential persistence;
- composed KDL configuration, trusted workspace discovery, retained sessions,
  terminal-aware output, and Fish completion;
- MCP resource and resource-template discovery plus resource reads; and
- selector-routed native LSP navigation, hover, signature help, document
  symbols, and workspace symbols across multiple providers.

Modern and legacy initialized stdio and Streamable HTTP are qualified. Legacy
HTTP+SSE is not implemented and remains deferred at the SDK boundary described
below.

Normal operations remain daemon-backed and must fail clearly when the daemon is
offline. `--direct` is the explicit one-shot path. SDK protocol types and MCP
revision details must not leak into the public semantic command/result contract.

## Decision queue

### 1. LSP runtime configuration prerequisite

Do not begin an LSP diagnostics slice before determining a server-neutral
representation for `initializationOptions`, workspace settings, and related
structured values in KDL. Also determine how Wirecmd observes asynchronous or
indefinitely running LSP behavior without assuming a particular language
server.

This requires a synchronous design decision before an implementation plan. Do
not hardcode a server catalog, infer language servers from executables or
extensions, introduce another configuration format, or select a generic
KDL-to-JSON/template mechanism without explicit deliberation.

### 2. Candidate LSP diagnostics and later edit-oriented operations

Once the runtime-configuration and asynchronous-notification boundaries are
settled, diagnostics are the leading candidate for the next LSP slice. Rename,
code actions, and other edit-oriented operations remain later candidates
because LSP commonly returns edits for a client to apply; their preview,
conflict, filesystem, and agent-tool semantics require a separate decision
rather than an assumed mini-editor implementation. Any promoted LSP milestone
requires deterministic fixtures and a real explicitly configured server
qualification; `gopls` may be a test target but must never become production
routing knowledge.

### 3. Candidate additional MCP primitives

Evaluate prompts, subscriptions, Tasks, sampling, and richer elicitation by
concrete shell/agent value. Do not pursue protocol breadth or competitor parity
as goals by themselves. Prefer official SDK APIs and preserve the existing
semantic boundary; bring any confirmed SDK gap back for deliberation before a
shim or dependency change.

### 4. Candidate MCP-server frontend

One future exploration is exposing Wirecmd's normalized daemon capabilities as
a latest-revision MCP server. Before accepting that slice, deliberate whether
and how configuration filters exposure by upstream server, tool, resource,
prompt, and native capability; its defaults, inheritance, and fail-closed
behavior are intentionally undecided. A frontend should reuse existing daemon
semantics and could eventually front native LSP operations or translate
supported older upstream MCPs, but it must not drive a generic proxy framework
or weaken the shell-first product boundary.

## External compatibility trigger

Legacy HTTP+SSE remains deferred until a stable official Go SDK release exposes
`ClientSessionOptions.ProtocolVersion`. At that point, follow the dependency
decision process, update the SDK deliberately, and requalify the historical
fixture before adding configuration or runtime support. Do not restore the
discarded local SSE path or maintain a second MCP protocol implementation.

## Deferred supporting work

Persistent MCP metadata was deliberately removed from the current help and
completion contract. Dynamic tool/argument completion may reconsider a cache
that survives daemon restarts and refreshes on reload only through a new
accepted contract. Bash/Zsh completion, service-manager integration, binary
archives, package-manager distribution, Windows support, advanced OAuth modes,
language-server catalogs, unsaved editor buffers, and file watching remain
deferred unless promoted through a separate plan.

## Working rule

Before implementing each numbered area, inspect current code and upstream
versions, write the smallest coherent plan, and identify public-contract,
configuration, dependency, lifecycle, or security decisions for user review.
Resolvable implementation details do not require escalation. If evidence would
change the accepted direction, explain how and why and ask before substituting
an alternative. A new user-facing configuration format or dependency change
requires explicit approval. Commits, pushes, merges, tags, releases, and other
external publication remain separately authorized actions.
