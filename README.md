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
`github.com/Kaylebor/wirecmd`. Once releases exist, the command is intended to
be installable with:

```sh
go install github.com/Kaylebor/wirecmd@latest
```

The module path deliberately does not depend on a vanity domain. A project
website such as `wirecmd.dev` may be added independently later.

The first agent-facing validation milestone is complete: comparative evidence
supports continuing the project. Supported legacy stdio and Streamable HTTP
protocol layers are qualified; legacy HTTP+SSE remains deferred at the SDK
boundary. The current milestone adds deterministic automatic configuration
discovery and workspace trust. Normal commands use a private foreground local
daemon; `--direct` is the deliberate one-shot path for testing and diagnosis.

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
ordinary shell tools. Calls and failures remain newline-terminated JSON:

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

The initial HTTP slice accepts absolute `http` or `https` endpoints only.
Typed query/header entries, credential injection, and OAuth are deliberately
not implemented yet.

## Project documents

- [Product thesis](docs/product-thesis.md) defines the authoritative product
  direction and boundaries.
- [Validation plan](docs/validation-plan.md) records the completed first
  falsifiable implementation milestone.
- [Compatibility plan](docs/compatibility-plan.md) defines the current
  supported newest-to-oldest protocol qualification and the deferred SSE
  boundary.
- [Discovery plan](docs/discovery-plan.md) defines the authoritative current
  automatic configuration-discovery and workspace-trust milestone.
- [Exploratory design notes](docs/notes/exploratory-design.md) retain ideas and
  research that are useful but not committed requirements.

Repository working instructions are in [AGENTS.md](AGENTS.md).
