# OAuth session reuse investigation, 2026-09-04

Status: non-authoritative implementation evidence.

## User-reported Mac qualification

The user tested the installed release on an M2, outside this workspace.
Ordinary daemon-backed `wirecmd cloudflare` triggered browser OAuth and
returned tool information successfully. Subsequent rapid sequential calls
sometimes returned `authorization_in_progress`, even after the initial
command had returned to the prompt. Waiting appeared to help. JSON output
regardless of TTY is the intended ordinary-command contract.

This report confirms a successful interactive authorization and discovery
flow, not completion of the full physical Mac qualification checklist.
No provider credentials or authorization URLs were captured here.

## Mechanism

The daemon pool entry retains both its authorization-started signal and its
session-ready signal. After automatic authorization completes, both channels
are closed. The old non-owner checkout path selected between those signals,
allowing a completed session to randomly return `authorization_in_progress`.

The signals do not expire, so waiting is not a reliable workaround. This is
local daemon coordination, not evidence of a native Keychain or SDK OAuth
failure. The intended correction gives completed startup precedence while
preserving rejection of a second caller during an actual active startup flow.

## Reproduction

The new SDK-backed local fixture test performs automatic OAuth during ordinary
daemon tool discovery, waits for that invocation to return, and then issues 32
sequential discovery invocations without a delay. It checks that only one
authorization occurs.

Before the daemon fix:

```sh
go test -count=1 ./internal/cli -run '^TestOAuthFixtureDaemonReusesAutomaticAuthorizationSession$'
```

The initial authorization succeeded, but immediate reuse number 1 failed with
exit 8 and error code `authorization_in_progress`. This reproduces the user
report without a real provider or native Keychain. A separate state-level test
covers a genuinely active flow and preservation of a completed startup error.

## Validation after the fix

```sh
go test -count=100 ./internal/cli -run '^(TestOAuthFixtureDaemonReusesAutomaticAuthorizationSession|TestDaemonOAuthStartupWaitDistinguishesActiveAndCompletedFlows)$'
go test -race -count=20 ./internal/cli -run '^(TestOAuthFixtureDaemonReusesAutomaticAuthorizationSession|TestDaemonOAuthStartupWaitDistinguishesActiveAndCompletedFlows)$'
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go mod verify
go build ./...
GOOS=darwin GOARCH=arm64 go build ./...
GOOS=darwin GOARCH=amd64 go build ./...
git -c core.fsmonitor=false diff --check
```

All passed. Socket-dependent tests ran with host access. Darwin cross-builds
initially reported missing standard-library packages inside the sandbox; the
same commands passed with host access. Cross-builds are not native execution.
Independent review found no actionable findings and exercised additional
focused OAuth and coalesced-startup tests.

No dependency, SDK, credential-store, retry, or delay changes were needed.
This local fix is not yet a tagged release and still needs physical Mac
verification. Preserve `alpha.2`; any release carrying the fix must move
forward, as specified in the macOS qualification plan.
