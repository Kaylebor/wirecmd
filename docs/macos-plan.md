# macOS Apple Silicon Qualification

Status: implemented; native CI passed and user-reported M2 release smoke passed.
The full physical checklist below remains the qualification reference, not an
assertion that every item has been exercised.

## Objective

Make Wirecmd usable on stock macOS without weakening the daemon, credential,
or shell contracts already qualified on Linux. Apple Silicon is the physical
support target. Intel receives the same native CI gates, but no physical-device
claim.

Historical record: this milestone was introduced during the private
`v0.1.0-alpha.2` prerelease and the user reported M2 daemon/Cloudflare OAuth
and terminal-output smoke success on 2026-09-04. That evidence does not make a
version number a future qualification procedure. The maintainer accepted this
smoke evidence together with native public CI as the stable v0.1 Apple Silicon
gate; publication remained a separate explicit decision.

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

For each physical qualification, build and install Wirecmd from the exact
version or source commit being qualified. Record that version or commit in the
qualification evidence. On an M2, the broader reference checklist is:

- version reporting and daemon startup without `XDG_RUNTIME_DIR`;
- daemon status, reload, interruption, restart, and retained stdio continuity;
- direct and daemon-backed stdio plus unauthenticated Streamable HTTP;
- browser-launched dynamic-registration OAuth;
- macOS Keychain persistence across CLI and daemon restarts; and
- authentication status, protected calls, logout, and absence of secret
  material in diagnostics or filesystem names.

If physical qualification fails, preserve the evidence and fix forward from the
qualified version. Stable v0.1 claims Apple Silicon support based on native CI
and the accepted M2 smoke evidence above, not exhaustive execution of every
reference checklist item.

## Deferred

- Physical Intel Mac qualification.
- launchd agents, socket activation, detached daemon startup, and autostart.
- Application signing, notarization, installers, binary archives, Homebrew,
  and distribution packaging.
- Native `~/Library` configuration or state-path migration.
- Windows support.
