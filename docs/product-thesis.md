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

### One public contract

Agents and humans should not use separate semantic interfaces. Scripts and CI
must receive the same operation, output, error, and exit-code behavior as an
agent invoking the command through a shell.

### Non-interactive by default

Ordinary commands must not unexpectedly wait for terminal input or open a
browser. When authentication or user action is required, the runtime returns a
structured, actionable condition. Explicit interactive commands may perform or
wait for those actions.

### Optional daemon

A local daemon may retain upstream connections, own long-lived authentication
transactions, manage local processes, cache discovery, and reap idle resources.
It is an optimization and lifecycle broker, not the agent-facing protocol.
Equivalent invocations should retain their observable contract with or without
the daemon, except where an upstream operation genuinely requires continuity.

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

## Decision test

A feature belongs in the product when it materially improves reliable agent
discovery, invocation, composition, recovery, or the transparent operation of
those behaviors across workspaces and harnesses. Protocol breadth or competitor
parity alone is not sufficient.
