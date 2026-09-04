# Legacy MCP fixture

`legacy-mcp` is a small, test-only executable built in its own Go module with
the official `github.com/modelcontextprotocol/go-sdk` **v1.6.1**. It exists to
qualify Wirecmd against a historical initialized server without adding that
older SDK to Wirecmd's production module graph. It is intentionally thin and
may be extracted into a separate fixture repository if it grows.

It exposes four tools:

- `set_value` with `{ "value": "..." }`
- `read_value`
- `delete_value`
- `fail`, which returns an intentional MCP tool-reported error

By default it serves stdio. Its value is process-local: calls on the same
fixture process share it until that process exits. Build it from this directory:

```sh
go build -o legacy-mcp .
```

Configure the resulting executable as a stdio MCP server, for example:

```kdl
wirecmd {
  server "legacy" {
    scope "workspace"
    stdio "/absolute/path/to/legacy-mcp"
  }
}
```

For Streamable HTTP, pass an explicit loopback address. Its value is
session-local: calls on one MCP session share it, while a fresh HTTP session
starts empty. The ready endpoint is written to stderr (never stdout):

```sh
./legacy-mcp --listen 127.0.0.1:8081
# legacy-mcp listening: http://127.0.0.1:8081/mcp
```

Use `--protocol-record PATH` only in tests to append one JSON object per
initialized session. It records the protocol version from the client's
initialize request as observed by the official SDK's initialized-session
handler, not the server's negotiated response. This is fixture diagnostics, not
a Wirecmd output feature:

```sh
./legacy-mcp --listen 127.0.0.1:8081 --protocol-record /tmp/legacy-protocol.jsonl
# {"protocol_version":"2025-11-25"}
```

The fixture has no credentials, persistence, or public distribution contract.
