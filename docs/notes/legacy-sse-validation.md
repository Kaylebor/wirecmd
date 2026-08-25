# Legacy HTTP+SSE compatibility tracking

Status: non-authoritative deferred-compatibility note, 2026-08-25

Wirecmd supports and qualifies only legacy initialized stdio and Streamable
HTTP with the pinned official Go SDK v1.7.0. Its uncommitted HTTP+SSE KDL,
runtime, tests, and fixture mode were removed rather than retaining an
unqualified public transport surface.

## Cross-version failure

The v1.7.0 client failed to connect to the v1.6.1 `NewSSEHandler` fixture with:

```text
connection closed: calling "initialize": client is closing: failed to write: 400 Bad Request
```

The modern client first sent `server/discover`. The historical SSE handler
rejected that unknown method at the HTTP layer with status 400. The connection
closed before the SDK's legacy `initialize` fallback could run.

The pinned v1.7.0 SDK has no exported client option for selecting the initial
protocol revision. As checked on 2026-08-25, v1.7.0 remains the latest stable
release; upstream PR #1127 adds `ClientSessionOptions.ProtocolVersion`, but it
is merged and unreleased.

## Upstream tracking

- [Issue #1112](https://github.com/modelcontextprotocol/go-sdk/issues/1112)
  reported that the SDK SSE server advertised a modern revision over a legacy
  transport. [PR #1121](https://github.com/modelcontextprotocol/go-sdk/pull/1121)
  fixes its advertised-version filtering and modern-client fallback behavior.
- [Issue #1113](https://github.com/modelcontextprotocol/go-sdk/issues/1113)
  requested a public client protocol-version choice. [PR #1127](https://github.com/modelcontextprotocol/go-sdk/pull/1127)
  exports `ClientSessionOptions.ProtocolVersion` while retaining normal
  negotiation semantics.
- [Issue #1177](https://github.com/modelcontextprotocol/go-sdk/issues/1177)
  records the related fatal `server/discover` problem on legacy stdio servers;
  the maintainer recommendation is to wait for the release containing PR #1127.

## Re-evaluation trigger

Revisit legacy HTTP+SSE only when the latest stable official Go SDK release
contains `ClientSessionOptions.ProtocolVersion` from PR #1127. First evaluate
that release under the dependency decision process, then requalify a historical
SSE fixture by explicitly requesting its legacy protocol revision. Do not add a
local protocol shim, unreleased SDK pin, KDL transport, or runtime branch before
that result.

The existing stdio and Streamable HTTP qualification remains unchanged in scope.
