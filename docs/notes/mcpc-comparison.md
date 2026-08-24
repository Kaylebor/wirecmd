# Apify mcpc comparison notes

Status: non-authoritative exploratory evidence. This is a product comparison,
not a parity roadmap or an accepted requirement.

This note records a temporary runtime and documentation comparison with Apify
[`mcpc` v0.6.0](https://github.com/apify/mcpc/tree/v0.6.0), including its
[v0.6.0 release](https://github.com/apify/mcpc/releases/tag/v0.6.0). The
upstream [usage reference](https://github.com/apify/mcpc/blob/v0.6.0/README.md)
is the primary source for its documented interface.

## Useful lessons

- A shell-facing MCP client with progressive discovery is a viable interaction
  model. `mcpc` also relies on an MCP SDK, reinforcing Wirecmd's decision to
  keep protocol behavior in the official Go SDK rather than reproduce it.
- `mcpc` makes persistent connections an explicit public object: a user creates
  a named `@session`, then runs MCP operations through that name. Wirecmd's
  workspace/configuration-derived daemon identity is intentionally different:
  connection lifecycle should normally remain transparent to callers.
- The comparison reproduced a concrete projected-input failure: its
  `create_entities` help/example suggested a scalar-like value for an array
  property. The projected syntax itself succeeds when given the *bare array*
  (`entities:='[{...}]'`), rather than an object wrapping the property. This
  matches the Wirecmd trial where two agents independently wrapped the value
  passed to `--entities`.
- Raw MCP-shaped JSON errors can identify an upstream validation problem, but
  Wirecmd's stable result/error envelopes and recovery categories are a more
  useful agent-facing contract. Keep that boundary independent of SDK result
  types and MCP revisions.
- Automatic reconnect/restart is not a safe default for a generic call: a
  transport loss can leave a caller unable to know whether an upstream action
  executed. Wirecmd's current honest broken-instance outcome avoids implicit
  replay.

## Candidate ideas, only if later evidence warrants them

- Compact tool search, provided it preserves lazy discovery and has a defined
  answer to whether searching may start configured servers.
- Explicit OAuth administration, once a current supported transport requires
  it and the official Go SDK path has been qualified.
- An optional MCP-server/proxy output adapter, only after the agent-facing CLI
  milestone; it must not reshape the primary shell contract.
- Importing selected existing MCP configurations on demonstrated demand. Any
  such work must be deliberately reconciled with the KDL configuration
  decision, not introduced as an implicit alternative format.

## Explicitly not adopted from this comparison

- Public named-session lifecycle commands.
- Eager bulk connection or discovery as normal operation.
- A competitor-parity roadmap, including broad MCP primitive support merely
  because `mcpc` exposes it.
- MCP-shaped public success or error output.
- Automatic reconnect, retry, or call replay.
- Broad editor/agent configuration import.

The immediate actionable finding is narrower: focused projected-argument help
should show a copyable value-shaped example for arrays and objects, and say
that a projected flag receives the property value, not an enclosing JSON
object. Validate that change with the existing controlled agent scenario before
promoting any broader feature decision.
