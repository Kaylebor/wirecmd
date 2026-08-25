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
- one controlled stateless modern Streamable HTTP MCP server fixture;
- listing configured sources;
- listing or inspecting a source's capabilities;
- focused help for a single tool;
- conservative top-level schema-derived arguments, with raw JSON overlay and
  exact-JSON fallback for every input that cannot be projected safely;
- lossless JSON input from an argument or stdin;
- stable structured success output;
- stable structured error output and documented exit codes; and
- a minimal universal Agent Skill describing discovery and recovery;
- a minimal local daemon path that retains an initialized stdio server across
  separate CLI invocations; and
- equivalent public command, output, error, and exit behavior for one-shot and
  daemon-backed calls where both modes are valid.

Normal test invocations use the daemon and must fail clearly when it is absent.
Tests request direct execution explicitly with `--direct`; an automatic direct
fallback is a contract violation.

Use `github.com/modelcontextprotocol/go-sdk` v1.7.0 for the supported protocol
paths and `github.com/njreid/gokdl2` v0.6.0 for explicit KDL 2 parsing. These
versions were selected from current upstream evidence before implementation.
Assume the SDK owns MCP negotiation, lifecycle, transport, capability, and
request semantics; verify its support before adding any MCP-specific code.

The first slice targets modern `2026-07-28` behavior. Later slices walk backward
through legacy initialized stdio/Streamable HTTP and then legacy HTTP+SSE. This
milestone need not complete that compatibility sequence, but its public shell
and daemon boundaries must not obstruct the full-MVP target.

## First implementation slice

Build one narrow vertical path before completing the rest of the milestone:

```text
explicit KDL configuration
-> daemon-backed CLI request
-> retained official stdio MCP server
-> tool listing and lossless invocation
-> deterministic structured output
```

Use the official Go SDK memory example without durable file storage to prove
continuity. Create fixture state through one CLI process, observe it through a
separate daemon-backed CLI process, and confirm that a fresh `--direct` process
does not inherit that process-local state.

The first slice exposes only the minimum development surface:

- foreground `daemon run`, status, and explicit config reload;
- normal daemon-required calls plus explicit `--direct` calls;
- listing configured servers and a selected server's tools;
- calling a selected tool through exact JSON argument or stdin input; and
- stable stdout, stderr, structured errors, and exit behavior.

Require one or more explicit `--config` files. Merge repeated files in command
line order with the last file most specific; automatic global/project discovery
comes later. The initial KDL schema covers a root, named server, workspace scope,
either a stdio executable with ordered arguments/environment entries or a modern
Streamable HTTP endpoint, literal values, and `(secret)"env://..."` references.
HTTP endpoints are absolute `http` or `https` URLs; typed query/header entries
and HTTP credential injection remain deferred. Parse into a source-syntax-
independent model that retains source file and semantic-path provenance.

The first daemon is a private per-user local broker with a compatibility
handshake, per-context configuration cache, and one retained MCP session per
effective workspace/server/configuration/authentication identity. Pool width is
fixed at one. It does not yet need detached startup, service-manager packaging,
idle cleanup, recovery policies, config watching, generated files, templates,
OAuth, typed HTTP header/query children, legacy HTTP qualification, or a stable
public daemon API.

For sensitive values injected when an instance starts, derive an internal
authentication/input identity with a daemon-local keyed digest over canonical
destinations and resolved values. Never expose that digest as a public config
fingerprint or persist its key. Different resolved instance-start credentials
must not reuse one retained process; invocation-time credentials do not split
the instance when they can safely be applied per request.

Validate repeatable config overlay behavior, workspace isolation, missing versus
empty environment values, secret redaction, clean separation of upstream stderr
from result stdout, JSON/stdin equivalence, daemon-offline failure without
fallback, daemon/direct contract equivalence where applicable, and retained
state across separate CLI processes.

