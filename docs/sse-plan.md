# SDK 1.8 and legacy HTTP+SSE qualification

Status: implemented, qualified, and released in v0.3.0 with stable SDK `v1.8.0`.
See [local evidence](notes/sse-pre2-validation.md).

The historical server fixture stays pinned to v1.6.1. The main module uses
stable SDK `v1.8.0`, which upstream declares equivalent to the previously
qualified `v1.8.0-pre.2` tag.

## Contract

First prove historical SSE initialization, tool enumeration, invocation and
shutdown using the SDK's `SSEClientTransport` with
`ClientSessionOptions{ProtocolVersion: "2024-11-05"}`. Only after that succeeds
expose `sse "URL"` alongside `stdio` and `http`; `http` remains Streamable HTTP.
There is no automatic transport fallback or public protocol-version flag.

SSE reuses existing endpoint validation, query/header values, environment
secrets, redaction and provenance. Same-transport layers compose; a transport
switch replaces the previous definition without inheriting its credentials.
Fingerprints and retained instance identities distinguish transport kind.
Server listings identify this transport as `sse`.

SSE supports unauthenticated access and configured static headers, including
secret-backed Authorization. Transparent SSE OAuth is deferred because the
SDK transport exposes no OAuth handler. Reject SSE OAuth blocks and auth
administration clearly; do not use the native credential store for SSE.
Streamable HTTP OAuth retains its existing behavior and regression coverage.

All discovery, calls, errors, output formats, daemon continuity, reload and
shutdown retain the existing shell contract. The SDK owns initialization,
negotiation, parsing, pagination, calls and lifecycle. Confirmed additional SDK
gaps require discussion, not local protocol shims or weakened acceptance.

## Historical acceptance and merge gate

The following checklist governed the v0.3.0 change. Stable local evidence is
recorded below, and [PR #6](https://github.com/Kaylebor/wirecmd/pull/6) passed
Linux and both macOS CI jobs before merge.

- Extend the historical fixture with SDK-backed SSE, preserving its older SDK.
- Exercise direct and retained daemon discovery/help/calls, state isolation,
  reload, cancellation, connection failure, and clean shutdown.
- Cover typed values, transport switching, provenance, fingerprints, secret
  isolation, static header forwarding and redirect safety.
- Run existing modern and legacy transport/OAuth regressions, full tests,
  race tests, vet, module verification, builds and Linux/macOS CI.
- Record reproducible evidence and obtain independent lifecycle/security/
  overengineering review.
- Keep the SDK bump independently reviewable. Do not tag or release from the
  feature branch.
- Inspect stable SDK release notes and repeat qualification/review before merge;
  merging remains a separate maintainer action.

Unrelated dependencies, templates, wider pools, new protocol primitives and
SSE OAuth integration are outside this branch.
