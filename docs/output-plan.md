# Contextual CLI output and color

Status: implemented and locally qualified; see the
[validation evidence](notes/output-validation.md). Physical macOS terminal
qualification remains separate from the successful cross-build checks.
This document supersedes earlier milestones' blanket always-JSON presentation
wording, not their semantic envelopes, IPC, exits, or OAuth interaction rules.

## Public contract

Prefix-only `--format auto|json|pretty` and `--color auto|always|never` default
to `auto`. `--colour` aliases `--color`; repeated settings use the last value.
They apply to ordinary and administrative commands, but flags after a tool name
remain tool arguments. `--json` continues to supply argument input. Successful
help is plain text regardless of format; `--version` remains standalone.

Automatic format selects pretty for stdout TTYs and compact JSON otherwise.
Explicit JSON preserves existing envelopes; pretty renders discovery,
administration, and errors as readable text and tool success envelopes as
two-space-indented JSON. Use `--format json --color never` for guaranteed
machine output, including in a PTY.

Automatic color requires stdout TTY, `NO_COLOR` unset or empty, and `TERM`
not equal to `dumb`. Explicit always/never overrides environment and
terminal detection. Color is independent of format and may style labels and
encoded JSON syntax. Upstream terminal controls never become executable ANSI.

## Rendering boundaries

- Servers: name, scope, transport columns in configured order.
- Tools: name, optional title, full description in existing tool order.
- Resources and resource templates: contextual discovery tables with their
  complete normalized identifiers and metadata. Resource reads keep their
  existing generic, two-space-indented JSON envelope rather than interpreting
  text or binary contents.
- Trust: explicit confirmation/status and workspace/root; list one root per line.
- Auth: server, status, registration and expiry when present.
- Daemon: labeled readiness/status and reload counts.
- Errors: category, code, message, recovery action, and indented attached result.
- Tool success: the entire existing envelope, including data, ordered messages,
  nulls and empty collections, without interpretation of arbitrary payloads.

All final results and failures stay on stdout; existing warnings, OAuth browser
handoffs and upstream diagnostics stay on stderr. No banners, duplicate
responses, truncation, Markdown rendering, deduplication, or decoding of
JSON-looking message strings is introduced. Dynamic pretty fields and focused
help escape terminal controls, retaining ordinary Unicode and useful line
breaks/tabs in descriptions; single-line identifiers remain unambiguous.

Presentation lives only in the CLI, shared by direct and daemon results and
foreground readiness/startup failure. Stdlib JSON encoding/indentation and a
small encoded-JSON highlighter require no dependency or jq subprocess. The
existing encoding-failure fallback stays compact and uncolored. Stdout TTY
detection is independent of OAuth's stdin/stderr interaction checks.

## Acceptance checks

Cover every output family, empty/optional/multiline fields, attached results,
TTY/environment/explicit format-color matrix, aliases and prefix ownership.
Verify semantic JSON, numbers, redaction, ordering and exits remain unchanged,
direct/daemon equivalence, control escaping, and disabled-color ANSI absence.
Run full tests, race tests, vet, module verification, builds, diff checks,
independent review and actual PTY/non-PTY CLI smoke tests.

Configuration settings, IPC revisions, dependencies, commits and releases are
not part of this slice.
