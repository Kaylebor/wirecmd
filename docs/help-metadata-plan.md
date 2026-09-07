# Conventional Help and Persistent MCP Metadata

Status: authoritative completed slice; implementation and local qualification.

## Contract

A final `--help` or `-h` selects help for a configured MCP server or a
recognized native or administrative command. Native and administrative suffix
help is offline and side-effect-free. `SERVER --help` follows the same
configuration, trust, daemon, and `--direct` behavior as `--help SERVER`.

At the tool boundary, a final `--help` or `-h` is resolved against the live
input schema. An explicitly projectable `help` property owns the argument;
otherwise Wirecmd renders focused tool help. Generic additional properties do
not claim the flag. This fallback never uses cached schemas, so an offline
daemon still fails normally; prefix `--help SERVER TOOL` remains the form that
can use cached metadata. Exact JSON remains the lossless escape for a literal
`help` property. Prefix `--help -- SERVER [TOOL]` continues to select MCP help
when a server name collides with administration or native LSP.

## Metadata cache

Successful MCP tool discovery refreshes private persistent metadata without an
extra upstream request. The cache stores only normalized, redacted names,
descriptions, titles, and canonical schemas. It is keyed by the effective
semantic configuration, selected execution identity, workspace, and server
alias; resolved credentials and protocol objects are never stored.

Live focused help is authoritative. Normal daemon-backed focused help consults
an exact cache entry only when the daemon is unavailable. Server help requires
a complete catalog, tool help requires detailed metadata for that tool, and no
stale-data banner is added. Authentication, configuration, protocol, upstream,
incompatible-daemon, and broken-session failures are never hidden by cached
help. Cached data never validates projected arguments, decides trailing-help
ownership, or participates in a call.

Metadata persists across reload and daemon restart and is replaced lazily by
the next successful discovery. Dynamic tool completion, expiry, pruning,
explicit cache administration, eager refresh, and LSP metadata remain
deferred.

## Storage and safety

The internal versioned JSON store lives below the Wirecmd XDG state directory.
Directories are `0700`; files and locks are `0600`; ownership, symlinks,
locking, atomic replacement, and opaque filenames are checked. Unsafe,
malformed, missing, or incomplete data is never rendered. Cache failures do not
turn a successful live operation into a failure.

## Qualification

Tests cover suffix ownership, live-schema fallback and collisions, offline
administrative behavior, direct and daemon refresh, exact offline fallback,
replacement and targeted merging, semantic identity isolation, private-state
defenses, concurrent writes, redaction, and unchanged call/projection behavior.
Local qualification includes the full and race-enabled suites, vet, module
verification, builds, Fish integration, an official SDK memory-fixture smoke
test, and independent parser/security review. CI qualification remains required
on the release commit before publication.
