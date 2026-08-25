# Legacy fixture research

Status: non-authoritative working evidence, 2026-08-25

The project is pinned to `github.com/modelcontextprotocol/go-sdk` v1.7.0. Its
published compatibility list includes `2026-07-28`, `2025-11-25`,
`2025-06-18`, `2025-03-26`, and `2024-11-05`.

The SDK exposes all three relevant high-level client transports:

- `CommandTransport` for stdio;
- `StreamableClientTransport` for Streamable HTTP; and
- `SSEClientTransport` for the legacy HTTP+SSE transport defined by
  `2024-11-05`.

`ClientSession.InitializeResult().ProtocolVersion` exposes the negotiated
revision after connection. The public client API does not expose a protocol
version override; qualification must therefore be driven by actual server
behavior rather than a Wirecmd-side selector.

The v1.7.0 conformance `everything-server` is useful for the current modern
baseline, but it is not by itself proof of the legacy initialized path. Its
stdio and Streamable HTTP forms use the current SDK server, which implements
modern `server/discover` and advertises all transport-supported revisions. A
fixture intended to prove fallback must actually reject modern discovery and
negotiate through `initialize`.

The SDK ships an official in-process HTTP+SSE example and tests using
`NewSSEHandler` with `SSEClientTransport`, but no standalone SSE fixture
executable. Wirecmd can exercise the client transport without a new library,
provided the public KDL spelling for selecting legacy SSE is deliberated before
implementation.

## Adopted fixture decision

The considered legacy-initialization fixture choices were:

1. a pinned historical official server implementation that predates modern
   discovery, giving the strongest black-box compatibility evidence;
2. a small test-only current-SDK server transport or handler that deliberately
   rejects discovery and limits advertised versions, giving deterministic local
   coverage but weaker deployed-server evidence; or
3. both, using the deterministic fixture for regression tests and a historical
   official server for qualification evidence.

The project adopted a thin test-only server in the third shape: a nested module
pinned to the historical official SDK plus black-box Wirecmd qualification. It
lives in `testdata/legacy-mcp` and can move to a separate repository if its scope
grows. The user explicitly approved this external fixture dependency.

The selected historical SDK is the official Go SDK v1.6.1,
released 2026-05-22. Modern client and server `server/discover` support landed
afterward in June 2026, while v1.6.1 already provides both stdio and Streamable
HTTP forms of the stateful memory example. The selected thin fixture uses that
same historical SDK as a separate pinned executable, exercising a real legacy
initialization handshake without adding the older SDK to Wirecmd's production
module graph. The memory example remains version-selection evidence, not the
binary run by the qualification script.

## Primary sources

- SDK v1.7.0 compatibility table:
  <https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/README.md#version-compatibility>
- Supported revisions and negotiation order:
  <https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/shared.go#L45-L60>
- Streamable HTTP client transport:
  <https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/streamable.go#L1933-L2045>
- Legacy SSE client transport:
  <https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/sse.go#L356-L430>
- Official SSE example:
  <https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/mcp/sse_example_test.go>
- Current conformance server transports:
  <https://github.com/modelcontextprotocol/go-sdk/blob/v1.7.0/conformance/everything-server/main.go#L30-L70>
- Server-side modern discovery landed after v1.6.1:
  <https://github.com/modelcontextprotocol/go-sdk/commit/dd978160abb30bf7e4db06520cb2176a4f26eba6>
- Historical v1.6.1 memory server:
  <https://github.com/modelcontextprotocol/go-sdk/blob/v1.6.1/examples/server/memory/main.go>
