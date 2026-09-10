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

## Install

The canonical Go module is `github.com/Kaylebor/wirecmd`. Install the latest
stable release with Go 1.26 or newer:

```sh
go install github.com/Kaylebor/wirecmd@latest
wirecmd --version
```

Use `github.com/Kaylebor/wirecmd@v0.2.0` when an exact version is required.
Tagged module installations report their version through `wirecmd --version`;
source-checkout builds report `wirecmd dev`.

The module path deliberately does not depend on a vanity domain. A project
website such as `wirecmd.dev` may be added independently later.

The first agent-facing validation milestone is complete: comparative evidence
supports continuing the project. Supported legacy stdio and Streamable HTTP
protocol layers are qualified; legacy HTTP+SSE remains deferred at the SDK
boundary. Automatic configuration discovery, workspace trust, typed HTTP query
and header values, and SDK-owned OAuth with encrypted credential persistence
are also complete. The first native LSP adapter adds automatically routed,
multi-provider source navigation and read-only inspection. Normal commands use
a private foreground local daemon; `--direct` is the deliberate one-shot path
for testing and diagnosis.

Linux and macOS are supported. Native macOS CI covers Apple Silicon and Intel;
the maintainer has smoke-tested the Apple Silicon build on an M2,
including daemon-backed Cloudflare OAuth and readable terminal output. This is
not a claim of exhaustive physical-device qualification. The checklist remains in the
[macOS plan](docs/macos-plan.md).

## First call

Save this complete KDL 2 document as `wirecmd.kdl`, replacing the command with
an installed stdio MCP server executable and its actual arguments:

```kdl
wirecmd {
    mcp "local" {
        scope "workspace"
        stdio "/absolute/path/to/mcp-server" {
            arg "--server-option"
        }
    }
}
```

For a locally buildable dummy server, follow the
[test fixture instructions](testdata/legacy-mcp/README.md), use its absolute
executable path, and omit the `arg` line. That fixture provides `set_value`
and `read_value`, used below.

In one terminal, start the daemon and leave it running:

```sh
wirecmd daemon run
```

Linux requires an absolute, private, same-user `XDG_RUNTIME_DIR`, normally set
by the login session. On macOS, leaving it unset uses the validated per-user
temporary directory. An invalid explicit value is an error on either platform.
Do not pass `--config` to `daemon run`: callers select configuration.

In another terminal, discover and invoke the configured fixture:

```sh
wirecmd --config ./wirecmd.kdl mcp
wirecmd --config ./wirecmd.kdl mcp local
wirecmd --config ./wirecmd.kdl --help mcp local tool set_value
wirecmd --config ./wirecmd.kdl mcp local tool set_value --value hello
wirecmd --config ./wirecmd.kdl mcp local tool read_value
```

For other servers, use their discovered tool names and focused-help arguments.
The daemon retains initialized sessions; separate `--direct` invocations do
not retain process-local state. Nothing silently falls back to direct mode.

Daemon administration and offline help:

```sh
wirecmd daemon run
wirecmd daemon status
wirecmd daemon reload
wirecmd --help daemon
```

Reload after configuration edits; it retires cached configuration and sessions
while allowing active calls to finish. Ctrl-C in the daemon terminal stops it.

## Configuration and trust

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
server named `config` remains callable through `wirecmd mcp config`.

Use `wirecmd --help daemon|config|auth` for an offline overview, or name an
administrative command for focused help, such as `wirecmd --help daemon reload`,
`wirecmd --help config trust status`, or `wirecmd --help auth login`.
Administrative help never executes the command. Known Wirecmd flags placed in
an administrative path position are rejected with ownership guidance rather
than being interpreted as paths. MCP servers whose names collide with
administration are naturally addressed through `wirecmd mcp NAME`; the
tool-side `--` remains the raw JSON overlay.

## Native LSP navigation and inspection

Wirecmd also provides native, workspace-scoped navigation and read-only
inspection through configured Language Server Protocol processes. It does not
supply language servers, infer executables, or maintain a language catalog.
Configure the actual command and one or more selectors alongside MCP sources:

```kdl
wirecmd {
    root "."

    lsp "primary" {
        implementation-id "optional-stable-metadata"
        selector language-id="your-language-id"
        stdio "your-language-server" {
            arg "--server-specific-option"
        }
    }
}
```

