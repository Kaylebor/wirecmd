# Legacy initialized transport qualification

Status: non-authoritative implementation evidence, 2026-08-25

Wirecmd was exercised against the isolated fixture in
`testdata/legacy-mcp`, built with the official Go MCP SDK v1.6.1. That release
predates modern `server/discover`; its initialized handler recorded
`2025-11-25` for both stdio and stateful Streamable HTTP sessions. The root
Wirecmd module remained pinned to SDK v1.7.0.

## Build and configuration

```text
go build -buildvcs=false -o /tmp/wirecmd-qualification .

cd testdata/legacy-mcp
go build -buildvcs=false -o /tmp/wirecmd-legacy-mcp-fixture .

/tmp/wirecmd-legacy-mcp-fixture \
  --listen 127.0.0.1:19876 \
  --protocol-record /tmp/wirecmd-legacy-http-protocol.jsonl
```

The repeatable form is:

```text
scripts/qualify-legacy.sh
```

That script builds both modules, allocates a temporary loopback endpoint,
generates the complete temporary KDL configuration, starts a foreground daemon
in a private `0700` runtime directory, runs the checks below, validates both
protocol records, and removes all temporary state. The stdio command receives a
separate `--protocol-record` path.

## Observed contract

Separate daemon-backed CLI processes set and then read process/session-local
values through both transports:

```json
{"ok":true,"result":{"data":{"exists":true,"value":"stdio-retained"},"messages":["{\"exists\":true,\"value\":\"stdio-retained\"}"]},"server":"legacy-stdio","tool":"read_value"}
{"ok":true,"result":{"data":{"exists":true,"value":"http-retained"},"messages":["{\"exists\":true,\"value\":\"http-retained\"}"]},"server":"legacy-http","tool":"read_value"}
```

Fresh `--direct` invocations for both transports returned `exists:false`,
proving that daemon continuity was not supplied by shared global fixture state.
Both transports classified the fixture's deliberate tool error identically as
`upstream_tool/tool_reported_error` with exit code `5`.

The fixture's initialized handlers recorded the actual protocol version in the
client's legacy `initialize` request:

```json
{"protocol_version":"2025-11-25"}
```

`wirecmd daemon reload` reported one retired context and two retired instances.
The next daemon-backed read over each transport returned `exists:false`, showing
that both legacy sessions were retired and recreated.

## Scope of the evidence

The pinned v1.6.1 server accepts that supported requested version and returns it
in `InitializeResult`; the record is evidence of the initialization path and
request revision, not a generic independent negotiation tracer.

This qualifies legacy initialized MCP over stdio and stateful Streamable HTTP
for the current Wirecmd discovery/call/result contract and minimal daemon
lifecycle. It does not qualify the older HTTP+SSE transport, OAuth, rich MCP
primitives, or every intermediate legacy revision. The SDK owns fallback,
initialization, pagination, calls, and shutdown; Wirecmd added no legacy
protocol implementation.
