# Comparative milestone evaluation, 2026-08-25

Status: non-authoritative measured evidence. Product conclusions require an
explicit decision before promotion into `docs/product-thesis.md` or the
authoritative validation plan.

## Result

Twelve fresh GPT-5.6 Luna repetitions exercised the same outcome task through
Wirecmd, mcpc v0.6.0, and native Codex MCP. Each condition received two cold
and two warm labels in a counterbalanced schedule. Native startup eagerly
registered a fresh tool catalog in every repetition; its warm label is a
matrix stratum, not a retained shell-client cache.

| Condition | Task success | One-shell MCP composition | Mean evaluator time | Mean input tokens | Mean shell commands |
| --- | ---: | ---: | ---: | ---: | ---: |
| Wirecmd | 4/4 | 4/4 | 58.1 s | 197,409 | 10.5 |
| mcpc v0.6.0 | 2/4 | 3/4 | 171.1 s | 614,724 | 20.3 |
| Native Codex MCP | 4/4 | 0/4 | 51.4 s | 345,400 | 1.0 |

Native used a mean 7.5 MCP calls in addition to its one shell transformation.
The token figures are whole-turn Codex telemetry and were mostly cached input;
they do not isolate capability-discovery cost. They are useful as comparative
observations, not causal estimates.

Wirecmd completed all four task repetitions through the normal retained daemon
path without `--direct`. Each transcript shows server/tool discovery, focused
help, HTTP sentence retrieval, shell normalization, graph creation in the same
parent shell, later exact retrieval, integer-to-string error recovery, deletion,
and an empty final lookup. Parent calls independently observed an empty retained
graph, and every daemon socket was removed.

Native MCP completed all four semantic tasks with direct captured MCP calls.
As expected, native MCP calls were unavailable inside an ordinary shell
program, so the shell transformation occurred between separate native calls.
One warm repetition selected another deterministic text capability from the
same `remote-tests` source instead of `test_simple_text`; its sentence,
normalized identifier, create/retrieve values, and cleanup were internally
consistent and satisfy the frozen outcome wording.

Two mcpc repetitions completed the full semantic sequence. Warm-2 created and
retrieved the entity through `@memory`, then observed that session empty before
cleanup and created a distinct `@memtask` session for its final lifecycle; it
therefore failed retained-state cleanup even though its final report claimed
success. The replacement cold-2 repetition also failed: its attempted pipeline
produced empty values, and it later used the known sentence in a separate
operation. That run did not complete the required one-shell composition. Across
the four runs, mcpc needed substantially
more commands, elapsed time, and model input, with recurring confusion around
named-session continuity and where JSON output was available to shell capture.
All four parent `read_graph` snapshots were empty after evaluator completion.

## Selected evidence

Sequences 1 through 10 are under:

```text
/tmp/wirecmd-comparative-matrix-20260825
```

Fresh replacements for sequences 11 and 12 are under:

```text
/tmp/wirecmd-comparative-matrix-tail-20260825
```

The original sequence-11 `mcpc-cold-2` under the first root is excluded. Its
model turn completed, but parent verification was interrupted when the running
executor source was edited and Bash later encountered a partial-file parse.
The arm was rerun from fresh workspace, fixture, client state, and conversation.

Each selected arm retains:

- `transcript.jsonl`, `stderr.log`, and `run.meta` from the pinned Codex runner;
- parent lifecycle timestamps and command status in `parent/lifecycle.tsv`;
- fixture, daemon, warm-preparation, parent-verification, and cleanup logs under
  `parent/logs/`; and
- the staged input and fixture hashes under `parent/staged-inputs.sha256`.

The evaluated binary SHA-256 was
`ef5b48101298e7fc6d83e67b7c9c65e9d05e59cff0d540e3386fc440806488a5`.
Official Go SDK v1.7.0 fixture hashes were:

```text
memory       11091a2bba4f19b0169fb557368109b54d4e6da27c29a2e15141ffe75d1a422a
everything   eb1e555f72cfc9e903f742e18ddc977e697eaf9adabe2f018cb529b3c98d1ca3
conformance  980917f51ea03c2885237f73a5068031c1e44c85fbbcde33070dc2ce545c48b6
```

No selected transcript used raw HTTP, executed a fixture directly, read a
parent-control path, or used another condition's capability interface.

## Harness corrections and limits

The first executor revision produced conservative false failures that must not
be confused with task outcomes:

- shell verification required later operations to occupy distinct Codex shell
  items, rejecting valid sequences grouped in one shell program;
- empty-graph verification searched compact JSON text and rejected mcpc's
  equivalent pretty-printed JSON; and
- native verification hard-coded `test_simple_text`, rejecting a valid alternate
  deterministic sentence from the same required source.

The checked-in evaluation scripts now use same-command-compatible ordering,
parse graph JSON structurally, and pair native sentence, shell identifier, and
graph values dynamically. The immutable transcripts and parent outputs were
audited after those corrections. Offline replay accepted all four Wirecmd
transcripts and all selected native transcripts; mcpc outcomes required manual
semantic audit because its client output and session changes were not reliably
captured by the generic shell predicate. The stale lifecycle status from an affected
arm remains historical evidence of the earlier harness decision; it was not
rewritten.

The official SDK's `Mcp-Param-*` behavior also exposed a lifecycle prerequisite:
`CallTool` derives annotated HTTP headers from the session's cached tool schema.
A fresh bare call lacks that cache, while the accepted discovery-first retained
daemon flow lists tools and then correctly returns an integer schema error and
`region=EU` success. Wirecmd did not add custom protocol-header logic.

Linux Codex permission profiles granted all conditions the same direct command
network authority because exact Unix-socket network allow rules are not
available there. Filesystem access remained default-deny apart from each arm's
assigned workspace, inputs, runtime, state, tools, and pinned Codex runtime.
A real Luna sentinel turn and every per-arm offline preflight confirmed that
repository source, Codex authentication, sibling configuration, and parent-only
fixtures were unreadable. Native stdio MCP processes are outside the command
profile boundary, so only pinned official fixtures launched through
environment-clearing parent wrappers were registered.

## Qualification commands

The matrix was staged with `scripts/eval/prepare-comparative-matrix.sh` and run
with `scripts/eval/run-comparative-matrix.sh`; exact public forms and lifecycle
gates are recorded in `comparative-matrix-scaffold.md`. After evaluation:

```text
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go mod verify
git diff --check
```

All passed when rerun with host loopback access. The first sandboxed test
attempt failed at `httptest` listener creation with `socket: operation not
permitted`; the same unchanged command passed outside that outer sandbox.

## Interpretation

This evidence supports the interaction thesis: Wirecmd matched native task
reliability while preserving shell composition, and did so with much lower
observed interaction cost than the maintained CLI comparison. Native remained
the latency baseline and required fewer explicit discovery actions, but could
not compose MCP calls inside a shell program. The result is strong enough to
continue product work; it is not evidence of broad MCP compatibility, OAuth,
legacy protocol coverage, production daemon operations, or causality from four
repetitions per condition.
