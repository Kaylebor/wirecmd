# Product Thesis

Status: authoritative product direction

## Thesis

Make capabilities supplied by MCP servers available to any shell-capable agent
without requiring native MCP support in its harness, eager tool-schema
injection, or loss of ordinary shell composition.

The shell is the agent-facing capability interface. MCP is the first upstream
adapter and should normally be invisible outside configuration and diagnostics.

## Problem

Harness-native MCP integrations commonly own server registration, connection
lifecycle, schema exposure, tool invocation, authentication, and presentation.
That couples capability access to each harness and can expose large tool
inventories before an agent needs them.

Most coding agents already have a shell and strong operational priors for
discovering commands, reading help, handling exit status, processing structured
output, and composing programs. A stable shell contract can make the same
capabilities usable by different agents, people, scripts, CI jobs, editors, and
other local automation without teaching each consumer MCP.

## Product boundary

The runtime provides:

- lazy discovery of configured capability sources;
- focused help and schema inspection;
- deterministic, non-interactive invocation;
- lossless structured input and machine-readable output;
- stable error categories with actionable recovery information;
- composition through pipes, redirection, scripts, and standard shell tools;
- configuration that can vary safely by invocation and workspace context; and
- optional local lifecycle brokering that does not change command semantics.
- transparent OAuth for protected HTTP sources when an interactive caller
  permits browser authorization, with encrypted local credential persistence.

The initial adapter consumes MCP servers. Future adapters are possible only if
real use demonstrates their value. The initial design must not implement or
publicly commit to them.

## Interaction principles

### Pull, do not inject

An agent begins with a small universal Skill and a stable discovery convention.
It asks for the available servers, capabilities, or schemas only when the task
requires them. Large inventories are not assumed to be present in the harness
context.

### Shell-native composition

Results must be usable with normal shell tooling. Structured input and output
are authoritative; ergonomic projections such as generated flags may improve
common calls but cannot make a valid upstream invocation unrepresentable.

The important unit of composition is an entire shell program submitted through
one harness shell call, not merely one MCP call presented as a command. An agent
should be able to combine repository search, logs, files, Git, language tools,
ordinary filters and transforms, and multiple remote capabilities without a
model round trip between every operation. The shell remains the workflow
language; this project must not grow a competing workflow engine.

### One public contract

Agents and humans should not use separate semantic interfaces. Scripts and CI
must receive the same operation, output, error, and exit-code behavior as an
agent invoking the command through a shell.

### Non-interactive by default

Ordinary commands remain deterministic and non-interactive when either stdin or
stderr is not a TTY, or when `WIRECMD_NONINTERACTIVE=1` is set. When both are
TTYs, a protected HTTP call may transparently open a browser and wait for the
OAuth callback. Authorization URLs and browser diagnostics go to stderr; the
machine-readable result remains the single stdout envelope. Explicit
`wirecmd auth login SERVER` provides the deliberate credential-management
flow, and headless callers receive an actionable structured condition instead
of an unexpected prompt.

### Daemon-backed normal operation

A local daemon may retain upstream connections, own long-lived authentication
transactions, coordinate browser callbacks, manage encrypted local
credentials, manage local processes, cache discovery, and reap idle resources.
It is an optimization and lifecycle broker, not the agent-facing protocol.
Equivalent invocations should retain their observable contract with or without
the daemon, except where an upstream operation genuinely requires continuity.

Normal invocation is daemon-backed and fails clearly when the daemon is
unavailable. An explicit `--direct` mode provides deliberate one-shot execution
for testing and diagnostics. The CLI must not silently fall back to direct mode
because doing so could discard expected server or application state while
appearing successful.

The daemon path should be validated early rather than postponed until after a
pure one-shot client is complete. Many currently deployed MCP servers use
initialized sessions or persistent stdio processes, so testing only ephemeral
connections would leave a material part of the product premise unexercised.
Early validation does not justify advanced pooling, recovery, hot reload,
service-manager integration, or a public daemon API.

## Differentiation

The project is not justified merely by being a compiled MCP CLI. Its intended
value is the combination of:

- harness-independent capability access through the shell;
- lazy, agent-directed discovery;
- predictable composition and recovery;
- workspace-aware behavior with explainable configuration provenance; and
- hidden upstream protocol and lifecycle complexity.

