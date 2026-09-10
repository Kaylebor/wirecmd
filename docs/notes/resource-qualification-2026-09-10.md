# MCP resource qualification evidence, 2026-09-10

Status: non-authoritative local qualification evidence. This records the
official Go MCP SDK v1.7.0 `conformance/everything-server` fixture used for the
resources slice. It contains no credentials or persistent local state.

## Build and configuration

The candidate and official fixture were built into a disposable directory:

```sh
qualification_dir="$(mktemp -d)"
go build -buildvcs=false -o "$qualification_dir/wirecmd" .
(
    cd "$(go env GOMODCACHE)/github.com/modelcontextprotocol/go-sdk@v1.7.0/conformance/everything-server"
    go build -buildvcs=false -o "$qualification_dir/everything-server" .
)
```

The temporary explicit configuration was the following, with
`$qualification_dir` shown as a documentation metavariable. The actual
absolute directory returned by `mktemp` was written into the KDL file before
the commands ran; KDL does not perform shell-variable expansion.

```kdl
wirecmd {
    mcp "everything" {
        scope "workspace"
        stdio "$qualification_dir/everything-server"
    }
}
```

All calls used compact, uncolored JSON and captured stderr separately:

```sh
wirecmd() {
    "$qualification_dir/wirecmd" --format json --color never \
        --config "$qualification_dir/wirecmd.kdl" "$@"
}

wirecmd --direct mcp everything resources
wirecmd --direct mcp everything resource-templates
wirecmd --direct mcp everything resource test://static-text
wirecmd --direct mcp everything resource test://static-binary
wirecmd --direct mcp everything resource test://template/example/data
```

## Observed direct results

Each listed direct command exited `0` with empty stderr. Captured stdout was:

```json
{"ok":true,"server":"everything","resources":[{"uri":"test://static-binary","name":"static-binary","description":"A static binary resource (image) for testing","mime_type":"image/png"},{"uri":"test://static-text","name":"static-text","description":"A static text resource for testing","mime_type":"text/plain"},{"uri":"test://watched-resource","name":"watched-resource","description":"A resource that auto-updates every 3 seconds","mime_type":"text/plain"}]}
{"ok":true,"server":"everything","resource_templates":[{"uri_template":"test://template/{id}/data","name":"template","description":"A resource template with parameter substitution","mime_type":"application/json"}]}
{"ok":true,"server":"everything","uri":"test://static-text","contents":[{"uri":"test://static-text","mime_type":"text/plain","text":"This is the content of the static text resource."}]}
{"ok":true,"server":"everything","uri":"test://static-binary","contents":[{"uri":"test://static-binary","mime_type":"image/png","blob":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg=="}]}
{"ok":true,"server":"everything","uri":"test://template/example/data","contents":[{"uri":"test://template/example/data","mime_type":"application/json","text":"{\"id\": \"example\", \"templateTest\": true, \"data\": \"Data for ID: example\"}"}]}
```

The executor recorded each local direct invocation as completing in roughly
`<0.1s`. That is a smoke observation, not a benchmark. The dynamic
`test://watched-resource` was listed but deliberately not read, so this record
does not claim a timing or stability result for its update behavior.

## Retained daemon observation

The same binary/configuration ran a foreground daemon with an isolated runtime
directory:

```sh
runtime_dir="$qualification_dir/runtime"
mkdir -m 700 "$runtime_dir"
XDG_RUNTIME_DIR="$runtime_dir" "$qualification_dir/wirecmd" daemon run \
    >"$qualification_dir/daemon.stdout" 2>"$qualification_dir/daemon.stderr" &
daemon_pid=$!

XDG_RUNTIME_DIR="$runtime_dir" wirecmd mcp everything resources
XDG_RUNTIME_DIR="$runtime_dir" wirecmd mcp everything resource test://static-text
XDG_RUNTIME_DIR="$runtime_dir" "$qualification_dir/wirecmd" daemon status
kill -INT "$daemon_pid"
wait "$daemon_pid"
XDG_RUNTIME_DIR="$runtime_dir" "$qualification_dir/wirecmd" daemon status
```

The resource listing and static-text read each exited `0` with empty stderr.
Before shutdown, daemon status reported one cached context, one active instance,
zero broken instances, on private IPC protocol `10`; this confirms the second
CLI process reused a retained initialized stdio session. After `SIGINT`, status
exited `7` with `daemon_unavailable`, and the socket was removed. This evidence covers normal
resource list/read continuity and shutdown only; it does not claim resource
subscription, dynamic-update, or provider-specific behavior.
