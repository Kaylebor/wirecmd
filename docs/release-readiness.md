# First Alpha Release Readiness

Status: completed authoritative first-alpha release, published 2026-08-26

## Objective

Make the current Linux implementation installable and honestly identifiable as
Wirecmd's first alpha without implying stable compatibility or broader platform
support. Release preparation adds version reporting, continuous qualification,
installation and runtime prerequisites, and a repeatable manual release
procedure. It does not add a packaging framework or automatic publication.

Historical evidence: the annotated `v0.1.0-alpha.1` tag and private GitHub
prerelease were published from CI-qualified commit `84c1d37`. Clean
explicit-version and `@latest` installations both reported
`wirecmd v0.1.0-alpha.1`; installed-binary direct and daemon-backed smoke
operations passed. Those commands describe that release event, not a maintained
latest-version installation contract. Publication remains manual for later
releases.

## Supported release surface

The supported source-build baseline is Go 1.26 or newer. The first release
target was Linux. Until Wirecmd provides a maintained latest-release path,
qualify and install an explicit source checkout:

```sh
git clone git@github.com:Kaylebor/wirecmd.git
cd wirecmd
git checkout COMMIT_TO_QUALIFY
go install .
```

Record `COMMIT_TO_QUALIFY` with the resulting evidence. Do not infer a current
prerelease through `@latest` or copy a version number from this historical
plan.

While the repository is private, installation requires authenticated GitHub
access and a private-module configuration such as:

```sh
go env -w 'GOPRIVATE=github.com/Kaylebor/*'
```

That command changes persistent Go environment state and is guidance, not an
operation Wirecmd performs. Git credentials must already grant repository
access. Once the repository is public, the ordinary `go install` command needs
no private-module setup.

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

The daemon requires an absolute, same-user `XDG_RUNTIME_DIR` and creates its
private socket below that directory. `--direct` remains the deliberate
daemonless diagnostic and one-shot path; normal commands never fall back to it.

OAuth-backed HTTP servers additionally require a usable Secret Service through
the native keyring. Interactive browser authorization uses `xdg-open`. Missing
or locked keyring service remains an explicit structured user-action failure;
there is no plaintext credential fallback.

## Qualification gates

Linux CI runs for pull requests, pushes to `main` and version tags, and manual
dispatches using the Go version declared in `go.mod`. It verifies modules, runs
the complete test suite and race detector, vets all packages, and builds all
packages. Before release, the same gates must pass locally:

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

1. Choose the intended semantic prerelease version without copying a version
   from this historical plan. Confirm `main` is clean, synchronized with
   `origin/main`, and contains the intended release commit.
2. Run every local qualification gate above and confirm the GitHub Actions run
   for that commit succeeds.
3. From a clean checkout of that exact commit, install with `go install .`,
   record the commit, and exercise one direct operation and one daemon-backed
   operation. Source builds identify themselves as `wirecmd dev`.
4. After separate release authorization, create an annotated tag using the
   chosen version on that exact commit and push only that tag.
5. Create a GitHub prerelease from the existing tag, using generated notes as a
   reviewed starting point, and verify that both the tag and release resolve to
   the qualified commit.

The tag and GitHub prerelease must not be created merely because the repository
passes its preparation checks. They require a separate explicit release action.

## Deferred

- Downloadable binary archives, checksums, signatures, and provenance
  attestations.
- Homebrew, distribution packages, containers, and service-manager units.
- Automated tagging or GitHub Release publication.
- Windows build or runtime qualification. macOS qualification is tracked in
  the [macOS plan](macos-plan.md).
- Stable-version compatibility promises.
