# Mixed transport validation evidence

Status: non-authoritative implementation and product-spike evidence.

Observed on 2026-08-24 against commit `ed261be` with the pinned official
`github.com/modelcontextprotocol/go-sdk` v1.7.0 fixtures. Temporary binaries,
configuration, runtime sockets, and fixture processes lived under
`/tmp/wirecmd-qualification.wiIzkF`; they are not product configuration.

## Configured fixture matrix

The temporary KDL configuration contained:

- the official `examples/server/memory` server over stdio, without `-memory`;
- the official `examples/server/everything` server over stdio;
- the same memory and everything examples over their default stateful
  Streamable HTTP handlers;
- the official `conformance/everything-server` over stdio; and
- the conformance server over Streamable HTTP with `-stateless=true`.

This qualifies the two combinations currently claimed by the milestone:
modern stdio and modern stateless Streamable HTTP. The default stateful HTTP
examples reject modern `2026-07-28` discovery, causing the pinned SDK client to
fall back to legacy initialization. Their successful calls are therefore
additional legacy-transport smoke evidence, not full legacy qualification.
Legacy initialized revisions and legacy HTTP+SSE remain unqualified and
unsupported claims must not be inferred from the SDK's own compatibility.

The official fixtures were built with:

```sh
go build -buildvcs=false -o /tmp/wirecmd-qualification.wiIzkF/wirecmd .

cd $(go env GOMODCACHE)/github.com/modelcontextprotocol/go-sdk@v1.7.0
go build -buildvcs=false \
  -o /tmp/wirecmd-qualification.wiIzkF/memory-server \
  ./examples/server/memory
go build -buildvcs=false \
  -o /tmp/wirecmd-qualification.wiIzkF/everything-server \
  ./examples/server/everything
go build -buildvcs=false \
  -o /tmp/wirecmd-qualification.wiIzkF/conformance-server \
  ./conformance/everything-server
```

The external HTTP fixtures ran on loopback only:

```sh
/tmp/wirecmd-qualification.wiIzkF/memory-server \
  -http 127.0.0.1:18765
/tmp/wirecmd-qualification.wiIzkF/everything-server \
  -http 127.0.0.1:18766
/tmp/wirecmd-qualification.wiIzkF/conformance-server \
  -http 127.0.0.1:18767 -stateless=true
```

## Direct and daemon contract results

Server discovery returned all configured servers in configuration order with
the expected `stdio` or `http` transport label. Tool discovery succeeded for
official memory servers over both transports. The everything fixture's
`greet` tool returned `Hi Ada` over both stdio and HTTP. Its
`greet (structured)` exact-envelope call returned the same normalized data and
messages through daemon-backed modern stdio and the legacy-initialized stateful
HTTP smoke path. Exact captured stdout was:

```json
{"ok":true,"result":{"data":{"message":"Hi Ada"},"messages":["{\"message\":\"Hi Ada\"}"]},"server":"everything-stdio","tool":"greet (structured)"}
{"ok":true,"result":{"data":{"message":"Hi Ada"},"messages":["{\"message\":\"Hi Ada\"}"]},"server":"everything-http","tool":"greet (structured)"}
```

The stateless HTTP conformance fixture returned its deterministic simple text
through direct and daemon-backed calls. Error classification was also observed
with exact exit codes:

```text
test_simple_text      -> exit 0, text success
test_error_handling   -> exit 5, upstream_tool/tool_reported_error
test_image_content    -> exit 6, upstream_protocol/unsupported_result
```

The tool-reported error retained the fixture message under `result.messages`
and instructed the caller to inspect and correct the invocation. The rich
image result produced the intentionally deferred unsupported-result error.

## Retained state and reload

With a private `XDG_RUNTIME_DIR`, a foreground daemon received separate CLI
processes that created `DaemonAda` in the stdio memory fixture and then read it
back. A fresh `--direct` call returned an empty graph. The exact administrative
reload then reported:

```json
{"ok":true,"reload":{"contexts_retired":1,"instances_retired":3}}
```

The next daemon-backed `read_graph` returned an empty graph and daemon status
reported generation `1`, one new active instance, and zero broken instances.
This demonstrates process-local continuity, deliberate direct isolation, and
retirement on reload.

## One-shell mixed transport composition

One shell program called the stateless HTTP conformance fixture, extracted and
trimmed its text with `jq` and `sed`, then passed the transformed value to the
stdio everything fixture through the same daemon:

```sh
message="$(wirecmd --config qualification.kdl \
  conformance-http-stateless test_simple_text \
  | jq -r '.result.messages[0]' \
  | sed 's/ for testing\.$//')"

wirecmd --config qualification.kdl \
  everything-stdio greet --name "$message"
```

Observed final result:

```json
{"ok":true,"result":{"data":null,"messages":["Hi This is a simple text response"]},"server":"everything-stdio","tool":"greet"}
```

An independent subagent was then given only the compiled binary, config path,
runtime directory, and a task. Without inspecting source or the config file, it
used CLI discovery to compose an HTTP `test_simple_text` result through `tr`
into an exact-JSON stdio `create_entities` call in one shell invocation. It
also triggered an invalid `create_entities` call, classified the exit-5
structured error, corrected the argument shape, and succeeded. It removed the
temporary entity afterward.

The subagent initially observed `operation not permitted` when its sandbox
blocked loopback access. Re-running the identical diagnostic with local socket
permission succeeded. This was an execution-sandbox boundary, not an MCP,
credential, configuration, or Wirecmd recovery result.

## Automated gates

The following passed after the black-box fixture work:

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -buildvcs=false ./...
go mod verify
git -c core.fsmonitor=false diff --check
```

The non-escalated test runs could not bind `httptest` loopback sockets in the
execution sandbox; identical host-permitted runs passed. No dependency or
implementation change was needed.

## Remaining milestone gap

This run shows the current transports can be discovered and composed by an
independent shell-capable agent. It is not yet the complete comparative
milestone evaluation: equivalent tasks still need measured runs through native
harness MCP exposure and at least one maintained MCP CLI, with context size,
round trips, malformed calls, cold/warm latency, and qualitative confusion
recorded consistently.
