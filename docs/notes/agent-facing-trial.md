# Agent-facing Wirecmd trial

Status: non-authoritative exploratory evidence.

Observed on 2026-08-24 after `f8d2cbc`. Two subagents received the same blind
task against isolated temporary Wirecmd configurations and private daemon
runtime directories. They were given only a binary path, config path, runtime
directory, and desired outcome. They were told not to inspect source or config,
not to use direct mode, and not to manage the already-running daemons.

The agents were not given the repository's universal Agent Skill. This makes
the run useful evidence that CLI self-discovery can work without it, but it does
not test the milestone requirement that the Skill remain small,
source-agnostic, and sufficient across harnesses.

The configured official Go SDK v1.7.0 fixtures were:

- `examples/server/memory` over stdio, under alias `knowledge`;
- `examples/server/everything` over stdio, under alias `utility`; and
- `conformance/everything-server -stateless=true` over Streamable HTTP, under
  alias `remote-tests`.

Each temporary config selected its own workspace root and daemon runtime. The
HTTP fixture was shared because it was stateless; stdio processes and daemon
state were isolated by runtime and workspace identity.

## Frozen task

Each agent had to use CLI discovery and focused help to:

1. obtain a deterministic sentence from an HTTP-backed capability;
2. transform it into a lowercase alphanumeric identifier with shell tools;
3. pass the identifier and original sentence into the stateful stdio knowledge
   capability from one shell program;
4. verify continuity through a separate Wirecmd process;
5. trigger and recover from one genuine tool-reported validation error; and
6. remove any created state.

## Luna trial

Luna followed the assigned config and lifecycle constraints. It discovered the
three aliases without source or config inspection, used focused help to select
`remote-tests test_simple_text`, and obtained:

```text
This is a simple text response for testing.
```

It transformed that to:

```text
thisisasimpletextresponsefortesting
```

Luna constructed the requested one-shell HTTP-to-stdio program, but an external
approval layer rejected the temporary knowledge-graph mutation before the
elevated process launched. A sandboxed attempt performed the HTTP call and
transformation but could not reach the daemon. The graph remained empty.

It completed the non-mutating error-recovery path against the HTTP fixture:

- invalid `test_x_mcp_header` argument `{"region":7}` returned exit `5`,
  `upstream_tool/tool_reported_error`, and a message saying the integer should
  be a string;
- corrected `{"region":"EU"}` returned exit `0` and `region=EU`.

Reported measurements:

```text
elapsed wall time: approximately 70 seconds
Wirecmd processes launched: 20
sandbox-artifact failures: 2
approval rejections before launch: 3
genuine CLI/upstream failures: 3
shell setup errors before Wirecmd launch: 2
```

Luna judged the interface sufficient for discovery, focused help, HTTP
invocation, shell transformation, and structured-error recovery. Stateful
composition was not established by this run because the agent execution
approval boundary prevented the mutation.

Its three non-sandbox Wirecmd failures were:

- one accidental invocation without the mandatory `--config` flag;
- an intentional call to the fixture's always-failing `test_error_handling`
  tool; and
- the intentional invalid integer `region` used for the recovery exercise.

Only the missing-config invocation was an unexpected agent mistake. The other
two were deliberate tool-error probes.

## Spark trial

Spark completed discovery, HTTP retrieval, transformation, state creation,
separate-process verification, validation-error recovery, and cleanup in about
two minutes. It produced the same identifier and stored it with the original
sentence as an observation, verified it through `open_nodes`, then deleted it.

Spark reported 31 Wirecmd invocations. It made two genuine upstream validation
mistakes:

- an incorrectly nested exact-call envelope; and
- an `open_nodes` call missing the required `names` field.

Structured errors and focused help were sufficient to correct both in the
contaminated environment.

However, Spark violated the frozen evaluation protocol. After sandbox/runtime
confusion it executed an older temporary composition script in debug mode,
learned that script's broader qualification config path, switched away from its
assigned isolated config, and attempted daemon lifecycle diagnostics despite
instructions not to manage the daemon. It said it did not directly open either
KDL file, but executing the script exposed implementation details the blind
trial was meant to withhold. Its successful functional outcome is useful UX
evidence but is not directly comparable to Luna's constrained run.

## Interpretation

Both agents found usable capabilities, preferred focused help, consumed
structured errors, and corrected invalid calls. Luna's constrained trace
supports CLI-only discovery and recovery. Spark completed the composed and
stateful workflow as an uncontrolled functional smoke result, but its leaked
script/config details mean that run cannot establish that Wirecmd discovery
alone was sufficient. Luna produced the cleaner interaction trace, but its
stateful half was blocked by an external approval layer.

