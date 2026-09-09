# Native LSP Navigation and Inspection

Status: automatic multi-provider navigation, signature help, hover, and symbol inspection are
implemented and qualified

## Objective

Expose workspace-scoped Language Server Protocol navigation and read-only
inspection through Wirecmd's shell and retained-daemon contract. Wirecmd owns
stable semantic commands, configuration, routing, normalization, and lifecycle.
It does not supply a language-server catalog, infer executable names or file
extensions, generate launch arguments, or contain server-specific behavior.
`gopls` and the Angular-like test fixture are qualification targets only.

## Public contract

```text
wirecmd lsp definition --file PATH --line N --column N
wirecmd lsp declaration --file PATH --line N --column N
wirecmd lsp type-definition --file PATH --line N --column N
wirecmd lsp implementation --file PATH --line N --column N
wirecmd lsp references [--include-declaration] --file PATH --line N --column N
wirecmd lsp hover --file PATH --line N --column N
wirecmd lsp signature-help --file PATH --line N --column N
wirecmd lsp document-symbols --file PATH
wirecmd lsp workspace-symbols --query TEXT
wirecmd lsp status [--file PATH]
wirecmd --help lsp [OPERATION]
```

Files resolve from the caller's current directory. Lines and columns are
one-based at the CLI boundary and are converted to zero-based UTF-16 positions.
Navigation rejects unreadable, non-UTF-8, out-of-range, outside-workspace, and
unmatched files with actionable errors.

The bare `wirecmd lsp` form and its help forms are static and start no process.
Normal navigation uses retained daemon sessions; `--direct` uses one-shot
sessions. Normal `lsp status` requires the daemon but never starts an LSP, while
`--direct lsp status` is configuration-only. An MCP server named `lsp` remains
reachable through `wirecmd mcp lsp` and the usual tool input forms.

Results contain a flat ordered location list. Every location is attributed to
its configured provider and uses a normalized absolute file path and one-based
range. `LocationLink` uses `targetSelectionRange`; null results produce an empty
list; non-file result URIs are unsupported. Provider outcomes are reported in
configuration order as `ok`, `unsupported`, or `failed`. A successful capable
provider, including an empty result, makes the operation successful; other
failures set `partial: true` without producing client stderr warnings. If every
capable provider fails, the first configured failure is top-level and all
outcomes remain attached. If no match advertises the operation, Wirecmd returns
`lsp_capability_unavailable`.

Hover, signature help, and symbol inspection use the same routing, session, and provider
outcome rules. Hover takes one-based file coordinates and returns ordered,
provider-attributed entries. Each entry preserves its optional range and its
ordered content blocks, normalized as `plaintext`, `markdown`, or `code` with an
optional code language; Markdown is not interpreted or concatenated. A null
hover contributes no entry. `document-symbols` returns provider-attributed
symbols, including nested document-symbol children, with numeric and readable
kinds, normalized paths and ranges, optional selection ranges, detail,
container names, and deprecation when reported. Flat server results remain
flat; Wirecmd does not rebuild hierarchy from container names. `workspace-symbols`
passes the required query, including an explicitly empty query, unchanged to
every configured provider and does no local ranking or truncation. Workspace
symbol results are range-bearing file locations; missing ranges and non-file
URIs are provider-level upstream errors. The JSON envelopes for these
operations use their own hover, signature, or symbol collections and counts; existing
navigation `locations` envelopes remain unchanged. File operations include the
absolute input file, while workspace search includes the effective workspace
and query.

Signature help synchronizes the selected document and returns ordered,
provider-attributed callable signatures. Each entry includes a label, active
state, optional plaintext or Markdown documentation, and ordered parameters
with normalized textual labels, active state, and optional documentation. Tuple
parameter labels are resolved using validated UTF-16 offsets against their
signature label. Signature-level active parameters override the top-level
value; explicit null preserves no-active-parameter, while absent and
out-of-range values use the LSP defaults. A null result is a successful empty
signature list.

The four inspection operations advertise the corresponding negotiated
capabilities in `lsp status`. File inspection opens and synchronizes the
requested document as needed; workspace search does not open a document.
Pretty hover output shows the file and position, provider labels, optional
ranges, and complete content blocks (with code-language labels), while symbol
output uses the existing indented JSON presentation.

