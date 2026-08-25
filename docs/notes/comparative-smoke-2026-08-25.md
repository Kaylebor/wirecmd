# Comparative interaction smoke, 2026-08-25

Status: non-authoritative smoke evidence. This run does not satisfy the
filesystem-isolation, common-telemetry, fresh-history, or repeated cold/warm
requirements in the authoritative validation plan. Approval authority also
differed: Wirecmd inherited recent authorization context, mcpc used scoped
elevated retries, and native tools were configured as preapproved. The run
therefore cannot pass or fail the milestone.

## Frozen inputs

All three conditions used GPT-5.6 Luna and the same outcome-based task: obtain
a deterministic sentence from an HTTP capability, transform it with the shell,
create and later retrieve process-local graph state, recover from an intentional
header-type error, delete the graph state, and verify cleanup. Shell clients
were additionally asked to compose the HTTP call, transformation, and graph
creation in one parent shell program.

The tested revisions were:

- Wirecmd commit `e8316ec25f5d733b637b28185056b869c7a8984f`;
- `github.com/modelcontextprotocol/go-sdk` v1.7.0 official memory,
  everything, and stateless conformance fixtures;
- `mcpc` v0.6.0; and
- `codex-cli` v0.149.1, using the documented temporary MCP configuration
  overrides corresponding to Codex's
  [MCP configuration](https://developers.openai.com/codex/mcp).

Fixture SHA-256 values were:

```text
memory       11091a2bba4f19b0169fb557368109b54d4e6da27c29a2e15141ffe75d1a422a
everything   eb1e555f72cfc9e903f742e18ddc977e697eaf9adabe2f018cb529b3c98d1ca3
conformance  980917f51ea03c2885237f73a5068031c1e44c85fbbcde33070dc2ce545c48b6
wirecmd      a9e05ab6a661b5b979b4e294ef816d0684c5d9a127a8f02f96b89a64fd0b89d1
```

Each arm used a fresh, distinct process-local memory-server process and graph
identity; only the stateless HTTP conformance endpoint was shared. The arms ran
sequentially with the preceding memory process or session closed before the next
one started.

The sentence and derived identifier were identical in every successful arm:

```text
This is a simple text response for testing.
thisisasimpletextresponsefortesting
```

## Wirecmd arm

A Luna subagent received the committed 331-word universal Skill, binary path,
opaque KDL path, workspace, and runtime directory. It used normal daemon-backed
operation only. Its first history-free run could perform discovery and error
recovery, but the approval classifier rejected the disposable graph mutation
because authorization supplied in the subagent assignment was not trusted user
context. The successful rerun inherited only recent turns containing the user's
authorization; this weakens blindness and is an evaluation-infrastructure
limitation, not a Wirecmd behavior change.

The successful run reported:

- 18 shell command executions and 16 Wirecmd processes;
- about 38 seconds of command wall time;
- server and tool discovery followed by focused help for the five used tools;
- HTTP call, shell transformation, and graph creation in one parent shell;
- exact state retrieval from a later Wirecmd process;
- one intentional exit-5 `upstream_tool/tool_reported_error`, correctly
  interpreted and corrected to `region=EU`;
- first-try success for projected `--entities`, `--names`, and
  `--entity-names`; and
- successful deletion and later absence verification.

The parent independently called `open_nodes` after the run and observed
`entities:null` and `relations:null`.

## mcpc arm

A separate history-free Luna subagent received `mcpc` v0.6.0, its opaque
configuration path, isolated client state, and the same task. It used mcpc's
normal help, explicit named sessions, and shell interface.

The run reported:

- 22 shell command executions and 27 mcpc processes;
- about 80–85 seconds elapsed;
- six schema queries and two explicit session-close processes;
- successful one-parent-shell HTTP transformation and graph creation;
- later state retrieval and cleanup verification;
- three initial sandbox connection failures, followed by successful narrowly
  elevated retries;
- one malformed help invocation and one malformed first composition attempt;
  and
- the intentional invalid-header result followed by successful `region=EU`.

The parent independently observed empty `sessions` and `profiles` after the
run. That corroborates session cleanup only; graph creation, retrieval, and
deletion remain agent-reported because the closed process-local memory fixture
could no longer be queried independently.

## Native Codex MCP arm

An ephemeral `codex exec` registered the same three fixtures directly. Codex's
`default_tools_approval_mode="auto"` still required unavailable interactive
approval, so that preflight was stopped before state mutation. The measured
rerun used `default_tools_approval_mode="approve"` for only the three temporary
fixtures.

The native run completed with seven MCP calls and one shell command:

- one HTTP sentence call;
- one shell transformation;
- create, later retrieve, delete, and final absence calls;
- one intentional invalid-header call; and
- one corrected header call.

It required no explicit discovery or help calls because the native tool catalog
and schemas were already supplied to the model. The captured Codex turn reported
651,739 input tokens, of which 606,976 were cached, plus 1,995 output tokens and
799 reasoning-output tokens. Those counts include the whole Codex turn and are
not directly comparable with the subagent arms, for which equivalent token
telemetry was unavailable.

In the tested `codex exec` v0.149.1 registration setup, native MCP calls could
not participate inside the parent shell program. The agent performed the shell
transformation between separate native calls and reported this tested-interface
property rather than task failure.

## Directional observations

- All three runs reported completion of the semantic capability task with the
  official fixtures. Native calls were captured directly; Wirecmd cleanup was
  independently checked; mcpc graph outcomes remain agent-reported.
- Both shell-client agents reported expressing the multi-capability work as one
  shell program. The captured native transcript shows separate native calls and
  one shell transformation; this `codex exec` native-registration setup did not
  make MCP calls available inside an ordinary shell call.
- The successful Wirecmd run reported first-try use of all projected complex
  flags, unlike the repeated `--entities` wrapping mistake in the earlier
  controlled trials. Repetition is required before attributing that change to
  the revised help.
- Native required no explicit discovery calls because its tool catalog was
  supplied initially. Its whole Codex turn reported 651,739 input tokens, but
  the portion attributable to tool schemas is unknown and equivalent shell-arm
  token telemetry was unavailable.
- The mcpc run issued explicit session setup and teardown commands, while the
  Wirecmd run did not expose connection lifecycle commands. Different failures,
  help paths, and telemetry make their process counts non-causal observations.

These are directional observations only. A defensible comparative result still
requires at least two fresh cold/warm repetitions per arm, one common telemetry
surface, parent-visible transcripts for every arm, and actual mount/container
isolation that prevents source and cross-condition reads. Do not use the raw
timings or process counts above as a product ranking.