Existing MCP clients are comparison points. Their breadth is not a target by
itself.

## MCP implementation constraint

The official Go SDK owns MCP behavior by default. Before adding MCP-specific
code, assume the pinned SDK already supports the required feature and verify it
against current SDK documentation, source, examples, and tests. Prefer its
highest-level client, session, transport, authorization, capability, and
request APIs.

Project code should primarily implement the product outside MCP: shell UX,
configuration and provenance, normalized output and recovery, local daemon IPC,
managed process lifecycle, instance scope, caching policy, diagnostics,
encrypted credential persistence, and Skills. It must not reimplement protocol
negotiation, wire codecs, transports, revision gates, or authorization
mechanics that the SDK handles. The current SDK does not expose a separate
stable resource/issuer identity for the storage boundary, so this slice uses
the resolved endpoint plus configured registration inputs as its credential
identity. Any collision evidence must be recorded before changing that
boundary or adding a compatibility shim.

If a required feature exposes a confirmed SDK gap, add the smallest isolated
shim and document the exact upstream limitation. A conceptual difference
between protocol eras does not by itself justify a local adapter or abstraction.

## MVP compatibility direction

The first full MVP should support three deployed MCP compatibility layers:

1. modern stateless MCP beginning with `2026-07-28`;
2. legacy initialized MCP over stdio and Streamable HTTP; and
3. legacy HTTP+SSE.

Implementation and validation proceed newest to oldest. Establish the public
shell contract and daemon boundary against modern MCP first, then exercise the
SDK's legacy initialized behavior, and finally add the isolated legacy SSE
transport path. The public command and result contract must not branch by era;
differences remain within the SDK and diagnostics unless they change an actual
capability available to the caller.

The first validation milestone found the shell interaction model viable, and
the supported legacy stdio and Streamable HTTP layers are now qualified. Legacy
HTTP+SSE remains deferred at the official SDK boundary. Trusted global and
workspace configuration discovery is complete. Typed query and header values
for Streamable HTTP endpoints and transparent OAuth with encrypted credential
persistence are complete; their contracts are
recorded in the [HTTP values plan](http-values-plan.md) and
[OAuth plan](oauth-plan.md). OAuth must use the official SDK's authorization
surface, with Wirecmd adding only interaction, persistence, daemon
coordination, redaction, and error mapping.

For this milestone, endpoint values remain structural configuration rather than
preassembled URL or request strings. `query NAME=value` and `header NAME=value`
children accept literal values or `(secret)"env://NAME"` references. Query
names are case-sensitive and header names are case-insensitive. Stronger
configuration layers replace matching keyed entries while preserving their
position and append new entries. Only the selected server resolves its secret
references; resolved startup credentials participate in daemon instance
identity so retained instances cannot cross credential boundaries.

## Current non-goals

- Acting primarily as an MCP server, aggregator, or proxy.
- Importing configuration from every supported editor or agent harness.
- Providing an MCP marketplace or registry.
- Turning arbitrary CLIs into MCP servers.
- Generating language-specific SDKs or typed application clients.
- Providing a general plugin or extension runtime.
- Building record/replay, audit-database, autoscaling, or policy-engine
  subsystems.
- Supporting every MCP primitive before validating the agent-facing contract.
- Designing future non-MCP adapters without a demonstrated consumer.
- Client ID Metadata Documents, device authorization, client credentials,
  provider-side revocation, non-loopback callbacks, multiple accounts per
  identity, and provider-specific OAuth compatibility guards are deferred.
- Templated or dynamically composed HTTP values and dynamic per-request
  headers remain deferred.

An optional future MCP-server frontend is not part of the current product
contract. If later justified, it may expose the daemon's aggregated semantic
capabilities over the latest MCP revision and could translate older upstream
servers into that newer frontend. This possibility should influence only the
existing separation between CLI rendering, daemon semantics, and SDK sessions;
it does not justify a generic frontend framework or proxy implementation now.

## Decision test

A feature belongs in the product when it materially improves reliable agent
discovery, invocation, composition, recovery, or the transparent operation of
those behaviors across workspaces and harnesses. Protocol breadth or competitor
parity alone is not sufficient.
