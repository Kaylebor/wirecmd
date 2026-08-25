# Automatic Configuration Discovery and Workspace Trust

Status: completed authoritative milestone; supported MCP compatibility
qualification is complete; legacy HTTP+SSE remains deferred at the SDK
boundary. The current typed HTTP query/header milestone is recorded in the
[HTTP values plan](http-values-plan.md).

## Objective

Make ordinary Wirecmd invocation discover user and workspace configuration
without prompts, while keeping explicit configuration deterministic and making
execution of workspace-defined commands an explicit same-user trust decision.
The supported protocol qualification is recorded in the
[compatibility plan](compatibility-plan.md).

This document is the completed record for automatic configuration discovery
and workspace trust. It remains the authority for that behavior; subsequent
configuration work is specified separately and must preserve its source
ordering, provenance, and explicit trust boundary.

## Discovery contract

When no `--config` option is present, the CLI builds an ordered source list:

1. `$XDG_CONFIG_HOME/wirecmd/config.kdl` when `XDG_CONFIG_HOME` is absolute;
   otherwise `~/.config/wirecmd/config.kdl`;
2. project `wirecmd.kdl` files from the nearest trusted workspace root down to
   the caller's current directory.

The global source is weakest. Workspace sources are ordered from broadest to
nearest, so later files override earlier files through the existing effective
configuration composition and retain source provenance. A missing global file
is allowed when a trusted workspace source exists. The global user
configuration is inherently trusted and is not part of the workspace registry.

The CLI resolves the caller directory and trust targets canonically. Trust is
recursive: the nearest trusted ancestor is the discovery boundary, and project
files above that boundary are ignored. If project configuration is present but
no trusted root matches the caller, discovery fails closed with the structured
`workspace_untrusted` error and exit code 8; its action is the exact
`wirecmd config trust ...` command needed to approve the directory. A missing
configuration source returns `config_not_found` with exit code 3. Discovered
project files must be regular, non-symlink files.

Any repeated `--config PATH` options replace discovery completely. Their
absolute paths are passed to the existing ordered `LoadEffective` composition,
and trust checks do not apply to explicitly selected files.

## Trust administration

The non-interactive administration commands are:

```text
wirecmd config trust [PATH]
wirecmd config untrust [PATH]
wirecmd config trust status [PATH]
wirecmd config trust list
```

`PATH` defaults to the caller's current directory. These forms reject ordinary
execution flags. A server named `config` remains callable through `--json` or
an exact-call envelope, so the administrative namespace does not remove that
server name from the public contract.

Trust means that future configuration edits within the canonical directory are
accepted; it does not create a content-hash approval or require reapproval for
each edit. Trusted directories are stored as private internal state below
an absolute `$XDG_STATE_HOME/wirecmd`, falling back to
`~/.local/state/wirecmd`. The state
directory and file are same-user only (`0700` and `0600`), reject unsafe
ownership or symlink replacement, and use advisory locking plus atomic writes.

## Daemon and reload behavior

Discovery and trust evaluation happen in the CLI. The daemon receives the
resulting absolute ordered paths and does not independently discover files or
consult the trust registry. Discovered paths participate in the existing
provenance, effective-configuration fingerprint, and cache identity. Wirecmd
does not watch configuration files; after a file or trust change, run
`wirecmd daemon reload` before relying on a daemon-backed call.

## Acceptance criteria

- Global and nested workspace sources compose weakest-to-strongest with stable
  ordering, provenance, config-relative roots, and daemon cache identity.
- Explicit repeated `--config` sources replace discovery and preserve their
  existing order and path behavior.
- Trust, untrust, status, and list manage recursive canonical directories, with
  safe state permissions, locking, atomic replacement, and malformed-state
  failures that do not broaden trust.
- Untrusted workspace configuration fails before loading or executing a server;
  listing or error recovery does not start a configured process.
- Missing-source, non-regular-file, nested-boundary, separate-CWD, daemon
  reload, and direct/daemon equivalence cases have deterministic tests.
- The public `config` server name remains reachable through the lossless JSON
  invocation paths, and administrative commands remain non-interactive.
