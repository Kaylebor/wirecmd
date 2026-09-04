# Contextual output validation

Non-authoritative validation evidence, 2026-09-04. Contract:
[contextual output](../output-plan.md). No real credentials or remote providers
were used, and no commit/release was made by this slice.

## Actual CLI smoke

Built the existing legacy SDK fixture and candidate CLI:

```sh
(cd testdata/legacy-mcp && go build -o /tmp/wirecmd-output-legacy-fixture .)
go build -o /tmp/wirecmd-output-smoke.NTm8yr/wirecmd-cli .
```

An isolated `0700` temporary runtime directory contained this config:

```kdl
wirecmd {
    server "fixture" {
        scope "workspace"
        stdio "/tmp/wirecmd-output-legacy-fixture"
    }
}
```

The smoke runner used Python's `pty.openpty` for actual stdout terminals and
subprocess pipes for non-terminals. It set `WIRECMD_NONINTERACTIVE=1`, isolated
`XDG_RUNTIME_DIR`, `TERM=xterm-256color`, and initially empty `NO_COLOR`.
The local runner was invoked as:

```sh
python /tmp/wirecmd-output-smoke.NTm8yr/smoke.py
```

Equivalent commands under test (with `wirecmd-cli` and the temporary config):

```sh
wirecmd-cli --direct --config config.kdl
wirecmd-cli --format json --color never --direct --config config.kdl
wirecmd-cli --format pretty --direct --config config.kdl
wirecmd-cli --format json --colour always --direct --config config.kdl
wirecmd-cli --format pretty --color never --direct --config config.kdl fixture
wirecmd-cli --format pretty --direct --config config.kdl fixture read_value
wirecmd-cli --format pretty --direct --config config.kdl fixture fail
wirecmd-cli --format json --color always --help --direct --config config.kdl fixture read_value
wirecmd-cli --format json --color never daemon run
wirecmd-cli --format pretty daemon status
wirecmd-cli --format pretty --config config.kdl fixture read_value
wirecmd-cli --config config.kdl
wirecmd-cli --format pretty daemon reload
wirecmd-cli --format pretty config trust list
wirecmd-cli --format pretty config trust /tmp/wirecmd-output-smoke.NTm8yr
wirecmd-cli --format pretty config trust status /tmp/wirecmd-output-smoke.NTm8yr
wirecmd-cli --format pretty config untrust /tmp/wirecmd-output-smoke.NTm8yr
XDG_RUNTIME_DIR= wirecmd-cli --format pretty daemon run
wirecmd-cli --format pretty --color invalid
wirecmd-cli --format pretty --color never daemon run
```

Observed:

- Non-TTY default: one compact JSON line; TTY default: readable server columns
  with Wirecmd-owned ANSI labels. `NO_COLOR=1` and `TERM=dumb` each disabled
  automatic color but retained readable formatting.
- Explicit JSON plus no color remained parseable in a PTY. Forced color worked
  in a pipe despite `NO_COLOR=1`; removing owned styling yielded valid JSON.
- Tool discovery retained full descriptions. `read_value` preserved the whole
  envelope, including `data:{exists:false,value:""}` and the JSON-looking
  message string, using two-space indentation rather than decoding the string.
- Tool failure returned exit 5, readable category/code/message/action and its
  attached JSON result. All other listed operations returned exit 0. Captured
  stderr was empty throughout these non-authentication checks.
- Focused help stayed plain text even with JSON format and forced color.
- Daemon readiness remained one JSON response. Readable status and reload
  counts worked; decoded direct/daemon result envelopes were identical.
- SIGTERM shutdown exited 0, with no extra stdout/stderr and socket removed.
- Empty trust listing, trust/status/untrust were checked with isolated
  `XDG_STATE_HOME` and exited 0. Empty `workspaces:null` now renders the same
  trusted-workspaces heading as an empty array. Pretty startup failure exited
  7 and invalid color exited 2, both retaining recovery fields on stdout with
  empty stderr. Pretty foreground readiness was also checked.

An initial harness setup placed the binary at the daemon's required `wirecmd`
directory path; startup correctly refused that non-directory. Moving the test
binary to `wirecmd-cli` resolved the harness conflict without product changes.

## Automated gates

The following gates passed (Go fixture/socket checks used host permissions):

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go mod verify
GOOS=darwin GOARCH=arm64 go build -o /tmp/wirecmd-output-smoke.NTm8yr/wirecmd-darwin-arm64 .
GOOS=darwin GOARCH=amd64 go build -o /tmp/wirecmd-output-smoke.NTm8yr/wirecmd-darwin-amd64 .
GOOS=linux GOARCH=arm64 go build -o /tmp/wirecmd-output-smoke.NTm8yr/wirecmd-linux-arm64 .
git -c core.fsmonitor=false diff --check
uv run --with pyyaml -- python "${CODEX_HOME:-$HOME/.codex}/skills/.system/skill-creator/scripts/quick_validate.py" skills/wirecmd
```

An independent reviewer found a schema-derived control-escaping gap in focused
help; it was fixed with regression coverage. Exact-call help also preserves
the original tool name while safely quoting its JSON envelope. The reviewer
rechecked and reported no remaining actionable findings, independently running
tests, vet and pipe/PTY presentation checks. Cross-builds do not substitute for
physical macOS terminal validation.
