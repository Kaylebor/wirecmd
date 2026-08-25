# Comparative matrix staging scaffold

Status: non-authoritative evaluation infrastructure note, 2026-08-25.

`scripts/eval/prepare-comparative-matrix.sh` stages the fixed matrix required
by the authoritative validation plan. It deliberately does not run a daemon,
fixture, session, or model. Those must remain parent-owned so that a run has
one accountable lifecycle transcript instead of opaque helper behavior.

## Frozen matrix

The script creates twelve fresh arms: two cold and two warm repetitions each
for Wirecmd, mcpc v0.6.0, and native Codex MCP. The fixed schedule rotates the
three conditions in three Latin-square orders and repeats the first order once;
with four repetitions, an exactly equal count at each serial position is
mathematically impossible. Each condition appears first, second, and third at
least once, and gets one additional occurrence at a different position.

The shared task lives in `scripts/eval/comparative-task.txt`. It explicitly
requires that the deterministic sentence come from the same HTTP-backed source
used later for header validation. Condition bootstrap text and the committed
Wirecmd Skill are recorded separately because they are part of the evaluated
interface, but the task body is byte-identical in every arm.

Each staged arm has a private `workspace/`, `tools/`, `inputs/`, `runtime/`,
and `state/` directory compatible with `codex-profile-exec.sh`. Its parent-only
directory contains the intended condition, cold/warm label, preassigned
loopback port, native registration forms, and hashes. The permission profile
does not grant the arm root itself, so the evaluator cannot read that metadata
or the output transcript beside its assigned directories.

The script gives model tools only their assigned condition's client/runtime.
Wirecmd's stdio fixture binaries and native MCP wrappers instead live under an
ungranted parent-only directory; mcpc necessarily receives its own client plus
the stdio fixture executables it starts. The supplied immutable mcpc v0.6.0
runtime is hard-linked only into mcpc arms. The script records both the mcpc
entrypoint hash and a recursive source-tree digest. The runtime must not be
modified while a staged matrix exists; model tools receive it read-only.

## Parent execution gates

Before releasing an evaluator for one arm, the parent executor must:

1. run `profile-preflight.sh` against that exact arm and the pinned native
   Codex executable, including the final repository and auth negative reads;
2. use a separate disposable preflight fixture set to establish that the
   stdio and stateless HTTP binaries work, then stop it before starting this
   arm's measured fixtures;
3. start one fresh loopback conformance server on the port in `parent/arm.env`;
   start all stdio servers only through the selected interface; and retain
   parent-visible PID, readiness command/result, stderr, and start timestamp;
4. for Wirecmd, start the foreground daemon with only this arm's
   `XDG_RUNTIME_DIR`, verify daemon readiness, and keep it parent-owned;
5. for a warm arm only, establish the relevant connection and tool listing
   before release, without creating task graph state; record the exact warm
   commands and their output. Cold arms receive no agent-visible discovery or
   call;
6. invoke `codex-profile-exec.sh` with the arm root, pinned GPT-5.6 Luna,
   `--allow-network`, and the arm's assigned `CAPABILITY_CONFIG`. Native uses
   the recorded `--native-mcp-root`, two stdio registrations, and one loopback
   HTTP registration. Its wrappers must remain parent-only; the command runner
   must not add that root to the model filesystem profile. Every condition
   receives the same command-network authority; no approval/escalation is
   allowed during a run;
7. capture the runner's JSONL, parent-visible stdout/stderr, command and
   fixture timestamps, CWD, input hashes, and a redacted environment policy;
8. independently verify creation, retrieval, and final deletion against the
   parent-owned stateful fixture, then terminate all child processes, close
   mcpc sessions, remove a Wirecmd socket, and check no arm child remains.

Any failed isolation check, escalation, alternative-interface use, missing
parent evidence, or incomplete cleanup makes a repetition a non-comparable
harness artifact. It may be retained as a smoke result but must not enter the
two-cold/two-warm comparative evidence set.

## Staging example

The following only creates inputs; it does not contact a model or start a
fixture:

```sh
scripts/eval/prepare-comparative-matrix.sh \
  --run-root /tmp/wirecmd-matrix-YYYYMMDD \
  --codex-bin /path/to/pinned/native/codex \
  --wirecmd /path/to/wirecmd \
  --memory /path/to/memory-server \
  --everything /path/to/everything-server \
  --conformance /path/to/conformance-server \
  --mcpc-runtime /path/to/mcpc-v0.6.0-runtime
```

Use the generated `plan.tsv` sequentially. Do not reuse an arm, port, daemon,
workspace, state directory, fixture process, or Codex conversation after a
repetition.

## Parent execution command

After separately qualifying final filesystem paths and the preflight fixture
set, invoke every staged arm with:

```sh
scripts/eval/run-comparative-matrix.sh \
  --run-root /tmp/wirecmd-matrix-YYYYMMDD \
  --codex-bin /path/to/pinned/native/codex
```

The executor starts one stateless conformance process per arm, records an MCP
initialize readiness exchange, starts a Wirecmd foreground daemon only for
Wirecmd, and always closes its own process groups. It performs the documented
warm connection/listing work before the evaluator only for warm arms. It also
captures lifecycle timestamps, fixture and daemon stderr, parent verification
output, and the profile runner's JSONL/stderr/meta artifacts.

Qualification evidence for this setup found that the official SDK validates
the conformance server's header schema after its tool list has been fetched by
the retained Wirecmd daemon: an integer `region` then reaches the upstream
validation error, while a bare fresh call can fail locally without that cached
schema. This is non-authoritative harness evidence, not an SDK contract. The
executor therefore requires parent-visible agent discovery before the composed
call and header checks: a Wirecmd remote-server list/help, or an mcpc
agent-visible tool list/help/search. It records that command index alongside
the semantic task evidence.

Before each arm, the executor reruns `profile-preflight.sh` against that exact
layout. It blocks repository source, a sibling arm's configuration, and either
the arm's parent metadata or its parent-only native wrapper. A failed preflight
skips the evaluator and leaves the arm non-comparable.

For mcpc, parent verification uses the active `@memory` session, then closes
the three expected sessions and records the resulting session inventory. Native
MCP's process-local graph cannot be queried accurately through a second
connection, so the parent instead verifies the host-captured Codex JSONL with
`jq`. It requires, in order, the completed deterministic-text, exact entity
creation, retrieval, integer header failure, EU retry, deletion, and empty
retrieval MCP calls. The resulting parent evidence records call indexes and
the expected sentence/identifier; it is not based on the model's final report.

Wirecmd and mcpc additionally require parent `jq` verification of their
captured Codex command transcript. The transcript must show an exit-zero,
single shell command containing the assigned client, `test_simple_text`, and
`create_entities`, with the expected sentence, identifier, entity type,
observation, and creation result. It must then show later exact retrieval, a
header failure whose diagnostics establish an integer where a string was
required, the `EU` success, and ordered deletion followed by an empty
retrieval. This transcript evidence and the separate parent live graph check
both must pass.

Native MCP is eagerly registered for both `cold` and `warm` matrix labels.
That eager registration/catalog is the interface's actual preparation in both
strata; the native `warm` label does **not** mean a retained native session or
invented shell-client cache. Record this distinction in analysis rather than
comparing native warm latency as if it had the same cache boundary as the two
shell clients.
