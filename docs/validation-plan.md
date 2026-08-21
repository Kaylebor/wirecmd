# First Validation Milestone

Status: authoritative current milestone

## Objective

Determine whether an agent given only shell access and a small universal Skill
can reliably discover and compose capabilities supplied by MCP servers without
native MCP integration in its harness.

This milestone validates the interaction model and the minimum lifecycle model
needed for stateful MCP servers. It does not validate daemon sophistication,
complete MCP compatibility, or feature parity with existing clients.

## Hypothesis

A stable shell discovery and invocation contract lets an agent select and use
MCP-backed capabilities with less eager context and comparable task reliability
to direct harness MCP exposure.

## Minimal spike

Implement only enough product surface to exercise:

- one controlled stdio MCP server fixture;
- one controlled HTTP MCP server fixture;
- listing configured sources;
- listing or inspecting a source's capabilities;
- focused help for a single tool;
- lossless JSON input from an argument or stdin;
- stable structured success output;
- stable structured error output and documented exit codes; and
- a minimal universal Agent Skill describing discovery and recovery;
- a minimal local daemon path that retains an initialized stdio server across
  separate CLI invocations; and
- equivalent public command, output, error, and exit behavior for one-shot and
  daemon-backed calls where both modes are valid.

Use the official Go MCP SDK for the supported protocol paths. Select exact
dependency versions from current upstream evidence when implementation begins.

## Explicitly deferred

- Production-grade daemonization and service-manager integration.
- Advanced daemon supervision, pooling, recovery, hot reload, and public API
  stability.
- OAuth browser flows.
- Hierarchical KDL configuration beyond the minimum needed for the fixtures.
- Schema-derived ergonomic flags.
- Legacy HTTP+SSE unless required by a selected comparison fixture.
- Resources, prompts, subscriptions, Tasks, sampling, and rich elicitation.
- Pool widths greater than one.
- Runtime templates and secret providers beyond minimal safe fixture needs.
- MCP proxy/server mode and non-MCP adapters.

Deferred items may influence whether the spike leaves a viable path forward,
but they are not implementation requirements for this milestone.

## Evaluation scenarios

Give the evaluated agent shell access and the universal Skill, but no native
registration of the test MCP servers. Require it to:

1. discover the relevant capability from more than one configured source;
2. inspect only the help needed to construct a valid call;
3. call a tool with structured input;
4. pipe or otherwise transform its structured result;
5. use that result in a second capability call; and
6. recover from at least one actionable failure using structured error data;
7. repeat a stateful operation across separate CLI invocations through the
   daemon; and
8. perform an operation valid in both modes without changing its public command
   or interpreting different result semantics.

Run comparable scenarios with:

- direct harness MCP exposure;
- this spike; and
- at least one maintained existing MCP CLI.

The comparison is intended to test the thesis, not to manufacture a favorable
feature checklist.

## Evidence to collect

- Task completion and correctness.
- Commands and help surfaces the agent chose to inspect.
- Schemas or tool descriptions introduced into context.
- Approximate token/context cost attributable to capability discovery.
- Number of failed or malformed invocations before success.
- Whether multi-step shell composition succeeded without manual intervention.
- Whether the agent correctly classified and recovered from the test failure.
- Cold and warm latency, with environment and caching state identified.
- Process identity or fixture state proving that daemon-backed calls reused the
  same initialized server instance across separate CLI processes.
- Contract differences, if any, between one-shot and daemon-backed execution.
- Qualitative confusion caused by naming, output, help, or configuration.

## Acceptance criteria

The milestone passes when, in repeatable scenarios:

- the agent completes discovery and the composed task without native harness
  MCP integration;
- every valid fixture tool input remains representable through the lossless
  invocation path;
- stdout, stderr, JSON, and exit behavior are deterministic and documented;
- the agent can act on a structured failure without parsing an interactive
  prompt or implementation-specific log text;
- the universal Skill remains source-agnostic and small enough to reuse across
  harnesses; and
- daemon-backed fixture calls preserve initialized server continuity across
  separate CLI invocations without changing the agent-facing contract; and
- observed context or composability benefits are material enough to justify
  further product work.

The milestone fails or forces reconsideration when agents routinely cannot find
the right capability, shell composition is less reliable than direct tool use,
the universal instructions become server-specific, or the measured benefit is
too small to justify a new runtime.

## After the milestone

Only after reviewing the evidence should the project commit to production
daemon behavior, configuration format and merging, broader revision and
transport support, OAuth, schema-derived flags, or packaging. Promote accepted
decisions into the authoritative documents; retain rejected and unresolved
alternatives in `docs/notes/`.
