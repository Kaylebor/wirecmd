# Wire Command (`wirecmd`)

This repository explores a shell-native capability runtime for agents, humans,
scripts, and CI. Its first upstream adapter is the Model Context Protocol
(MCP), but MCP is not intended to be part of the harness-facing contract.

The intended dependency direction is:

```text
agent / human / automation
          |
          v
        shell
          |
          v
 capability CLI and local daemon
          |
          v
   MCP servers and future adapters
```

A shell-capable agent should be able to discover capabilities lazily, inspect
only the help and schemas it needs, invoke them deterministically, and compose
results with ordinary shell tools. A harness should not need native MCP
integration or eager injection of every configured tool schema.

The canonical Go module and repository path is
`github.com/Kaylebor/wirecmd`. The first intended prerelease is
`v0.1.0-alpha.1`; once that release exists, install it with Go 1.25 or newer:

```sh
go install github.com/Kaylebor/wirecmd@latest
```

Until the repository is public, installation also requires authenticated
GitHub access and appropriate `GOPRIVATE` configuration. The exact prerelease
can be selected with
`go install github.com/Kaylebor/wirecmd@v0.1.0-alpha.1`. Run
`wirecmd --version` to identify an installed binary; local development builds
report `wirecmd dev`.

The module path deliberately does not depend on a vanity domain. A project
website such as `wirecmd.dev` may be added independently later.

The first agent-facing validation milestone is complete: comparative evidence
supports continuing the project. Supported legacy stdio and Streamable HTTP
protocol layers are qualified; legacy HTTP+SSE remains deferred at the SDK
boundary. Automatic configuration discovery, workspace trust, typed HTTP query
and header values, and SDK-owned OAuth with encrypted credential persistence
are also complete. Normal commands use a private foreground local daemon;
`--direct` is the deliberate one-shot path for testing and diagnosis.

The published `v0.1.0-alpha.1` checkpoint is Linux-only. Native macOS 15
qualification is the next milestone: Apple Silicon is the physical target and
Intel receives native CI compatibility coverage. The candidate contract and
M2 validation gate are recorded in the
[macOS plan](docs/macos-plan.md).

Current daemon administration is intentionally small:

```sh
wirecmd daemon run
wirecmd daemon status
wirecmd daemon reload
```

Without `--config`, Wirecmd loads the global file at
`$XDG_CONFIG_HOME/wirecmd/config.kdl`, or `~/.config/wirecmd/config.kdl` when
`XDG_CONFIG_HOME` is unset or not absolute. It then composes trusted workspace `wirecmd.kdl`
files from the trusted workspace root to the caller's current directory.
Repeated `--config PATH` options instead provide the complete ordered source
list and bypass discovery and trust checks.

Manage recursive directory trust explicitly:

```sh
wirecmd config trust [PATH]
wirecmd config untrust [PATH]
wirecmd config trust status [PATH]
wirecmd config trust list
```

`PATH` defaults to the caller's current directory. These administrative forms
are non-interactive and reject ordinary execution flags. If a workspace
configuration is present without a trusted root, Wirecmd returns the
`workspace_untrusted` structured error (exit 8) with the exact trust action. If
no configuration source exists, it returns `config_not_found` (exit 3). A
server named `config` remains callable through `--json` or an exact-call
envelope.

Focused help is conventional text, so it can be read directly or filtered with
ordinary shell tools. Output defaults to readable discovery, administration,
and errors on a terminal, with indented JSON for tool results. Pipes retain
compact newline-terminated JSON. Override presentation with prefix flags:

```sh
wirecmd --format pretty --color never daemon status
wirecmd --format json --color never SERVER TOOL
```

`--format auto|json|pretty` and `--color auto|always|never` default to `auto`.
`--colour` is an exact alias; the last supplied setting wins. Automatic color
requires stdout to be a TTY, non-`dumb` `TERM`, and no non-empty `NO_COLOR`.
Explicit color settings override detection, independently of format. Agents
using a PTY should select `--format json --color never` for machine output.
Output detection does not change OAuth's separate stdin/stderr TTY checks.
Successful help stays plain text and `--version` remains standalone.

Client presentation flags must precede server/tool names; flags after the tool
name belong to the tool. `--json` still supplies tool input, not output format.
See the [output contract](docs/output-plan.md) for details.

```sh
# Discover a server's tools, then inspect the one needed.
wirecmd --config ./wirecmd.kdl --help memory
wirecmd --config ./wirecmd.kdl --help memory create_entities

# Use generated top-level flags where the schema is unambiguous.
wirecmd --config ./wirecmd.kdl memory create_entities --entities '[...]'

# Merge collision-prone or otherwise raw properties structurally.
wirecmd --config ./wirecmd.kdl server tool --simple value -- '{"tool_name":"one","toolName":"two"}'
```

