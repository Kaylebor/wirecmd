# Product Thesis

Status: authoritative product direction

## Thesis

Wirecmd makes useful capabilities available through a stable shell contract.
A shell-capable agent can discover, inspect, invoke, and compose those
capabilities without native support for every upstream protocol, eager schema
injection from its harness, or loss of ordinary shell tools.

Wirecmd is agent-oriented, but not agent-exclusive. A technical human commonly
configures or supervises it; a properly instructed agent may configure it too.
Humans, agents, scripts, editors, and CI use the same semantic commands,
results, errors, and exit behavior.

MCP and LSP are upstream capability families rather than the product boundary.
Wirecmd is a configuration-driven capability runtime and local lifecycle
broker. It hides protocol and lifecycle complexity without hiding the
configuration needed to operate arbitrary providers.

## Problem

Harness-native integrations commonly own provider registration, connection
lifecycle, schema exposure, invocation, authentication, and presentation. That
couples capability access to one harness and can inject large inventories before
an agent needs them.

Agents already know how to discover commands, read focused help, handle exit
status, consume structured output, and compose shell programs. A stable shell
interface lets the same configured capability work across different agents and
ordinary local automation while preserving those skills.

Upstream providers also vary in ways a generic runtime cannot predict. Their
executables, arguments, environments, settings, project conventions, and
lifecycle assumptions are owned by their developers and users. A useful
runtime must expose predictable primitives without pretending to understand
every server.

## Product model

Configuration is Wirecmd's control plane. Global sources define reusable
providers. Project sources add defaults or overrides when a project needs them.
Configuration provenance remains explainable regardless of where the resulting
provider runs.

The runtime keeps several concerns distinct:

- configuration sources describe providers and compose static intent;
- invocation context describes facts resolved for the current call;
- explicit materialization places contextual or sensitive values into fields a
  provider consumes;
- instance scope selects the ownership and reuse boundary for a live provider;
- materialized startup configuration describes what is actually launched; and
- authentication identity describes the selected credentials or upstream
  principal without exposing secret material; and
- retained-instance identity combines scope, startup-affecting configuration,
  and sensitive identity for honest reuse decisions.

Where configuration is stored does not determine instance scope. Scope does not
remove contextual information or prescribe how an upstream provider interprets
it. When materialized values make two providers start differently, Wirecmd must
represent that difference honestly rather than claiming interchangeable state.

This separation lets a provider be defined once and used in many contexts. A
developer—or an agent operating from adequate instructions—decides which
available values a particular provider needs and where to place them.

## Responsibilities and ownership

Wirecmd owns behavior that can be made generic:

- lazy discovery and configuration composition;
- provenance and actionable configuration errors;
- context resolution and explicit value materialization;
- secret resolution, redaction, and sensitive identity boundaries;
- process, connection, and retained-session lifecycle;
- protocol negotiation and framing through maintained SDKs where available;
- focused capability inspection and lossless invocation;
- normalized structured output, stable errors, and recovery guidance; and
- consistent direct and daemon-backed semantics where continuity permits.

The configurator owns arbitrary provider semantics. Wirecmd should validate its
own contract but should not infer a language server, database connector, MCP
server, or future provider's private policy merely to simplify an adapter.

Known-provider conveniences may become worthwhile after repeated evidence.
They remain conveniences that compile down to the generic configuration model;
they must not create a separate runtime or make the generic path incomplete.

## Interaction principles

### Pull, do not inject

An agent begins with a small universal Skill and a stable discovery convention.
It asks for available providers, capabilities, or schemas only when needed.
Large inventories are not assumed to be present in the harness context.

### Compose through the shell

Structured input and output are authoritative. Ergonomic projections may
improve common calls but cannot make a valid upstream invocation
unrepresentable. The important unit of composition is an entire shell program,
which may combine repository search, files, Git, language tooling, filters, and
multiple remote capabilities without a model round trip between every step.

Wirecmd must not grow a competing workflow language.

### One public contract

Agents and humans do not receive different semantic interfaces. Presentation
may adapt to a terminal, but scripts and CI retain the same operation, result,
error, and exit-code behavior. Machine-readable output remains deterministic
and selectable explicitly.

### Deterministic interaction

Ordinary non-interactive calls do not unexpectedly prompt. Narrow documented
flows—such as interactive OAuth or authorization performed by a selected
hardware-backed identity—may involve the user without changing the stdout
result contract. Diagnostics and interaction guidance remain on stderr.

### Honest lifecycle brokering

Normal operation is daemon-backed so processes, connections, authentication
flows, and protocol state can be retained. The daemon is an internal lifecycle
facility, not the agent-facing protocol or a general service manager.

Equivalent invocations preserve their public contract in direct and
daemon-backed modes except where genuine continuity changes semantics. Wirecmd
must never silently fall back to one-shot execution or pretend that state
survived replacement, restart, or identity change.

## Adapter boundaries

Public commands and normalized results remain independent of upstream wire
revisions and generated SDK types. Maintained official SDKs own protocol
negotiation, transport, authorization, and compatibility behavior by default.
Local shims require a confirmed gap and remain narrow.

Adapters translate generic Wirecmd operations into upstream protocol behavior.
They do not define a second agent-facing architecture. MCP, LSP, and any future
family share the configuration, context, secret, scope, lifecycle, output, and
recovery model even when their protocol mechanics differ.

## Product boundaries

Wirecmd is not:

- an IDE or replacement editor client;
- an agent harness or harness-specific integration layer;
- a marketplace, provider registry, or mandatory server catalog;
- a workflow engine competing with the shell;
- a policy engine that decides what arbitrary providers are allowed to mean;
- a general plugin or extension runtime;
- a universal service manager, autoscaler, or audit database;
- a security sandbox for hostile same-user processes; or
- a promise to expose every primitive of every supported protocol.

Future capability families, provider conveniences, and broader protocol
surfaces require demonstrated value and an accepted contract. Current shipped
state and open decisions are recorded in the [roadmap](roadmap.md); detailed
feature contracts and qualification records are indexed in the
[documentation map](README.md).

## Decision test

For unfamiliar behavior, first determine whether it is required by an upstream
protocol, is a generic runtime or lifecycle invariant, or belongs to arbitrary
provider configuration. Wirecmd should encode the first two centrally and give
the configurator an expressive, explicit path for the third.

A feature belongs in the product when it materially improves reliable
discovery, configuration, invocation, composition, recovery, or transparent
lifecycle operation across agents and contexts. Protocol breadth, competitor
parity, or local adapter convenience alone is insufficient.
