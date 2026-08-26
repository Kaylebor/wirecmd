# macOS Apple Silicon Qualification

Status: authoritative current milestone; native CI and physical M2 qualification pending

## Objective

Make Wirecmd usable on stock macOS without weakening the daemon, credential,
or shell contracts already qualified on Linux. Apple Silicon is the physical
support target. Intel receives the same native CI gates, but no physical-device
claim.

This milestone is implemented through a separate pull request and is intended
for private prerelease `v0.1.0-alpha.2`. The repository remains private until
the tagged build passes the physical M2 checklist.

## Runtime contract

An explicitly configured `XDG_RUNTIME_DIR` retains the existing contract on
every platform: it must be absolute, private, and owned by the current user. An
invalid explicit value is an error and never triggers fallback.

When `XDG_RUNTIME_DIR` is unset on Darwin, Wirecmd uses `os.TempDir()` as the
runtime base only if that directory passes the same absolute-path, ownership,
directory, and private-mode checks. The Wirecmd child directory remains `0700`
and its lock and socket paths remain `0600`. Linux continues to require an
explicit `XDG_RUNTIME_DIR`.

Interactive OAuth launches the default browser with `xdg-open` on Linux and
`/usr/bin/open` on Darwin. Opener failure remains diagnostic-only after the
authorization URL is emitted; it does not cancel the callback listener.

Configuration and encrypted-state discovery retain their current XDG paths and
HOME fallbacks. This milestone does not migrate paths into `~/Library` or add a
second configuration contract.

## Qualification

GitHub Actions runs the complete module verification, test, race, vet, and
build gates on Linux, macOS 15 Apple Silicon, and macOS 15 Intel. Native macOS
tests cover Unix-socket lifecycle, locking, ownership checks, reload,
cancellation, and SIGTERM child cleanup. Keyring protocol tests remain
deterministic through the existing mock boundary.

After the PR is squash-merged, the squash commit is tagged as
`v0.1.0-alpha.2`. Physical qualification installs that exact private release on
an M2 and verifies:

- version reporting and daemon startup without `XDG_RUNTIME_DIR`;
- daemon status, reload, interruption, restart, and retained stdio continuity;
- direct and daemon-backed stdio plus unauthenticated Streamable HTTP;
- browser-launched dynamic-registration OAuth;
- macOS Keychain persistence across CLI and daemon restarts; and
- authentication status, protected calls, logout, and absence of secret
  material in diagnostics or filesystem names.

If physical qualification fails, preserve the tag and fix forward in
`alpha.3`. Only after the checklist passes may the milestone be marked
complete, the prerelease notes be updated with Apple Silicon qualification,
and the repository be made public.

## Deferred

- Physical Intel Mac qualification.
- launchd agents, socket activation, detached daemon startup, and autostart.
- Application signing, notarization, installers, binary archives, Homebrew,
  and distribution packaging.
- Native `~/Library` configuration or state-path migration.
- Windows support.
