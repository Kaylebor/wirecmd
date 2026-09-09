---
name: wirecmd
description: Discover and compose Wirecmd capabilities from the shell, including trusted workspace configuration, focused help, native LSP navigation and inspection, projected arguments, lossless JSON calls, and transparent OAuth-backed HTTP servers.
---

# Wirecmd

Use Wirecmd as a shell-native capability interface. Keep client flags before
the server and tool names. Pipes default to compact JSON; terminals default to
readable discovery/administration/errors and indented JSON tool results. Use
`--format json --color never` before positional names whenever machine output
is required, especially in a PTY. Send no assumptions about the upstream
protocol into a call.

`--format pretty` requests readable output without requiring a terminal.
`--color` (alias `--colour`) independently accepts `auto`, `always`, or `never`;
do not rely on a harness setting `NO_COLOR`. Successful help remains plain
text. Output choices do not disable OAuth: use `WIRECMD_NONINTERACTIVE=1`
separately when browser interaction is not permitted.

## Select configuration

Normal calls need `wirecmd daemon run` in a persistent foreground terminal or
process session. Do not pass it --config; configuration is selected by each
caller. On Linux, XDG_RUNTIME_DIR must be absolute, private, and same-user;
macOS can use its validated per-user temporary directory when unset.
`wirecmd daemon reload` refreshes cached configuration and retires sessions.
Use offline `wirecmd --help daemon`, `--help config`, or `--help auth` before
guessing administrative syntax. Do not replace a user's daemon without approval.

When `--config` is omitted, Wirecmd loads the global KDL file at
an absolute `$XDG_CONFIG_HOME/wirecmd/config.kdl`, falling back to
`~/.config/wirecmd/config.kdl`, then composes trusted workspace `wirecmd.kdl`
files from the trusted root to the current directory. Repeated `--config PATH`
options replace discovery completely and preserve their weakest-to-strongest
order.

Manage trust explicitly when a workspace is not yet approved:

```sh
wirecmd config trust [PATH]
wirecmd config untrust [PATH]
wirecmd config trust status [PATH]
wirecmd config trust list
```

The optional path defaults to the current directory. A missing approval is the
recoverable `workspace_untrusted` error (exit 8); no discovered source is
`config_not_found` (exit 3). These commands remain non-interactive. A server
named `config` can still be called through `wirecmd mcp config`.

## Use native LSP navigation and inspection

LSP navigation is a native capability rather than an MCP tool. Begin with
offline help:

```sh
wirecmd --help lsp
wirecmd --help lsp definition
```

The effective KDL configuration supplies one or more `lsp` blocks. Wirecmd
never selects or constructs a language server. The workspace config supplies
the executable, arguments, environment, and selector language IDs:

```kdl
lsp "primary" {
    implementation-id "optional-stable-metadata"
    selector language-id="your-language-id"
    stdio "your-language-server" {
        arg "--server-specific-option"
    }
}
```

Selectors optionally accept a workspace-relative `pattern`, defaulting to
`**/*`. Matching definitions are queried concurrently and their locations are
attributed to the provider. Query a saved UTF-8 file with one-based coordinates:

```sh
wirecmd lsp definition --file ./main.go --line 21 --column 13
wirecmd lsp references --file ./main.go --line 21 --column 13
wirecmd lsp hover --file ./main.go --line 21 --column 13
wirecmd lsp signature-help --file ./main.go --line 21 --column 13
wirecmd lsp document-symbols --file ./main.go
wirecmd lsp workspace-symbols --query 'Wirecmd'
wirecmd lsp status --file ./main.go
```

Available operations are `definition`, `declaration`, `type-definition`,
`implementation`, and `references`; references exclude declarations unless
`--include-declaration` is supplied. Read-only inspection also provides
`hover`, `signature-help`, `document-symbols`, and `workspace-symbols`. Hover preserves ordered
plaintext, Markdown, and code blocks. Signature help preserves provider-attributed
callable labels, active states, optional plaintext/Markdown documentation, and
parameter labels resolved from validated UTF-16 offsets. Document symbols preserve nested
children; workspace symbols pass the explicit query unchanged to all
configured providers, including an empty query, without local ranking or
truncation. Normal calls retain selected sessions through the daemon; use
`--direct` only for one-shot diagnosis. Navigation results are normalized file
locations with one-based ranges, while inspection uses its own hover,
signature, or symbol collections and matching per-provider counts. Partial
provider failures are reported in structured provider
outcomes without noisy client stderr. Use `lsp status` to inspect configured
selectors and already-observed runtime identity and capabilities without
starting a process. `lsp_not_configured` and `lsp_no_matching_provider` mean
configuration or selector routing needs attention. `lsp_capability_unavailable`
means no matching server supports the operation; `lsp_encoding_unsupported`
requires UTF-16 support.

The bare `lsp` form is native help. A configured MCP server named `lsp` remains
callable through `wirecmd mcp lsp`. Do not assume any language/server catalog,
initialization options, unsaved-buffer support, mutating operations, or dynamic
completion.

## Configure HTTP values

Streamable HTTP endpoints can declare structural query and header values:

```kdl
http "https://example.test/mcp" {
    query tenant="acme"
    query token=(secret)"env://API_TOKEN"
    header X-API-Key=(secret)"env://API_KEY"
    header Authorization=(secret)"env://AUTHORIZATION"
}
```

