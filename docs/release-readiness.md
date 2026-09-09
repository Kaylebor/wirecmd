# Stable v0.1 Release Readiness

Status: authoritative stable v0.1 release contract; publication explicitly
authorized 2026-09-08

## Objective

Publish Wirecmd's first stable release with an explicit supported surface,
maintained installation path, compatibility promise, qualification gates, and
manual release procedure. Stable v0.1 does not imply support for deferred MCP
transports or primitives, exhaustive physical platform qualification, binary
packaging, or daemon service management.

The private alpha series established version reporting, direct and retained
daemon behavior, Linux qualification, and the initial macOS smoke evidence.
That prerelease history was intentionally not migrated when the repository was
recreated with sanitized public history. Stable qualification applies to the
current public repository and its exact release commit.

## Supported release surface

The supported build baseline is Go 1.26 or newer. Install the latest stable
release with:

```sh
go install github.com/Kaylebor/wirecmd@latest
```

For reproducible installation of this milestone, use:

```sh
go install github.com/Kaylebor/wirecmd@v0.1.0
```

Linux and macOS are supported. Apple Silicon has native CI and maintainer M2
smoke evidence; Intel macOS is CI-qualified without a physical-device claim.
Windows remains unsupported.

Stable v0.1 supports the public CLI, KDL configuration, structured result and
error contracts, daemon/direct split, stdio and Streamable HTTP MCP transports,
SDK-backed OAuth, and the documented native LSP operations. Modern and legacy
initialized stdio and Streamable HTTP are qualified. Legacy HTTP+SSE is not
implemented and remains explicitly deferred at the official SDK boundary.

Within the `v0.1.x` line, backward-incompatible changes to the documented CLI,
configuration, and structured output contracts require `v0.2.0`. A security or
correctness defect that cannot safely preserve existing behavior may require a
documented exception.

`wirecmd --version` is the standalone version query. It prints
`wirecmd VERSION` as conventional newline-terminated text and performs no
configuration discovery, daemon connection, keyring access, or upstream
request. Tagged module installations report their module version; local
development builds report `dev`. `wirecmd version` is not reserved and may
address a configured server named `version`.

## Runtime prerequisites

Normal commands require a separately running foreground daemon:

```sh
wirecmd daemon run
```

On Linux, the daemon requires an absolute, same-user `XDG_RUNTIME_DIR`. On
macOS, an unset value uses the validated private per-user temporary-directory
path. `--direct` remains the deliberate daemonless diagnostic and one-shot
path; normal commands never fall back to it.

OAuth-backed HTTP servers require a usable native credential store: Secret
Service on Linux or Keychain on macOS. Interactive browser authorization uses
`xdg-open` on Linux and `/usr/bin/open` on macOS. Missing or locked credential
storage remains an explicit structured user-action failure; there is no
plaintext fallback.

## Qualification gates

CI runs on Linux, macOS Apple Silicon, and macOS Intel for pull requests,
pushes to `main`, version tags, and manual dispatches. It uses the Go version
declared in `go.mod`, verifies modules, runs the complete test suite and race
detector, vets all packages, builds all packages, and checks Fish completion on
Linux. Before release, the same language gates must pass locally:

```sh
go mod verify
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build ./...
git diff --check
```

## Manual release procedure

Publication remains an explicit maintainer action:

1. Confirm `main` is clean and synchronized with `origin/main`, then freeze
   unrelated feature merges until release preparation lands. Before choosing
   the release commit, prepare the release notes manually: use
   GitHub-generated notes only as a reviewed draft, move the curated
   `[Unreleased]` content in `CHANGELOG.md` into a dated `[X.Y.Z]` entry,
   recreate an empty `[Unreleased]` section, and roll the comparison links
   forward. The new `[X.Y.Z]` link compares `vPREVIOUS...vX.Y.Z`, and
   `[Unreleased]` compares `vX.Y.Z...HEAD`. The first release may link directly
   to its GitHub Release because it has no prior tag. Review this complete
   changelog against the intended release contents, then commit it. Use bare
   `X.Y.Z` for changelog headings, and `vX.Y.Z` for Git tags, module
   installation, and reported binary versions.
2. Publish the release-preparation commit through the normal reviewed PR and
   merge path. If an unrelated change lands before it merges, re-curate the
   changelog against the resulting `main` and submit that revision for review;
   do not qualify a commit whose release summary was prepared for different
   contents. Then resynchronize `main` with the merged result; do not qualify
   or tag an unmerged release-preparation branch.
3. Select that exact merged `main` commit, then run every local qualification
   gate above and confirm the GitHub Actions run for that commit succeeds.
4. From a clean checkout of that exact commit, install with `go install .`,
   record the commit, and exercise one direct operation and one daemon-backed
   operation. Source builds identify themselves as `wirecmd dev`.
5. After separate release authorization, create a signed annotated `vX.Y.Z`
   tag on that exact commit and push only that tag.
6. Create a non-prerelease GitHub Release from the existing tag, using the
   same curated summary as the changelog, and mark it as
   the latest release.
7. Verify that `go install github.com/Kaylebor/wirecmd@vX.Y.Z` and
   `go install github.com/Kaylebor/wirecmd@latest` both report the intended
   version and exercise the release smoke operations.

The tag and GitHub Release require an explicit release action. That action was
authorized for `v0.1.0` on 2026-09-08, conditional on the exact release commit
passing the gates above.

## Deferred

- Downloadable binary archives, checksums, signatures, and provenance
  attestations.
- Homebrew, distribution packages, containers, and service-manager units.
- Automated tagging or GitHub Release publication.
- Windows build or runtime qualification. macOS qualification is tracked in
  the [macOS plan](macos-plan.md).
- Legacy HTTP+SSE and additional MCP primitives listed in the compatibility
  plan.
