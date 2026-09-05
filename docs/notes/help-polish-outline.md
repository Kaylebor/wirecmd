# Administrative Help Polish

Status: non-authoritative implementation and validation note. Dynamic
MCP-derived command completion and its possible persisted command cache are
explicitly deferred.

## Goal

Make the small administrative surface easier to understand without changing
command behavior, configuration resolution, daemon IPC, MCP discovery, or
output envelopes.

## Proposed scope

1. Give each administrative leaf a focused help page:
   `daemon run|status|reload`, `config trust|untrust`,
   `config trust status|list`, and `auth login|status|logout`.
2. Keep every administrative help path offline and side-effect-free. Auth help
   may accept configuration-selection flags syntactically but must not load
   configuration, touch the keyring, contact a provider, or connect to the
   daemon.
3. Give known misplaced Wirecmd flags contextual ownership errors within
   otherwise-recognized administrative forms. In particular, explain that
   callers select configuration, `daemon run` does not; presentation flags
   belong before administrative names; and call-input flags belong only to tool
   calls. Reject known flags where `config trust` or `config untrust` would
   otherwise interpret them as paths. Do not add a second general parser or
   reinterpret arbitrary suffix flags, tool arguments, or path names outside
   the existing documented administrative-name collision contract.
4. Preserve the explicit `wirecmd --help -- SERVER [TOOL]` escape for configured
   servers named `daemon`, `config`, or `auth`.
5. Keep successful help as plain text and failures in the existing structured
   error contract. Do not add a documentation framework or generated command
   tree.

## Validation outline

- Exercise every group and leaf help form without a daemon, configuration, or
  credential store.
- Verify each recognized misplaced-flag combination names the rejected flag,
  the administrative command it was attached to, and the correct recovery
  action. Include `config trust --direct` and `config untrust --direct` as
  state-mutation regressions.
- Verify prefix-only ownership and configured-server escape behavior remain
  unchanged, including leaf-name collisions such as configured tools named
  `status`, `trust`, or `login`.
- Confirm help performs no filesystem state writes, keyring access, process
  startup, network access, or daemon connection.
- Run the normal test, race, vet, module, build, diff, and independent-review
  gates before merging.

## Deferred

- MCP-derived command-model changes or richer tool-schema help.
- Persisted command metadata and dynamic tool/projected-argument completion.
- Bash and Zsh completion.
- Automatic completion installation or background refresh.

## Validation evidence

The implemented slice was exercised through every administrative group and leaf
help form with a disposable binary and no running daemon. Independent review
checked parser ordering, trust-path mutation, reserved server/tool collisions,
JSON and stdin escapes, and tool-suffix ownership. Full tests and race tests,
vet, module verification, builds, Fish completion integration, and diff checks
passed. Socket-using suites were run outside the filesystem sandbox so their
local listeners could bind normally.

The deliberately ambiguous form `config trust --direct PATH` remains an
ordinary `config` server/tool invocation: it is not a valid administrative
arity, and suffix arguments belong to tools. The exact reserved administrative
form `config trust --direct` is rejected before it can treat the known flag as
a path. Configured servers colliding with administration retain the documented
JSON, stdin, exact-call, and explicit-help escapes.
