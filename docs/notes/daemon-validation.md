# Foreground Daemon Validation Evidence

Status: non-authoritative development evidence

## Scope

This records the first retained-stdio qualification for the private foreground
daemon. It does not establish service-manager behavior, automatic recovery, or
HTTP compatibility.

## Fixture and command

The fixture was the pinned official Go SDK v1.7.0 memory example, built without
its `-memory` persistence option. The test used a private temporary
`XDG_RUNTIME_DIR` and ran:

```sh
WIRECMD_OFFICIAL_MEMORY_BINARY=/tmp/wirecmd-official-memory-v1.7.0 \
  go test -count=1 -run TestOfficialMemoryFixture -v ./internal/cli
```

The test starts the Wirecmd daemon, creates `WirecmdFixture` through one normal
CLI invocation, reads it through a second normal invocation, reads a fresh
empty graph through `--direct`, reloads the daemon, then verifies that the next
normal read is fresh too.

Observed result on 2026-08-24: PASS.

## Contract observations

- Normal calls require the private daemon socket; there is no direct fallback.
- Retained upstream stderr belongs to the foreground daemon and is redacted
  there. It is intentionally not multiplexed to short-lived client stderr.
- Reload globally retires cached contexts and instances; it does not watch files
  or recover failed instances automatically.
