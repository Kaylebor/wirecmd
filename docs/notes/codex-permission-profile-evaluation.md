# Codex permission-profile evaluation runner

Status: non-authoritative evaluation infrastructure note, 2026-08-25.

`scripts/eval/codex-profile-exec.sh` is the small containment layer for a
comparative evaluation repetition. It relies on Codex CLI's own custom
permission profile, including its Code Mode path, rather than recreating that
sandbox with a second Bubblewrap launcher.

## Boundary

The runner requires one fresh, trusted arm root. Only its five fixed children
are granted to model-executed tools:

- `workspace/` is the evaluated CWD and writable;
- `tools/`, `inputs/`, and `runtime/` are read-only; and
- `state/` is writable client state.

The profile starts with Codex's `:minimal` runtime grant. It adds read access to
the pinned native Codex runtime directory so Codex can start its sandboxed
runner, then adds the four read-only arm directories and the writable state
directory. It grants workspace write using the special workspace-root rule.
It does **not** grant the arm root itself. Files beside those children,
repository source, other arms, and host home are consequently outside the
profile.

The parent Codex CLI uses its normal host-side login. No authentication file is
copied, mounted, or supplied through the evaluated environment. The runner uses
`--ignore-user-config`, `--ignore-rules`, `--ephemeral`, an explicit model, a
fresh CWD, and a cleared model-shell environment with only condition-specific
paths. Its stdin is closed so no parent-process input is silently appended to
the frozen task. JSONL events go to the fresh arm's `transcript.jsonl`; the
runner refuses to overwrite one.

The runner grants no command networking unless `--allow-network` is explicit.
On Linux, Codex 0.149.1 reports that exact Unix-socket proxy allowlists are
macOS-only. Qualification therefore uses direct command networking equally for
all three conditions so Wirecmd's assigned Unix socket and the loopback HTTP
fixture work. This also permits unrelated command-network destinations; any run
that uses one is invalid. Filesystem isolation remains default-deny. The runner
does not itself start fixtures, daemons, or repetitions.

## Offline qualification

Use `scripts/eval/profile-preflight.sh` with the same arm root and pinned native
Codex executable before releasing an agent. It invokes only `codex sandbox` and
checks that the fixed arm paths behave as intended while this repository's
`go.mod` and the standard Codex auth location are unreadable. It never reads or
prints either blocked file's contents.

Example (the executable path is deliberately explicit and must be pinned in
the evaluation record):

```sh
scripts/eval/profile-preflight.sh \
  --codex-bin /path/to/pinned/native/codex \
  --arm-root /path/to/fresh-arm

scripts/eval/codex-profile-exec.sh \
  --codex-bin /path/to/pinned/native/codex \
  --arm-root /path/to/fresh-arm \
  --model gpt-5.6-luna
```

The profile must be requalified whenever the Codex CLI/runtime, its permission
profile semantics, or an arm's access requirements change.

Permission profiles govern the evaluator's local command execution; they do
not sandbox MCP servers launched by Codex itself. Native-arm stdio fixtures are
therefore pinned official binaries started through recorded wrappers that clear
their inherited environment. This is an explicit fixture trust boundary, not a
property of the command profile.