LSP scope defaults to `workspace`; the executable remains user-supplied and
argv/environment are optional. A selector requires `language-id`; its optional
`pattern` defaults to `**/*` and is matched relative to the workspace root.
Multiple definitions may match one file and are queried concurrently. Partial
source layers compose using the same named-definition, selector replacement,
argv-replacement, environment-override, provenance, discovery, and trust rules
as MCP sources.

Start the daemon as usual, then use static help or query a disk-backed file:

```sh
wirecmd --help lsp
wirecmd --help lsp definition
wirecmd lsp definition --file ./main.go --line 21 --column 13
wirecmd lsp references --file ./main.go --line 21 --column 13
wirecmd lsp hover --file ./main.go --line 21 --column 13
wirecmd lsp signature-help --file ./main.go --line 21 --column 13
wirecmd lsp document-symbols --file ./main.go
wirecmd lsp workspace-symbols --query 'Wirecmd'
wirecmd lsp status --file ./main.go
```

Available navigation operations are `definition`, `declaration`,
`type-definition`, `implementation`, and `references`; references exclude the
declaration unless `--include-declaration` is supplied. Line and column are
one-based. Results contain provider-attributed, one-based file locations and
structured provider outcomes when a matching provider fails. The daemon
retains each configured LSP session; `--direct` starts one-shot sessions.
`lsp status` reports configured selectors and already-observed runtime identity
without starting a process. Hover preserves ordered plaintext, Markdown, and
code blocks. `signature-help` preserves provider-attributed callable labels,
active states, optional plaintext or Markdown documentation, and parameter
labels resolved from validated UTF-16 offsets. `document-symbols` preserves nested symbols; `workspace-symbols`
passes its explicit query, including an empty query, to every configured
provider without local ranking or truncation. These inspection results have
provider outcomes and partial-failure semantics matching navigation, while
using their own result collections and counts. The bare `lsp` form is native
help, but an MCP server named `lsp` remains available through
`wirecmd mcp lsp`. See the
[LSP plan](docs/lsp-plan.md) for the complete contract and qualification
boundary.

## Invocation and output

Focused help is conventional text, so it can be read directly or filtered with
ordinary shell tools. Output defaults to readable discovery, administration,
and errors on a terminal, with indented JSON for tool results. Pipes retain
compact newline-terminated JSON. Override presentation with prefix flags:

```sh
wirecmd --format pretty --color never daemon status
wirecmd --format json --color never mcp SERVER tool TOOL
```

Configured servers also accept `wirecmd mcp SERVER --help`. MCP server and focused
tool help obtains live metadata, so it requires a running daemon or explicit
`--direct` execution; it does not fall back while the daemon is offline.
For aliases literally named `--help` or `-h`, use `wirecmd mcp -- --help` or
`wirecmd mcp -- -h`; focused help uses the corresponding `--help mcp -- ...`
form. The namespace-local escape does not affect the raw overlay after a tool
name. Separately, `wirecmd --help mcp --` inspects a configured alias literally
named `--`; it is not shorthand for the `--help` escape.
Everything after an MCP tool name normally belongs to that tool. A final
`--help` or `-h` uses the live schema: an explicitly projected `help` property
remains tool input; otherwise Wirecmd renders focused tool help.

`--format auto|json|pretty` and `--color auto|always|never` default to `auto`.
`--colour` is an exact alias; the last supplied setting wins. Automatic color
requires stdout to be a TTY, non-`dumb` `TERM`, and no non-empty `NO_COLOR`.
Explicit color settings override detection, independently of format. Agents
using a PTY should select `--format json --color never` for machine output.
Output detection does not change OAuth's separate stdin/stderr TTY checks.
Successful help stays plain text and `--version` remains standalone.

Client presentation flags must precede `mcp`/server/tool names; flags after the tool
name belong to the tool. `--json` still supplies tool input, not output format.
See the [output contract](docs/output-plan.md) for details.

```sh
# Discover a server's tools, then inspect the one needed.
wirecmd --config ./wirecmd.kdl --help mcp memory
wirecmd --config ./wirecmd.kdl --help mcp memory tool create_entities

# Use generated top-level flags where the schema is unambiguous.
wirecmd --config ./wirecmd.kdl mcp memory tool create_entities --entities '[...]'

# Merge collision-prone or otherwise raw properties structurally.
wirecmd --config ./wirecmd.kdl mcp MCP_SERVER tool TOOL --simple value -- '{"tool_name":"one","toolName":"two"}'
```

