# Onboarding, Help, and Fish Completion

Status: authoritative accepted slice; implementation and local qualification.
This milestone is independent of the parked SDK/SSE prerelease branch.

## Help and onboarding

README gives a first-call sequence, real release/platform status, complete KDL,
foreground daemon setup, discovery and argument inspection. Global help lists
all public prefix flags and input forms; the agent Skill keeps only operational
guidance. Dedicated administrative help is offline and side-effect-free.

`wirecmd --help daemon|config|auth` describes that administrative group;
recognized subcommands return focused leaf help rather than repeating the whole
group page. Auth help accepts configuration/direct selection without resolving
it. Daemon/config help retains restrictions on those flags. Known Wirecmd flags
in a reserved administrative path position produce contextual ownership errors
instead of being interpreted as paths. This recognition remains narrow so
ordinary tool suffixes keep their existing ownership. MCP operations are under
the explicit `mcp` namespace, so servers named like native groups need no
collision escape. All help rejects tool input modes and remains plain text.

MCP-focused help uses `wirecmd --help mcp SERVER [tool TOOL]`. For a configured
alias literally named `--help` or `-h`, use the unambiguous
`wirecmd --help mcp -- --help [tool TOOL]` or `... mcp -- -h [tool TOOL]` form;
the corresponding ordinary paths are `wirecmd mcp -- --help` and
`wirecmd mcp -- -h`. Later `--` after a tool name belongs only to the raw
argument overlay. Ordinary invocation and standalone version semantics do not
change. The distinct prefix form `wirecmd --help mcp --` targets a configured
alias literally named `--`; it is not the `--help` escape. Invalid admin help
returns an invocation error.

## Completion contract

Fish completion is manually installed. It covers prefix syntax, administrative
verbs, paths, the `mcp` namespace, and local server names, not upstream tools,
projected flags or JSON.
It must honor repeated configuration order, trust, and prefix/positional ownership.

The private `--completion-servers` prefix flag accepts only repeated `--config`
paths and no positionals. It prints literal newline-separated server names in
effective order; names containing terminal controls are omitted. Errors produce
no candidates or diagnostics. This private protocol is an explicit exception to
ordinary result envelopes. The helper uses existing read-only discovery and
composition, never daemon IPC, MCP startup, OAuth, secret resolution or state
writes. Completion skips non-regular config paths (such as FIFOs) rather than
waiting for stream input; ordinary explicit-config loading is unchanged.
That completed onboarding milestone introduced no cache, dependency, or
generic completion framework. The later conventional suffix-help contract is
defined by [the help metadata plan](help-metadata-plan.md).

Disk configuration may differ from daemon-cached configuration until reload.
Dynamic upstream completion and Bash/Zsh support remain deferred. Fish
completion remains server-only, and Wirecmd persists no MCP metadata for
completion.

## Acceptance

Tests cover offline help, reserved names, separators, argument ownership, unchanged
version/invocation, ordered/discovered configurations, unsafe state/names, missing
secrets and no completion side effects. Native Fish `complete -C` tests are a
required Linux CI step. Full tests/race/vet/module/build checks, independent
review, and a recorded rich-fixture consumer trial qualify the implementation.

No automatic installation, commit, push, merge or release is authorized by this
milestone. Runtime and SDK dependencies remain unchanged.