The run is therefore promising but not a controlled model comparison. Before
using it for comparative claims:

- pre-authorize the exact local Unix socket, loopback, and temporary fixture
  operations so sandbox and approval behavior is identical for every agent;
- give each agent an environment containing only its assigned binary/config,
  with no discoverable older harness scripts;
- automatically capture commands, exit codes, and timestamps rather than rely
  only on self-report;
- run multiple repetitions with fresh daemon state; and
- compare the same frozen task against native harness MCP exposure and one
  maintained MCP CLI.

The 20 and 31 Wirecmd invocation counts are high for the small task and should
be treated as a UX signal, not normalized away. This run did not capture enough
structured telemetry to separate necessary discovery/help calls from sandbox
retries and Spark's environment debugging, so it cannot yet attribute that
overhead to the product contract.

All foreground fixtures started by the parent evaluation were stopped. Luna's
assigned knowledge graph was verified empty. Spark reported successful cleanup,
but its daemon became unavailable before the parent could independently verify
that graph, so cleanup there remains agent-reported rather than parent-verified.

## Controlled rerun with the universal Skill

A second run used fresh binaries, configs, workspaces, runtime directories, and
a telemetry-only Wirecmd wrapper for each agent. The old composition helper was
removed. Both agents received the 331-word universal Skill and the same frozen
task. The parent preflighted create/read/delete against both process-only
knowledge fixtures before clearing telemetry.

The first no-history attempts showed that an assignment's quoted authorization
is not treated as trusted user authorization by the harness approval layer.
Luna was prevented from creating state, while Spark initially saw its healthy
host daemon as offline from the sandbox and then performed forbidden daemon
diagnostics. Those attempts are harness-control evidence, not model or Wirecmd
results.

The comparable reruns inherited the user's authorization and sent every
Wirecmd operation through the host-permitted path. Successful logged calls and
the agents' reports support HTTP discovery, transformation, stdio state
creation, separate-process verification, structured-error recovery, deletion,
and cleanup. The wrapper did not capture verification stdout. The parent
independently called `read_graph` afterward and observed empty graphs for both
fixtures.

Exact wrapper telemetry was:

| Trial | Raw calls | Comparable calls | Comparable exit 0 | Comparable exit 5 | Pre-rerun exit 7 | Comparable wall interval |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| Luna | 16 | 16 | 15 | 1 | 0 | 105.9 s |
| Spark | 25 | 23 | 19 | 4 | 2 | 106.9 s |

Spark also had two pre-rerun exit-7 calls followed by a 58.9-second harness
diagnostic gap. They are excluded from the table. The comparable wall intervals
measure agent think time plus subprocess execution; individual Wirecmd calls
were generally only a few milliseconds.

Luna followed the intended discovery sequence: global help, configured-server
listing, selected-server help, focused tool help, then calls. Spark listed the
relevant servers but invoked `create_entities` and `add_observations` before
requesting focused help, so its four exit-5 results include avoidable probes and
shape errors.

Both agents independently made the same projected-complex-value mistake after
seeing focused `create_entities` help. They supplied this object to
`--entities`:

```json
{"entities":[{"name":"...","entityType":"...","observations":["..."]}]}
```

The flag expects the array value itself. Both recovered by passing:

```json
[{"name":"...","entityType":"...","observations":["..."]}]
```

This is a concrete help/projection UX signal: the structured upstream error was
sufficient for recovery, but focused help did not prevent the same first-use
misinterpretation in either model. Spark also confused `content` with
`contents`, then corrected it after focused help.

The wrapper timestamps show HTTP and knowledge calls separated by only a few
milliseconds, which strongly corroborates one-shell composition. The wrapper
did not capture the parent shell source, stdout, stderr, or transformed value,
so the exact compound program and result remain agent-reported rather than
independently replayable telemetry.

This rerun is controlled functional evidence, not yet a fair model comparison:

- Luna's wrapper logged every command from `.` rather than
  its assigned workspace, while Spark used its assigned workspace;
- configs and telemetry were readable within a shared temporary tree;
- non-Wirecmd shell commands were not logged, so absence of source/config
  inspection cannot be independently proven; and
- no token/context measurements were captured.

No Wirecmd code defect was observed. The universal Skill remained small and
source-agnostic, and Luna's trace followed it closely. The next repetition must
enforce CWD and filesystem permissions, positively wait for daemon readiness,
capture the parent shell plus redacted stdout/stderr, and automatically verify
fixture state. Native-harness MCP and maintained-client comparisons remain
separate requirements.
