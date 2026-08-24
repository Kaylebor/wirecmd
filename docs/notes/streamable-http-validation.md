# Streamable HTTP validation evidence

Status: non-authoritative implementation evidence.

## Fixture

The repository test fixture uses the pinned `github.com/modelcontextprotocol/
go-sdk` v1.7.0 `mcp.NewStreamableHTTPHandler` with
`StreamableHTTPOptions{Stateless: true}`. It exposes paginated tool discovery,
focused schema help, a projected-argument tool, exact JSON calls, and a
tool-reported failure.

## Qualification commands

Run from the repository root:

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build -buildvcs=false ./...
go mod verify
git diff --check
```

The HTTP-focused coverage is in `TestDirectStreamableHTTPContracts` and
`TestDaemonStreamableHTTPContracts`. Both use `http "<fixture URL>"` KDL and
exercise the public CLI through the same direct and daemon-backed paths used by
ordinary callers.

## Cancellation limitation in pinned SDK qualification

On the v1.7.0 stateless fixture, cancellation causes the SDK to send a
`notifications/cancelled` request. The fixture responds `400 Bad Request`, and
the SDK closes that retained client session. Wirecmd therefore marks that
instance broken and returns `instance_unavailable` for later requests until a
daemon reload or restart; it never retries, replaces, or replays the call.
The fixture handler is released during test cleanup because remote cancellation
delivery is asynchronous and is not a Wirecmd guarantee.

## Deferred qualification

The fixture is a modern stateless SDK server. OAuth, typed headers/query
entries, standalone SSE, stateful legacy Streamable HTTP, and legacy HTTP+SSE
remain separate work. A later milestone evaluation must also run a black-box
agent against mixed stdio and HTTP sources and record shell-composition and
recovery evidence.