`--json`, `--stdin`, and the exact-call object remain the lossless fallback for
every tool input. Wirecmd does not locally validate the full JSON Schema; the
upstream tool remains responsible for semantic validation.

MCP resources use their own explicit operations. They retain the upstream
server's ordered read contents, while listings are sorted deterministically:

```sh
wirecmd mcp SERVER resources
wirecmd mcp SERVER resource-templates
wirecmd mcp SERVER resource URI
```

Resource reads return text directly and binary data as base64 with its URI and
MIME type. Resource pagination, transport, authorization, and session
lifecycle remain owned by the official MCP SDK.

Servers may use either a local stdio command or a modern Streamable HTTP
endpoint. They are mutually exclusive in an effective server definition:

```kdl
wirecmd {
    mcp "remote" {
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
mcp "remote" {
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
browser diagnostics go to stderr; stdout contains one final result, rendered
according to the selected output format.

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

On macOS, OAuth uses Keychain and `/usr/bin/open`. On either platform,
`credential_store_unavailable` requires making the native keyring available;
there is no plaintext fallback. `authorization_required` directs a
non-interactive caller to perform login from a local interactive terminal.

## Recovering from common errors

- `daemon_unavailable`: start `wirecmd daemon run`; use `--direct` only for a
  deliberate one-shot call.
- `config_not_found`: supply `--config PATH`, or create the global/workspace file.
- `workspace_untrusted`: review the configuration, then run the exact trust
  command from the error's action field.
- `config_mismatch`: reload the daemon after configuration edits.
- `lsp_not_configured` or `lsp_no_matching_provider`: configure a workspace
  `lsp` definition and a matching selector. `lsp_capability_unavailable` means
  no routed provider advertises the requested navigation or inspection
  operation.
- `lsp_selector_ambiguous`: make matching selectors choose one language ID per
  provider; `lsp_encoding_unsupported` requires UTF-16 support.
- Argument errors: read `wirecmd --help mcp SERVER tool TOOL`; pass complex flag values
  as the value itself, not an object wrapping its property name.

Errors preserve category, code, message, action, and a nonzero exit status.
Use `--format json --color never` for machine-readable results in any terminal.

## Fish completion

From a checkout of this version, install completion manually:

```fish
mkdir -p ~/.config/fish/completions
cp completions/wirecmd.fish ~/.config/fish/completions/wirecmd.fish
```

For a custom Fish configuration directory, use its `completions` directory
instead. Start a new Fish session after installation. `go install` installs
the binary only; it does not install shell completion.

Completion covers client flags, administration, paths, the `mcp` namespace,
and locally configured server names. It respects ordered `--config` flags and workspace trust, but
never contacts the daemon, resolves secrets, starts an MCP, or opens OAuth.
Invalid/untrusted configuration yields no server suggestions; use an ordinary
command to obtain diagnostics. Completion reads disk configuration, which may
differ from daemon-cached configuration until reload. Tool names, projected
flags, and JSON contents are not completed. Bash/Zsh support is deferred.

## Project documents

- [Onboarding and completion](docs/onboarding-plan.md) defines administrative
  help, the MCP namespace, and local-only Fish completion.
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
- [MCP resources plan](docs/resources-plan.md) defines the completed resource,
  resource-template, and read operations and their redaction boundary.
- [LSP plan](docs/lsp-plan.md) defines the completed multi-provider navigation,
  signature-help, hover, and symbol-inspection milestone.
- [Output contract](docs/output-plan.md) defines contextual terminal output,
  explicit machine output, and color policy.
- [Release readiness](docs/release-readiness.md) defines the authoritative
  stable installation, compatibility, qualification, and publication contract.
- [macOS plan](docs/macos-plan.md) defines the authoritative Apple Silicon
  qualification milestone and Intel CI boundary.
- [Exploratory design notes](docs/notes/exploratory-design.md) retain ideas and
  research that are useful but not committed requirements.

Repository working instructions are in [AGENTS.md](AGENTS.md).

## License

Wirecmd is licensed under the [Apache License 2.0](LICENSE).