Unannotated values are literals. `(secret)"env://NAME"` reads an environment
value when the selected server is executed. Query names are case-sensitive;
header names are case-insensitive. A stronger source replaces a matching key
without moving it and appends new keys. Existing endpoint query parameters are
preserved unless a structural query entry has the same key. The
`Authorization` value is complete, for example `Bearer ...`. Templates and
dynamic per-request headers are not part of this slice. HTTP and MCP
transport-owned headers are reserved and rejected; see the [HTTP values
plan](../../docs/http-values-plan.md) for the complete list.

Listing and help do not resolve secrets for unselected servers. In daemon mode,
resolved startup credentials distinguish retained instances, so one server
definition cannot reuse an instance started with different credentials.

## Authenticate protected HTTP servers

When an HTTP server has no configured `Authorization` header, Wirecmd can use
the official SDK's OAuth implementation. Dynamic client registration is
automatic. Configure a preregistered client only when the provider requires
one:

```kdl
http "https://example.test/mcp" {
    oauth {
        client-id "wirecmd-client"
        client-secret (secret)"env://OAUTH_CLIENT_SECRET"
        redirect-uri "http://127.0.0.1:8765/callback"
    }
}
```

The client ID and exact loopback redirect URI are required in the `oauth`
block; the client secret is optional. A static `Authorization` header disables
OAuth and cannot be combined with that block.

Manage local credentials with:

```sh
wirecmd auth login SERVER
wirecmd auth status SERVER
wirecmd auth logout SERVER
```

Normal commands and auth administration use the daemon. Use `--direct` only
for deliberate one-shot testing or diagnosis. `status` is local-only;
`logout` removes Wirecmd's local credential and retires matching daemon
sessions. OAuth URLs and browser diagnostics go to stderr, never stdout.

When stdin and stderr are TTYs, an ordinary protected call may open a browser
and wait for authorization. Set `WIRECMD_NONINTERACTIVE=1` for scripts, CI,
and other callers that must receive `authorization_required` instead. Explicit
login also requires a local interactive terminal.

Wirecmd stores encrypted OAuth state using a random master key held by the
native keyring through `go-keyring`; there is no plaintext fallback. The MCP
SDK owns OAuth discovery, PKCE, registration, token exchange, refresh, issuer
validation, and retry behavior. Wirecmd owns only persistence, daemon
coordination, redaction, and shell error mapping.

## Discover before calling

Start by listing configured servers, then list the selected server's tools.
Request focused help for a tool before guessing its input shape:

```sh
wirecmd mcp
wirecmd mcp SERVER
wirecmd --help mcp SERVER tool TOOL
wirecmd mcp SERVER tool TOOL --help
```

MCP resources are separate from tools:

```sh
wirecmd mcp SERVER resources
wirecmd mcp SERVER resource-templates
wirecmd mcp SERVER resource URI
```

Lists are deterministic. Resource reads preserve upstream content order, using
text directly and base64 for blobs. Do not supply tool JSON or projected flags
to these operations.

Use `--config PATH` in these forms when an explicit source list is required.
Servers named daemon/config/auth/lsp remain reachable through `wirecmd mcp
SERVER`. The tool-side separator remains the raw argument overlay.

`wirecmd mcp SERVER --help` is equivalent to server help. For a configured
server literally named `--help` or `-h`, use `wirecmd mcp -- --help` or
`wirecmd mcp -- -h`; focused help uses the corresponding prefix form. MCP server and tool help
requires live metadata, so start the daemon or use `--direct` for a deliberate
one-shot request; it cannot fall back while the daemon is offline. Native and
administrative commands accept a final `--help` or `-h`. After `mcp SERVER tool TOOL`, a
final help flag uses the live schema: an explicit projected `help` property
receives it; otherwise Wirecmd renders tool help.

The distinct prefix form `wirecmd --help mcp --` inspects a configured server
literally named `--`; it is not shorthand for the `--help` alias escape.

Focused help is readable text. It identifies simple projected flags, the
original JSON names and types, and properties that need JSON input or a
fallback path.

## Choose the smallest clear call form

Use projected flags for straightforward top-level values:

```sh
wirecmd --config PATH mcp SERVER tool TOOL --query 'text' --limit 10 --enabled
```

For a named complex value, pass one JSON value to its documented flag:

```sh
wirecmd --config PATH mcp SERVER tool TOOL --filters '{"status":["open"]}'
```

Use `--` followed by exactly one JSON object to add properties that cannot be
projected uniquely, such as name-normalization collisions. Do not repeat a key
already supplied by a projected flag:

```sh
wirecmd --config PATH mcp SERVER tool TOOL --query 'text' -- '{"toolName":"value"}'
```

When the full object is clearer, use a lossless JSON path instead:

```sh
wirecmd --config PATH --json '{"query":"text"}' mcp SERVER tool TOOL
printf '%s\n' '{"query":"text"}' | wirecmd --config PATH --stdin mcp SERVER tool TOOL
wirecmd --config PATH mcp SERVER '{"tool":"TOOL","arguments":{"query":"text"}}'
```

## Compose and recover

Pipe ordinary JSON results through standard shell tools when it makes the next
step clearer. Calls are non-interactive unless both stdin and stderr are TTYs
and `WIRECMD_NONINTERACTIVE` is not `1`; in that interactive case protected
HTTP calls may open a browser for OAuth. Inspect the structured error envelope
and exit status when one fails; its category, code, and action indicate
whether to correct invocation, configuration, authentication, transport, or an
upstream tool failure.

Normal calls require the local daemon. Use `--direct` only for deliberate
one-shot testing or diagnostics; it does not retain server state between
invocations. See the [OAuth plan](../../docs/oauth-plan.md) for the accepted
credential and provider-qualification boundary.
