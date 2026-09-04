# Onboarding and Fish completion evidence

Non-authoritative validation evidence, 2026-09-04. Scope:
[onboarding milestone](../onboarding-plan.md). Main-based worktree
`/tmp/wirecmd-onboarding`, branch `codex/onboarding-fish`; SDK remains v1.7.0.
The SDK/SSE draft was not modified. At the time of the local validation below,
no commit, push or release had been performed. The slice was subsequently
committed as `4c07770` and pushed for CI on 2026-09-04.

## Local gates

An isolated private runtime directory was created with `mktemp -d` so the
user's existing daemon could not influence offline tests. Host socket/cache
permissions were used for tests, not sandbox workarounds.

Passed:

```sh
XDG_RUNTIME_DIR=/tmp/wirecmd-onboarding-tests.iF9BDI go test -count=1 -timeout=120s ./...
XDG_RUNTIME_DIR=/tmp/wirecmd-onboarding-tests.iF9BDI go test -race -count=1 -timeout=180s ./...
go vet ./...
go mod verify
go build ./...
fish -n completions/wirecmd.fish scripts/test-fish-completion.fish
fish scripts/test-fish-completion.fish
git diff --check
```

Fish 4.8.1 native integration printed `Fish completion integration passed`.
Darwin arm64/amd64 cross-builds and the agent Skill validator also passed.
After the final FIFO guard and trusted-workspace test, the full ordinary suite,
focused help/completion race tests, Fish integration, vet and diff checks passed
again. CI now installs Fish and runs its integration test on Linux, but no
remote CI result is claimed by that initial local validation.

The subsequent push CI exposed Fish 3.7.0 on Ubuntu lacking `commandline -x`,
available on local Fish 4.8.1. Completion now falls back to the native `-opc`
tokenizer on older Fish, without evaluating command text or upgrading CI's
system shell. Both APIs handle quoted paths; variable expansion is available
only with the newer API. The rerun result is reported at handoff.

Independent review found and reproduced a FIFO config path blocking completion.
A completion-only regular-file check fixes that case; ordinary explicit-config
loading is unchanged. The reviewer retested the FIFO and found no remaining
actionable help/parser/completion issues. Same-UID path replacement races and
blocking filesystem metadata are not new guarantees of this completion helper.

## Consumer trial

Parent built the pinned official SDK fixtures without changing dependencies:

```sh
go build -o /tmp/wirecmd-onboarding-trial-bin .
go build -o /tmp/wirecmd-onboarding-memory github.com/modelcontextprotocol/go-sdk/examples/server/memory
go build -o /tmp/wirecmd-onboarding-everything github.com/modelcontextprotocol/go-sdk/examples/server/everything
```

An isolated foreground daemon used `/tmp/wirecmd-onboarding-trial.VHzq5w` as
XDG_RUNTIME_DIR. Explicit KDL configured `knowledge` as memory stdio (without
file persistence) and `utility` as everything stdio. No real providers or
credentials were used. The consumer received binary/config/runtime paths and
task goals, not schemas, implementation source or setup instructions.

The agent used global and focused help, scalar flags, nested JSON, stdin,
cross-process state and error recovery. Deliberately omitting `utility greet`'s
required name returned upstream-tool exit 5; adding `--name` returned exit 0.
An accidental omitted binary path caused shell exit 126, not a Wirecmd failure.
The first pass tested transformation and storage separately; parent requested a
follow-up that actually composed the two and used a projected array value.
This is guided functional evidence, not a model comparison or unattended
onboarding benchmark. No OAuth, completion UX by that agent, or HTTP trial is
implied; Fish completion has separate native tests.

The agent's temporary report contains manually transcribed excerpts; it is not
treated as a byte-exact transcript. Parent independently replayed this Bash
workflow with `set -euo pipefail`:

```sh
export XDG_RUNTIME_DIR=/tmp/wirecmd-onboarding-trial.VHzq5w
export WIRECMD_NONINTERACTIVE=1
wirecmd=(/tmp/wirecmd-onboarding-trial-bin --config /tmp/wirecmd-onboarding-trial.VHzq5w/config.kdl --format json --color never)
greeting=$("${wirecmd[@]}" utility greet --name 'Ada Lovelace')
transformed=$(printf '%s\n' "$greeting" | jq -r '.result.messages[0]' | tr '[:lower:]' '[:upper:]')
entities=$(jq -cn --arg observation "$transformed" '[{name:"Parent Verified Trial",entityType:"composition",observations:[$observation]}]')
"${wirecmd[@]}" knowledge create_entities --entities "$entities"
"${wirecmd[@]}" --json '{"names":["Parent Verified Trial"]}' knowledge open_nodes
"${wirecmd[@]}" knowledge delete_entities --entity-names '["Parent Verified Trial"]'
"${wirecmd[@]}" knowledge read_graph
```

Observed exit 0, empty stderr, and four success envelopes. Create and readback
both contained observation `HI ADA LOVELACE`; delete succeeded; final output:

```json
{"ok":true,"result":{"data":{"entities":null,"relations":null},"messages":["Graph read successfully"]},"server":"knowledge","tool":"read_graph"}
```

The official fixtures emitted protocol diagnostics to the separate foreground
daemon's stderr; empty stderr above refers to the ephemeral CLI invocations,
not to the fixture/daemon log.

Parent also exercised administrative help in a PTY: plain text, exit 0, no
daemon action. An incompatible admin-help flag returned a structured invocation
error. The disposable daemon was stopped after graph verification; the user's
daemon was left untouched.
