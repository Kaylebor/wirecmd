# Native LSP definition validation

Status: non-authoritative validation evidence, 2026-09-05

This records the first real-server check for the native LSP definition slice.
The configured executable is test evidence, not a Wirecmd default or language
catalog entry.

## Environment

- Wirecmd source: `752b154941ec22c7e815646898c8a7abdea0158a` plus the uncommitted native-LSP slice
- Go: `go1.27.0 linux/amd64`
- LSP server: `golang.org/x/tools/gopls v0.23.0`
- Workspace: `/tmp/wirecmd-lsp`
- LSP command: the installed `gopls serve`, supplied entirely by explicit KDL
- `XDG_CACHE_HOME` and `GOCACHE`: private paths below `/tmp/wirecmd-lsp-gopls-cache`

The explicit configuration contained one workspace-scoped `lsp
"qualification"` definition with `language-id "go"`, the installed executable,
and the `serve` argument. No production source selected or constructed gopls.

## Direct qualification

```text
env GOCACHE=/tmp/wirecmd-lsp-gocache go build -buildvcs=false -o /tmp/wirecmd-lsp-bin .
/tmp/wirecmd-lsp-bin --direct --config /tmp/wirecmd-lsp-gopls.kdl lsp definition --file main.go --line 21 --column 13
```

Observed exit code: `0`. Observed stdout:

```json
{"ok":true,"lsp":{"operation":"definition","file":"/tmp/wirecmd-lsp/main.go","locations":[{"path":"/tmp/wirecmd-lsp/internal/cli/cli.go","range":{"start":{"line":47,"column":6},"end":{"line":47,"column":9}}}]}}
```

Stderr was empty.

## Retained-daemon qualification

The daemon used a private `0700` runtime directory at
`/tmp/wirecmd-lsp-runtime`. Two separate CLI invocations ran the same definition
operation against it. Both returned the same semantic location as direct mode.

`wirecmd daemon reload` reported one retired context and one retired instance.
The next definition call returned the same location through a fresh session.
`wirecmd daemon status` then reported generation `1` with one healthy retained
instance:

```json
{"daemon":{"active_instances":1,"broken_instances":0,"cached_contexts":1,"generation":1,"pid":2467378,"protocol":5,"retiring_instances":0,"status":"running"},"ok":true}
```

The daemon was stopped with SIGINT after the check. This demonstrates process
retention and direct/daemon result equivalence for one real server. It does not
qualify other servers, languages, LSP operations, dynamic registration, or
unsaved editor buffers.

## Multi-provider routing requalification

Status: non-authoritative validation evidence, 2026-09-07

After the automatic multi-provider routing change, the same installed `gopls`
was configured with the current selector-based syntax:

```kdl
lsp "qualification" {
    implementation-id "golang.org/x/tools/gopls"
    selector language-id="go" pattern="**/*.go"
    stdio "gopls" {
        arg "serve"
    }
}
```

The executable path is qualification evidence only. The command was:

```text
XDG_CACHE_HOME=/tmp/wirecmd-lsp-gopls-cache /tmp/wirecmd-multi-lsp-bin --direct --config ./wirecmd-multi-lsp-gopls.kdl lsp definition --file main.go --line 21 --column 13
```

Observed exit code: `0`. The result attributed the existing `cli.Run`
definition location to provider `qualification`, and its provider outcome was
`ok` with one location. A direct `lsp status --file main.go` reported the
selector as matched and runtime status `not_checked`, confirming that status
did not start the process. The automated fixture suite separately covered two
overlapping providers, partial failure, all navigation operations, daemon
retention, provider ordering, cross-provider overlap, and per-provider
serialization.