`--json`, `--stdin`, and the exact-call object remain the lossless fallback for
every tool input. Wirecmd does not locally validate the full JSON Schema; the
upstream tool remains responsible for semantic validation.

Servers may use either a local stdio command or a modern Streamable HTTP
endpoint. They are mutually exclusive in an effective server definition:

```kdl
wirecmd {
    server "remote" {
        scope "workspace"
        http "https://example.test/mcp"
    }
}
```

The HTTP endpoint accepts structural query and header entries. Values are
literal strings by default or environment-backed secret references:

```kdl
http "https://example.test/mcp" {
    query tenant="acme"
    query token=(secret)"env://API_TOKEN"

    header X-API-Key=(secret)"env://API_KEY"
    header Authorization=(secret)"env://AUTHORIZATION"
}
```

Query names are case-sensitive; header names are case-insensitive. A stronger
configuration layer replaces a matching entry while retaining its position,
and appends new entries. Existing endpoint query parameters remain supported;
structural query entries replace matching keys. `Authorization` is the
complete header value, such as `Bearer ...`; transport-owned headers are
reserved. Templates remain deferred. Only the selected server's secret
references are resolved. In daemon mode, resolved startup credentials also
distinguish retained instances.

## OAuth

Protected Streamable HTTP servers use the official SDK's OAuth implementation
when no static `Authorization` header is configured. Dynamic client
registration is automatic. A preregistered client can be supplied when a
provider requires one:

```kdl
server "remote" {
    scope "workspace"
    http "https://example.test/mcp" {
        oauth {
            client-id "wirecmd-client"
            client-secret (secret)"env://OAUTH_CLIENT_SECRET"
            redirect-uri "http://127.0.0.1:8765/callback"
        }
    }
}
```

The client ID and exact loopback redirect URI are required in a preregistered
block; the client secret is optional. A configured `Authorization` header
disables OAuth and cannot be combined with an `oauth` block.

Normal operation remains daemon-backed. Manage local credentials with:

```sh
wirecmd auth login SERVER
wirecmd auth status SERVER
wirecmd auth logout SERVER
```

`--direct` performs the same operation without the daemon. `auth login`
requires a local terminal; ordinary protected calls may open the browser when
both stdin and stderr are TTYs. Set `WIRECMD_NONINTERACTIVE=1` to force an
actionable structured authentication error instead. Authorization URLs and
browser diagnostics go to stderr; stdout remains one JSON envelope.

Wirecmd stores a random encryption master key in the native keyring through
`go-keyring` and stores encrypted OAuth state in its private XDG state
directory. There is no plaintext fallback. The SDK owns OAuth protocol
behavior; Wirecmd owns persistence, daemon coordination, redaction, and error
mapping. The current SDK does not expose a separate stable resource/issuer
identity for storage, so the credential record identity uses the resolved
endpoint plus registration inputs; any collision or provider-specific quirk
must be qualified before changing that boundary.

On Linux, the daemon requires an absolute same-user `XDG_RUNTIME_DIR`.
OAuth-backed servers additionally require an available Secret Service, and
interactive browser authorization uses `xdg-open`. See the
[release-readiness record](docs/release-readiness.md) for installation,
qualification, and runtime details.

## Project documents

- [Product thesis](docs/product-thesis.md) defines the authoritative product
  direction and boundaries.
- [Validation plan](docs/validation-plan.md) records the completed first
  falsifiable implementation milestone.
- [Compatibility plan](docs/compatibility-plan.md) defines the current
  supported newest-to-oldest protocol qualification and the deferred SSE
  boundary.
- [Discovery plan](docs/discovery-plan.md) records the completed authoritative
  automatic configuration-discovery and workspace-trust milestone.
- [HTTP values plan](docs/http-values-plan.md) defines the authoritative
  typed query/header configuration milestone.
- [OAuth plan](docs/oauth-plan.md) defines the authoritative transparent OAuth
  and encrypted credential-persistence milestone.
- [Output contract](docs/output-plan.md) defines contextual terminal output,
  explicit machine output, and color policy.
- [Release readiness](docs/release-readiness.md) defines the authoritative
  first-alpha installation, qualification, and manual publication contract.
- [macOS plan](docs/macos-plan.md) defines the authoritative Apple Silicon
  qualification milestone and Intel CI boundary.
- [Exploratory design notes](docs/notes/exploratory-design.md) retain ideas and
  research that are useful but not committed requirements.

Repository working instructions are in [AGENTS.md](AGENTS.md).
