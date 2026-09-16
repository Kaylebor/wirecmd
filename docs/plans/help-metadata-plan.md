# Conventional Help

Status: authoritative completed slice. The persistent MCP metadata-cache
portion is withdrawn and superseded by live MCP help; conventional help
placement remains current.

## Contract

A final `--help` or `-h` selects help for a configured MCP server or a
recognized native or administrative command. Native and administrative suffix
help is offline and side-effect-free. `mcp SERVER --help` follows the same
configuration, trust, daemon, and `--direct` behavior as `--help mcp SERVER`.

At the tool boundary, a final `--help` or `-h` is resolved against the live
input schema. An explicitly projectable `help` property owns the argument;
otherwise Wirecmd renders focused tool help. Generic additional properties do
not claim the flag. MCP server and tool help always obtains live metadata: it
requires a running daemon, or explicit `--direct` execution for a one-shot
request. An offline daemon fails normally for both prefix and suffix MCP help.
Exact JSON remains the lossless escape for a literal `help` property. A server
alias literally named `--help` or `-h` uses the namespace-local
`mcp -- --help` or `mcp -- -h` escape, including the corresponding prefix help
form for focused help. The distinct prefix form `--help mcp --` targets a
literal `--` alias, not the `--help` escape. The explicit `mcp` namespace
selects MCP help when a server name collides with administration or native LSP.

## Withdrawn persistent metadata cache

Persistent MCP metadata is no longer stored or read. It had no current consumer
once offline MCP help was removed, and it must not be reintroduced as an
offline-help or completion path without a new accepted contract. Native and
administrative help remain offline and side-effect-free.

Alpha builds that implemented the withdrawn cache may have left an inert
`toolcache` directory below the Wirecmd XDG state directory. Current builds do
not read or update it. Removing legacy state is an explicit user or future
migration action; Wirecmd does not silently delete filesystem state.

## Historical qualification evidence

The former cache implementation was locally qualified for refresh, exact
offline fallback, replacement and targeted merging, semantic-identity
isolation, private-state defenses, concurrent writes, and redaction. Those
claims describe the withdrawn implementation at the time; they are not current
requirements or evidence for the live-help contract. The retained conventional
help behavior remains qualified by suffix ownership, live-schema fallback and
collisions, offline administrative behavior, and unchanged call/projection
behavior. CI qualification remains required on a release commit before
publication.
