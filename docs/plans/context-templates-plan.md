# Context Templates and LSP Initialization Options

Status: implemented in two reviewed changes

## Purpose

Wirecmd exposes a small, explicit invocation context to provider configuration.
This lets a provider defined once consume the current call's paths without
Wirecmd inferring server-specific behavior. A second change uses the same
mechanism for arbitrary LSP `initializationOptions` JSON.

This plan does not add a general template language, secret interpolation,
workspace settings, diagnostics, server catalogs, or server-specific defaults.

## Context templates

Exactly three references exist:

- `${wirecmd.cwd}` is the canonical caller CWD;
- `${wirecmd.project-root}` is the resolved canonical project root; and
- `${wirecmd.global-root}` is Wirecmd's resolved global configuration path,
  whether or not the directory exists.

Expansion is enabled only by the KDL `(template)` annotation. `$$` emits a
literal dollar sign. Expansion is single-pass and non-recursive. A template
must contain at least one reference; malformed or unknown references are
rejected. `(template)` and `(secret)` are mutually exclusive.

Templates are accepted only in provider-consumed strings: MCP and LSP stdio
executables, arguments, and environment values; HTTP and SSE endpoints, query
values, and header values; and OAuth client IDs, client secrets, and redirect
URIs. They are not accepted in names, scope, root selection, `git-root`, LSP
selectors, implementation metadata, or age identity paths.

Selected definitions are materialized once per invocation before
destination-specific validation, authentication identity, execution
fingerprints, or pool keys. Static configuration identity retains the template
source. Runtime and retained identity use the materialized values, so global
providers reuse across projects only when their effective startup
configuration agrees. Direct mode materializes locally. In daemon mode the
daemon independently materializes cached configuration from the caller context
already carried through private IPC; expanded values are not sent over IPC.

`lsp status` reports a resolved executable without starting a provider. Static
MCP listing, generic command help, and completion do not resolve invocation
context; focused MCP server/tool help retains its established live behavior. A
missing referenced value returns `context_value_unavailable`; invalid expanded
destinations retain their existing validation errors. The private daemon
protocol for this phase is 14.

## LSP initialization options

An LSP definition may add one optional child:

```kdl
initialization-options #"""
{
  "feature": true,
  "paths": ["/one", "/two"]
}
"""#
```

It accepts exactly one KDL string containing one strict JSON value. Every JSON
root type, including explicit `null`, is valid. Omission inherits a weaker
value and omits the protocol member; a stronger declaration replaces the
complete document. There is no deep merge.

Untemplated validated JSON is forwarded byte-for-byte. `(template)` expands
references only inside JSON string values through token-level transformation;
object names, numbers, and structure remain unchanged. Secret references are
not supported. The document is never printed or included verbatim in errors,
status, logs, diagnostics, fingerprints, or IPC, while its materialized value
does participate in execution and retained identity. The private daemon
protocol for this phase is 15.

Strict JSON validation rejects invalid UTF-8, duplicate object names, trailing
values, and malformed syntax. The already pinned
`github.com/go-json-experiment/json/jsontext` module becomes a direct
dependency without a version change.

## Qualification

Template tests cover parsing, escaping, invalid references, non-recursive
expansion, composition, provenance, every provider input, direct and daemon
materialization, destination validation, status, and context-dependent reuse.
Static commands must not start providers, discover secrets, or materialize
unneeded definitions.

Initialization-option tests cover every JSON root type, omission versus null,
inheritance and replacement, duplicate names, invalid UTF-8, trailing JSON,
raw-byte preservation, large numbers, unusual path characters, string-only
substitution, identity, and direct/daemon initialization parity. The normal
module, race, vet, build, completion, legacy, and Linux/macOS CI gates apply to
both reviewed changes.
