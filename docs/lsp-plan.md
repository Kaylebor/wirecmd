# Native LSP Definition

Status: completed authoritative milestone; qualified 2026-09-05

## Objective

Expose one workspace-scoped, configured Language Server Protocol (LSP) process
through Wirecmd's existing shell and daemon contract. This first native,
non-MCP capability is deliberately limited to definition lookup. It validates
that Wirecmd can provide a stable semantic command while leaving language,
server, executable, arguments, environment, and workspace configuration under
the user's control.

Wirecmd has no language-server catalog, executable discovery, presets, or
generated launch commands. `gopls` is a qualification fixture only, never a
production default or special case.

## Public contract

```text
wirecmd [client flags] lsp definition --file PATH --line N --column N
wirecmd --help lsp
wirecmd --help lsp definition
```

`--file` is required and resolves from the caller's current directory. `--line`
and `--column` are required one-based positions. Wirecmd converts them to the
LSP's zero-based UTF-16 positions and rejects unreadable, non-UTF-8, or
out-of-range files and positions as invocation errors.

The bare `wirecmd lsp` form and its help forms are static, offline native help;
they do not load configuration or start a server. Normal definition calls use
the daemon and retain an initialized LSP process. `--direct` deliberately
starts and closes a one-shot process. As with MCP calls, normal operation never
falls back to direct mode.

The `lsp` namespace reserves only those bare native forms. A configured MCP
server named `lsp` remains reachable through `--json`, `--stdin`, an exact-call
object, and `wirecmd --help -- lsp [TOOL]`.

Successful definition output keeps Wirecmd's ordinary semantic envelope:

```json
{
  "ok": true,
  "lsp": {
    "operation": "definition",
    "file": "/absolute/input.go",
    "locations": [
      {
        "path": "/absolute/target.go",
        "range": {
          "start": {"line": 47, "column": 6},
          "end": {"line": 47, "column": 9}
        }
      }
    ]
  }
}
```

Location ranges are one-based. A server's `null`, `Location`, location-array,
and location-link results normalize to an ordered `locations` array; links use
their target selection range. Non-file result URIs are unsupported upstream
results. Presentation follows the [output contract](output-plan.md): compact
JSON for pipes, contextual terminal output by default, and guaranteed machine
output with `--format json --color never`.

## Configuration and execution

An LSP definition is a sibling of MCP `server` definitions in the existing KDL
document:

```kdl
wirecmd {
    root "."

    lsp "primary" {
        scope "workspace"
        language-id "your-language-id"
        stdio "/absolute/path/to/your-language-server" {
            arg "--server-specific-option"
            env EXAMPLE_SETTING="value"
        }
    }
}
```

The executable, its arguments, and any environment entries are examples only;
they must be the actual command supported by the selected server. Literal and
`(secret)"env://NAME"` stdio values use the existing value and redaction rules.
There is no `initializationOptions` syntax in this milestone.

Sources compose through the existing weakest-to-strongest configuration engine.
Named LSP definitions and their environment entries merge by name; stronger
scalars win, a non-empty stronger argv replaces inherited argv, and winning
values retain provenance. A complete effective LSP definition requires
workspace scope, `language-id`, and a non-empty stdio executable.

Definition lookup requires exactly one effective LSP definition for the
workspace. No definition returns `lsp_not_configured`; multiple definitions
return `lsp_ambiguous_configuration`. Both are configuration errors (exit 3).
The definition's name is configuration and retained-instance identity, not a
routine CLI argument. Explicit repeated `--config` paths replace automatic
discovery exactly as they do for MCP configuration; trusted global-to-local
discovery and workspace-root resolution therefore apply unchanged.

Wirecmd starts the configured process, sends conservative standard LSP
initialization, and sends `initialized` after success. It advertises only
implemented client capabilities, including UTF-16 positions, and rejects a
server that selects another position encoding. It checks definition capability
before issuing `textDocument/definition`.

The requested on-disk document is opened lazily. A retained session remembers
opened files and uses monotonically increasing versions when their disk content
changes: full-sync servers receive whole-content changes and incremental-sync
servers receive a whole-document replacement. `didClose`, `shutdown`, and
`exit` run during direct completion, retirement, reload, and daemon shutdown.
Unsaved editor buffers are outside this contract.

Daemon instances are keyed by the named definition, resolved workspace root,
selected execution configuration, daemon generation, and resolved sensitive
startup identity. Operations sharing an instance are serialized; distinct
workspaces retain distinct processes. A broken session remains unavailable
until reload or restart and is never silently retried or replayed. LSP stderr
uses the existing secret-redaction path.

## Error and qualification criteria

Malformed command input, invalid files, and invalid positions are invocation
errors (exit 2). Missing or ambiguous LSP configuration is a configuration
error (exit 3). Startup and retained-session failures are transport errors
(exit 7). Initialization failures, unsupported encoding or result shape,
missing advertised capability, and failed protocol requests are upstream
protocol errors (exit 6). A server that advertises definition support but
responds Method Not Found returns `lsp_capability_mismatch` (exit 6).

The implementation acceptance suite covers configuration composition and
provenance, native grammar and namespace escapes, one-based UTF-16 conversion,
all supported definition result shapes, disk synchronization, capability and
encoding rejection, direct/daemon equivalence, retained-session reuse, reload,
and broken-session handling. Full repository checks remain required for the
change.

Real-server qualification used a disposable per-workspace configuration and
the installed `gopls` to resolve `cli.Run` from this repository's `main.go` in
both direct and retained-daemon modes. The exact configuration, commands,
observed output, retained-instance status, and tool versions are recorded in
the non-authoritative [validation evidence](notes/lsp-definition-validation.md).
The standard test, race, vet, module-verification, build, and diff checks passed.

## Deferred work

- Hover, references, diagnostics, rename, symbols, code actions, and other
  semantic operations.
- Multiple LSP definitions and file-to-server routing.
- `initializationOptions`, arbitrary KDL structured values, templates, and
  language-specific settings.
- Language/server catalogs, executable discovery, presets, installation, and
  generated command lines.
- Unsaved buffers, file watching, dynamic registration, workspace
  configuration, progress UI, server-applied edits, TCP transports, and raw
  JSON-RPC access.
- Persistent completion caches and dynamic shell completion for LSP operations.
