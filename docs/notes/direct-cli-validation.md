# Direct CLI validation evidence

Status: non-authoritative validation record, 2026-08-24

This records the direct-mode qualification for the first CLI slice. It does
not qualify the daemon path, HTTP, focused help, or legacy MCP revisions.

## Fixture

The fixture was the official `go-sdk` v1.7.0 `examples/server/memory` program,
built without its optional `-memory` durable-storage flag. Its Wirecmd source
was:

```kdl
wirecmd {
    server "memory" {
        scope "workspace"
        stdio "/tmp/wirecmd-memory"
    }
}
```

## Commands and observations

```sh
SDK_ROOT="$(go env GOMODCACHE)/github.com/modelcontextprotocol/go-sdk@v1.7.0"
go -C "$SDK_ROOT" build -buildvcs=false -o /tmp/wirecmd-memory ./examples/server/memory
go -C . build -buildvcs=false -o /tmp/wirecmd .
/tmp/wirecmd --direct --config /tmp/wirecmd-direct-memory.kdl memory
/tmp/wirecmd --direct --config /tmp/wirecmd-direct-memory.kdl memory '{"tool":"read_graph"}'
/tmp/wirecmd --direct --config /tmp/wirecmd-direct-memory.kdl --json '{"entities":[{"name":"Ada","entityType":"person","observations":[]}]}' memory create_entities
/tmp/wirecmd --direct --config /tmp/wirecmd-direct-memory.kdl memory read_graph
```

All commands exited zero. Tool listing produced the nine expected memory
tools in name order. The first `read_graph` returned a null/empty graph. The
`create_entities` call returned `Ada`. The final, separate direct invocation
again returned an empty graph, proving direct mode starts a fresh in-memory
server process.

The official fixture deliberately uses the SDK logging transport, so its MCP
wire diagnostics appeared on stderr. Wirecmd's result envelopes remained the
only stdout output.

## SDK connection-failure cleanup

The pinned SDK's `CommandTransport.Connect` starts its child before
`Client.Connect` completes initialization. In v1.7.0, some later
`Client.Connect` error returns, including an unsupported legacy initialize
protocol version, return without a client session for the caller to close.
Wirecmd retains the SDK connection returned by `CommandTransport` until
`Client.Connect` has returned a session. On an error it closes that connection.
The SDK's connection close is idempotent and uses the normal
`CommandTransport` shutdown path, including waiting for the child, so this does
not race or replace normal SDK session shutdown.