This slice deliberately tests direct typed secret values through a child
environment entry. Typed HTTP header/query injection, generated secret
artifacts, and template materialization remain deferred; their internal
representation must remain possible without implementing them here.

Focused schema help, projected arguments, the universal Agent Skill, and the
controlled modern HTTP fixture are now implemented. The remaining work is the
full agent-facing milestone evaluation rather than another transport baseline.

## Explicitly deferred

- Production-grade daemonization and service-manager integration.
- Advanced daemon supervision, pooling, recovery, hot reload, and public API
  stability.
- OAuth browser flows.
- Hierarchical KDL configuration beyond the minimum needed for the fixtures.
- Legacy initialized protocol qualification and HTTP+SSE implementation; these
  are sequenced after the modern slice, not excluded from the full MVP.
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
   or interpreting different result semantics; and
9. complete one composed shell program that combines local search or
   transformation with more than one MCP-backed operation in a single harness
   shell call.

Run comparable scenarios with:

- direct harness MCP exposure;
- this spike; and
- at least one maintained existing MCP CLI.

The comparison is intended to test the thesis, not to manufacture a favorable
feature checklist.

### Comparative run protocol

Freeze one outcome-based task and use the same evaluator model for every
condition. The task must require HTTP-backed discovery, an ordinary shell
transformation, stateful stdio creation and later retrieval, recovery from one
intentional invalid call, cleanup, and—where the interface permits it—more than
one MCP-backed operation composed in one parent shell program.

Compare the interfaces as they actually operate:

- Wirecmd receives ordinary shell access, the committed universal Skill, an
  explicit KDL config path, and a ready foreground daemon.
- The maintained CLI receives ordinary shell access and its normal documented
  configuration and help surfaces, without Wirecmd-specific instructions.
- Native harness MCP receives the same fixtures as directly registered tools,
  without either shell client. Native inability to place MCP calls inside one
  shell program is an observed interface property, not task failure.

Each condition and repetition must use a fresh stateful fixture, workspace, and
client or daemon state. Use identical fixture builds, a shared task wording,
preflight fixtures separate from measured fixtures, and no condition-specific
tool or command hints. Positively establish readiness before releasing the
agent and independently verify state after creation, retrieval, and cleanup.
Use fresh conversation history and equivalent pre-authorized authority for each
condition. If approval policy, inherited user context, or sandbox escalation
differs, record the affected run as a non-comparable harness artifact.

Capture the parent-visible shell and native-tool transcript, timestamps, CWD,
redacted inputs and outputs, exit or error outcomes, fixture request/state
evidence, help or schemas actually inspected, and the exact Skill or initial
tool descriptions supplied to the agent. Record cold and warm preparation
explicitly; do not pretend eager native registration has the same cold-start
boundary as a shell client.

Before treating the outcome as repeatable evidence, run at least two fresh cold
and two fresh warm repetitions per condition. Cold starts from new client or
daemon state with no agent-visible discovery or call; warm may establish only
the relevant connection and tool listing, never task graph state. Counterbalance
condition order and record eager native registration as that interface's actual
preparation rather than inventing an equivalent shell-client boundary. Here,
"no discovery" means no agent-initiated discovery: a native catalog injected
before release is part of that condition's recorded cold preparation.

A run is comparative milestone evidence only when filesystem isolation prevents
the evaluated agent from reading repository source, another condition's
artifacts, or hidden fixture configuration. CWD placement, instructions, and
separate directory names alone do not establish that isolation. Otherwise label
the run as a smoke test and retain its limitations.

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
- Model round trips, subprocess calls, and intermediate serialization required
  by the composed scenario.
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
daemon behavior, automatic configuration discovery and templating, OAuth,
broader schema projection, or packaging. If the interaction model remains
viable, compatibility work then continues backward through the two legacy
layers required for the full MVP. Promote accepted decisions into the
authoritative documents; retain rejected and unresolved alternatives in
`docs/notes/`.
