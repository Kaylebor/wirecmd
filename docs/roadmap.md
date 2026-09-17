# Current Roadmap

Status: authoritative current-state handoff after v0.4.0; later items remain a
decision queue

## Purpose

This document gives a fresh contributor the current sequence, rationale, and
decision boundaries without requiring conversational history. Detailed feature
contracts remain in their linked authoritative plans. Items below preserve the
order in which open areas should be discussed; they do not authorize an
unspecified design or commit the project to every candidate.

The governing documents are the [product thesis](product-thesis.md),
[LSP plan](plans/lsp-plan.md), [MCP compatibility plan](plans/compatibility-plan.md),
[MCP resources plan](plans/resources-plan.md),
[age secrets plan](plans/age-secrets-plan.md),
[scope and invocation context plan](plans/scope-context-plan.md),
[release-readiness contract](release-readiness.md), and
[macOS qualification plan](plans/macos-plan.md). The
[documentation index](README.md) routes contributors to authoritative milestone
documents and non-authoritative working material.

## Current baseline

Wirecmd v0.4.0 is the current stable release. Wirecmd is a shell-native
capability client and local daemon. MCP is its first upstream adapter rather
than the harness-facing abstraction. The baseline surface includes:

- explicit `wirecmd mcp` discovery, focused help, projected and exact-JSON tool
  calls, and deterministic structured results;
- stdio, Streamable HTTP, and legacy HTTP+SSE MCP through the official Go SDK,
  including transparent Streamable HTTP OAuth and encrypted credential
  persistence;
- composed KDL configuration with optional workspace-defaulted scope, trusted
  workspace discovery, retained sessions, terminal-aware output, and Fish
  completion;
- MCP resource and resource-template discovery plus resource reads; and
- selector-routed native LSP navigation, hover, signature help, document
  symbols, and workspace symbols across multiple providers.

Modern and legacy initialized stdio and Streamable HTTP are qualified. Legacy
HTTP+SSE is qualified through stable SDK v1.8.0; transparent SSE OAuth remains
deferred.

Normal operations remain daemon-backed and must fail clearly when the daemon is
offline. `--direct` is the explicit one-shot path. SDK protocol types and MCP
revision details must not leak into the public semantic command/result contract.

## Implemented v0.4.0 milestone

Age-backed secret resolution adds the fixed `age` CLI as an optional internal
provider beside `env`. It reads encrypted stores only for selected operations,
supports user-managed TPM and Secure Enclave identities, and keeps only
metadata needed to reuse retained daemon instances. Workspace discovery uses
`.wirecmd/config.kdl` and `.wirecmd/secrets.json.age`; the former top-level
workspace filename is not part of discovery. The exact contract and
qualification requirements are in the [age secrets plan](plans/age-secrets-plan.md).
Physical Apple Silicon qualification of Secure Enclave-backed age resolution
remains pending; v0.4.0 was explicitly published first to provide the tagged
build for that test, so no physical-device qualification is claimed yet.

## Unreleased on main

The accepted scope and invocation-context milestone is implemented on `main`
but is not part of the v0.4.0 release. MCP and LSP definitions may use
`scope "global"` as well as the default `workspace` scope. Global lifecycle
ownership, provider roots, retained identity, LSP status, and age resolution
are scope-separated. Its complete contract and qualification matrix are in the
[scope and invocation context plan](plans/scope-context-plan.md). It does not
introduce public context variables or templating.

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

## Completed external compatibility trigger

Stable official Go SDK v1.8.0 exposes
`ClientSessionOptions.ProtocolVersion`; the historical HTTP+SSE fixture and
public `sse` configuration path are qualified without a local protocol stack.
Future protocol changes still follow the dependency decision process and must
preserve this SDK-owned boundary.

## Deferred supporting work

Persistent MCP metadata was deliberately removed from the current help and
completion contract. Dynamic tool/argument completion may reconsider a cache
that survives daemon restarts and refreshes on reload only through a new
accepted contract. Bash/Zsh completion, service-manager integration, binary
archives, package-manager distribution, Windows support, advanced OAuth modes,
language-server catalogs, unsaved editor buffers, and file watching remain
deferred unless promoted through a separate plan. Arbitrary secret-provider
commands, provider plugins, secret-file templating, and inherited-environment
hardening are also deferred.

## Working rule

Before implementing each numbered area, inspect current code and upstream
versions, write the smallest coherent plan, and identify public-contract,
configuration, dependency, lifecycle, or security decisions for user review.
Resolvable implementation details do not require escalation. If evidence would
change the accepted direction, explain how and why and ask before substituting
an alternative. A new user-facing configuration format or dependency change
requires explicit approval. Commits, pushes, merges, tags, releases, and other
external publication remain separately authorized actions.
