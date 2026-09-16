# SDK 1.8 prerelease / SSE evidence

Non-authoritative working evidence, 2026-09-04. Authoritative scope and merge
gate: [draft SSE milestone](../plans/sse-plan.md). No release is authorized here.

## Dependency and initial gate

Main module: official Go SDK `v1.8.0-pre.2`; historical fixture remains
`v1.6.1`. Go remains 1.25. The dependency-only commit also lets `go mod tidy`
correct the existing oauth2 import classification; its version is unchanged.

The prerelease includes [PR #1127](https://github.com/modelcontextprotocol/go-sdk/pull/1127)
and [PR #1121](https://github.com/modelcontextprotocol/go-sdk/pull/1121).
Stable was still v1.7.0 when inspected. No alternate SDK or protocol shim was
introduced. SSE OAuth remains unsupported because `SSEClientTransport` has no
OAuth handler; query/header secrets and static authentication are supported.

Before exposing KDL/runtime SSE, a disposable standalone client was built:

```sh
# cwd: testdata/legacy-mcp
go build -buildvcs=false -o /tmp/legacy-mcp-sse-probe .
# cwd: repository root
go build -buildvcs=false -o /tmp/legacy-sse-probe /tmp/legacy-sse-probe.go
```

The historical server was started with `--listen 127.0.0.1:0 --sse
--protocol-record PATH`. The probe used the SDK's SSE transport and
`ClientSessionOptions{ProtocolVersion: "2024-11-05"}`. Observed output:

```text
initialized=2024-11-05 tools=[delete_value fail read_value set_value] call=set_value close=ok
```

The server's recorded initialized revision was independently asserted to be
`2024-11-05`. This passed before configuration/runtime integration began.
The disposable probe is not part of the repository; the integrated script
below is the maintained reproduction.

## Integrated historical matrix

```sh
bash scripts/qualify-legacy.sh
```

The script builds both modules, runs isolated loopback fixtures and a private
daemon, and exercises stdio, Streamable HTTP and SSE through the compiled CLI.
It checks discovery, focused help, projected and exact calls, state retention
across separate CLI processes, fresh direct-session isolation, exit-5 tool
errors, three-instance reload, and clean SIGTERM/socket removal. Machine-output
flags are explicit. It asserts every recorded revision rather than inferring
protocol behavior from transport success.

Observed exit 0 and stdout:

```text
legacy stdio/Streamable HTTP (2025-11-25) and SSE (2024-11-05) qualification passed
```

This script also runs in Linux and both macOS architecture CI jobs on the draft
branch. Results must be rechecked after replacing the prerelease with stable.

## Environment and remaining gate

Local tests use a fresh private `XDG_RUNTIME_DIR`. An initial run using the
caller's runtime found an existing test daemon, causing the test that expects
an offline daemon to return a successful server listing. Isolation resolved
that pre-existing environmental assumption; no daemon or runtime behavior was
changed to hide the failure. Host permissions are required for socket tests.

Local full checks passed:

```sh
XDG_RUNTIME_DIR=/tmp/wirecmd-pre2-tests.u2si3g go test -count=1 ./...
XDG_RUNTIME_DIR=/tmp/wirecmd-pre2-tests.u2si3g go test -race -count=1 ./...
go vet ./...
go mod verify
go build ./...
GOOS=darwin GOARCH=arm64 go build -o /tmp/wirecmd-sse-darwin-arm64 .
GOOS=darwin GOARCH=amd64 go build -o /tmp/wirecmd-sse-darwin-amd64 .
GOOS=linux GOARCH=arm64 go build -o /tmp/wirecmd-sse-linux-arm64 .
shellcheck scripts/qualify-legacy.sh
git diff --check
```

The runtime directory above was created with `mktemp -d`; create a fresh private
one when reproducing. Added tests cover direct/daemon SSE contracts, retained
state/reload, canceled-session failure, headers/query/redaction, transport
fingerprints and SSE OAuth rejection. The existing OAuth regression suite also
passed against pre.2. The updated agent Skill passed its validator.

Independent review found no production blocker or unnecessary protocol
abstraction. It caught a test cleanup ordering error: the SSE fixture closed
before the daemon released its retained GET connection. Registering daemon
cleanup after fixture setup fixed the hang; both affected tests passed with a
30-second diagnostic test timeout. Stale test processes were stopped; the
user's separate testing daemon was left running. Cross-origin header forwarding
was inspected against the existing same-origin guard, but no new cross-origin
runtime test was added.

Draft CI is recorded at handoff. Physical M2 SSE
testing and real-provider SSE qualification are not implied by fixture success.
No merge or release before stable SDK qualification.

## Stable v1.8.0 follow-up

On 2026-09-14, upstream published stable `v1.8.0` and stated that it is
equivalent to `v1.8.0-pre.2`. GitHub's tag comparison reported zero commits
between those tags. The main module was updated to the stable tag; the original
prerelease observations above remain historical evidence. Stable local and CI
results are recorded by the porting PR rather than retroactively rewriting the
earlier commands.

The stable port passed `go test -count=1 ./...`, the full race suite (including
the CLI package in 122.546 seconds), `go vet ./...`, `go mod verify`, the Linux
build, Darwin arm64 and amd64 cross-builds, `git diff --check`, and
`scripts/qualify-legacy.sh`. The stable CI result remains a PR gate rather than
being inferred from these local checks.

## PR-run cancellation regression

The initial push CI passed all platforms, but the separate PR-triggered Linux
run 33885124268 failed `TestDaemonCancellationMarksSSEInstanceBroken`. The test
released its blocked upstream tool immediately after the CLI returned, racing
upstream completion against the daemon observing the client disconnect. The
test now keeps the tool blocked until the daemon records cancellation; fixture
cleanup retains its fallback release. No production behavior or timeout changed.
The corrected test passed 100 repetitions and 30 race-enabled repetitions, and
the full local suite passed before pushing the correction.
