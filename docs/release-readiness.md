# First Alpha Release Readiness

Status: authoritative release preparation for `v0.1.0-alpha.1`; tag and GitHub prerelease not yet created

## Objective

Make the current Linux implementation installable and honestly identifiable as
Wirecmd's first alpha without implying stable compatibility or broader platform
support. Release preparation adds version reporting, continuous qualification,
installation and runtime prerequisites, and a repeatable manual release
procedure. It does not add a packaging framework or automatic publication.

## Supported release surface

The first release target is Linux with Go 1.25 or newer. The supported source
installation path is:

```sh
go install github.com/Kaylebor/wirecmd@latest
```

Before a stable release exists, Go resolves `@latest` to the highest available
prerelease. The first alpha can always be selected explicitly:

```sh
go install github.com/Kaylebor/wirecmd@v0.1.0-alpha.1
```

After any stable version is published, `@latest` prefers the highest stable
version over prereleases. Users evaluating a later alpha must then name its
version explicitly.

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

Linux CI runs on every push and pull request using the Go version declared in
`go.mod`. It verifies modules, runs the complete test suite and race detector,
vets all packages, and builds all packages. Before release, the same gates must
pass locally:

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

1. Confirm `main` is clean, synchronized with `origin/main`, and contains the
   intended release commit.
2. Run every local qualification gate above and confirm the GitHub Actions run
   for that commit succeeds.
3. Create annotated tag `v0.1.0-alpha.1` on that exact commit and push only that
   tag.
4. Create a GitHub prerelease from the existing tag, using generated notes as a
   reviewed starting point.
5. From a clean environment, install both
   `github.com/Kaylebor/wirecmd@v0.1.0-alpha.1` and
   `github.com/Kaylebor/wirecmd@latest`.
6. Confirm both installed binaries print `wirecmd v0.1.0-alpha.1` from
   `wirecmd --version`, then exercise one direct operation and one daemon-backed
   operation.

The tag and GitHub prerelease must not be created merely because the repository
passes its preparation checks. They require a separate explicit release action.

## Deferred

- Downloadable binary archives, checksums, signatures, and provenance
  attestations.
- Homebrew, distribution packages, containers, and service-manager units.
- Automated tagging or GitHub Release publication.
- macOS and Windows build or runtime qualification.
- Stable-version compatibility promises.