Presentation follows the [output contract](output-plan.md): compact JSON for
pipes, contextual terminal output by default, and guaranteed machine output
with `--format json --color never`.

## Configuration and routing

LSP definitions are siblings of `mcp` definitions in KDL:

```kdl
wirecmd {
    root "."

    lsp "primary" {
        implementation-id "optional-stable-metadata"
        selector language-id="your-language-id" pattern="src/**"
        selector language-id="your-language-id"

        stdio "/absolute/path/to/language-server" {
            arg "--server-specific-option"
            env EXAMPLE_SETTING="value"
        }
    }
}
```

Scope defaults to `workspace`; root defaults to the caller CWD. The stdio
executable and at least one selector are mandatory. `implementation-id`, argv,
and environment are optional. Each selector requires `language-id`; `pattern`
defaults to `**/*`. Patterns are validated and matched with doublestar against
slash-normalized paths relative to the effective workspace root.

Selectors use OR semantics. A matching definition with different selected
language IDs returns `lsp_selector_ambiguous`. All matching definitions are
eligible providers. Sources compose weakest-to-strongest: named definitions and
environment entries merge by name, stronger scalars win, non-empty selectors
and argv replace their inherited collections, and winning values retain
provenance. The same rules serve explicit repeated `--config` paths and trusted
global-to-local discovery.

The KDL collection for MCP configurations is `mcp "name"`. The former `server`
node is unknown; this alpha change has no compatibility alias. Internal Go
model names and public CLI terminology continue to use server where appropriate.

## Runtime and lifecycle

Each routed operation starts or acquires every matching provider and filters it
using initialization capabilities. Distinct providers run concurrently, while
operations for one retained instance remain serialized. Output is reordered to
configuration order after fan-out. Initialization supplies the workspace root,
one workspace folder, conservative implemented client capabilities, and UTF-16
position support. Server name/version, capabilities, chosen encoding, and sync
behavior are observed from initialization; none participates in launch routing.

Documents open lazily with the selector's language ID. Retained sessions reuse
unchanged content and increment versions when files change. Full-sync providers
receive full content; incremental providers receive one whole-document
replacement; sync-none providers receive no document synchronization. Direct
completion, reload, retirement, and shutdown close documents and perform the
standard LSP shutdown/exit sequence. Broken sessions remain unavailable until
reload or daemon restart and requests are never replayed.

Daemon instances are keyed by definition, resolved workspace root, selected
execution configuration, daemon generation, and sensitive startup identity.
The LSP slice introduced private daemon protocol version 6; the current client
and daemon use version 9 after adding schema-aware trailing help, native LSP
inspection requests, and signature help. Status
reports configured definitions, selectors, optional implementation metadata,
executable, selector matches, and already-observed runtime identity and
capabilities without starting a process.

## Errors and qualification

Invalid command positions and files are invocation errors (exit 2). Incomplete
configuration is exit 3. Process and retained-session failures are transport
errors (exit 7). Initialization, encoding, capability, malformed-result, and
unsupported-URI failures are upstream protocol errors (exit 6). A provider that
advertises an operation but returns Method Not Found reports
`lsp_capability_mismatch`.

Tests cover composition, provenance, selector replacement and ambiguity,
namespace escapes, all five navigation methods, hover, signature help, document symbols,
workspace symbols, result variants, synchronization, capability filtering,
provider fan-out and ordering, partial/all failures, direct/daemon equivalence,
retained state, reload, broken sessions, status, and redaction. Real-server
qualification uses an explicit disposable `gopls` configuration against this
repository; exact evidence for navigation, signature help, hover, and symbols is recorded in
the non-authoritative [validation note](notes/lsp-definition-validation.md).

Earlier validation notes may show the pre-rename KDL collection spelling
`server`; those historical commands remain unchanged and do not describe the
current contract.

## Deferred work

- Diagnostics, rename, code actions, and mutating operations.
- Symbol resolution, indexing/completeness guarantees, and workspace settings.
- Routing policy beyond selector matching and automatic provider fan-out.
- `initializationOptions`, arbitrary KDL structured values, templates, and
  language-specific settings.
- Language/server catalogs, executable discovery, presets, installation, and
  generated command lines.
- Unsaved buffers, file watching, dynamic registration, workspace
  configuration, progress UI, server-applied edits, TCP transports, and raw
  JSON-RPC access.
- Persistent completion caches and dynamic shell completion for LSP operations.
