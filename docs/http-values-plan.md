# Typed HTTP Query and Header Values

Status: authoritative current milestone

This plan records the accepted initial configuration contract for static query
parameters and request headers on Streamable HTTP endpoints. It extends the
completed [automatic discovery milestone](discovery-plan.md) without changing
the shell-facing command or the official SDK's MCP transport behavior.

## KDL contract

An `http` node may contain repeated keyed `query` and `header` children:

```kdl
http "https://example.test/mcp" {
    query tenant="acme"
    query token=(secret)"env://API_TOKEN"

    header X-API-Key=(secret)"env://API_KEY"
    header Authorization=(secret)"env://AUTHORIZATION"
}
```

Values are literal strings unless they use the native KDL `(secret)` annotation.
The initial secret provider is `env://NAME`; the resolved value is the complete
destination value. Authorization prefixes are not inferred or composed, so an
`AUTHORIZATION` value must already contain its scheme and credentials.

Query names are case-sensitive. Header names are case-insensitive for duplicate
detection, overlay matching, and validation, while their configured spelling is
retained for request construction. Duplicate keyed entries in one source are
configuration errors.

## Composition and endpoint construction

Configuration layers compose from weakest to strongest. A stronger layer
replaces a matching query or header key while retaining the replaced entry's
position; a new key appends in source order. Existing query parameters in the
endpoint URL remain supported, and a structural `query` entry replaces a
matching URL key. Present-empty values remain meaningful.

The base endpoint remains an absolute `http` or `https` URL. It is parsed and
encoded structurally rather than assembled by string concatenation. Query
values are added through the URL query representation, and headers are injected
through a narrow client wrapper around the official SDK's HTTP client. Wirecmd
does not implement MCP negotiation, session handling, retries, or a parallel
transport.

## Resolution, safety, and identity

Only the selected server's secret references are resolved. Listing, help, and
other operations that do not execute a server do not resolve unselected
secrets. Missing references produce the structured `secret_not_available`
condition; present-empty values are not treated as missing.

Resolved values are validated as UTF-8 text. Header names must be valid HTTP
tokens, header values must reject CR/LF, and transport-owned headers are
reserved. The HTTP transport set is `Host`, `Content-Length`, `Content-Type`,
`Accept`, `Connection`, `Transfer-Encoding`, `Trailer`, `Upgrade`, and
`Proxy-Connection`. The MCP/session set is `Mcp-Protocol-Version`,
`Mcp-Session-Id`, `Mcp-Method`, `Mcp-Name`, `Mcp-Param-*`, and
`Last-Event-ID`. Matching is case-insensitive. Resolved secrets never appear
in diagnostics, effective configuration, fingerprints, or unredacted URLs.

In daemon mode, resolved values that affect instance startup contribute to an
internal authentication identity. Different resolved credentials therefore
select different retained instances, while identical credentials can reuse one;
the secret material itself is neither logged nor persisted as identity data.

## Deferred scope

This slice does not add OAuth flows, templated or dynamically composed values,
dynamic per-request headers, retries, custom MCP transports, or a new
configuration format. The official Go MCP SDK remains the owner of protocol and
HTTP session behavior.

## Acceptance record

- KDL parses and composes literal and `(secret)"env://..."` query/header values
  with stable order and source provenance.
- Endpoint query encoding, existing-URL overrides, empty values, header
  case-insensitivity, invalid names/values, and reserved-header rejection are
  deterministic.
- Direct and daemon-backed execution resolve only the selected server and
  preserve header injection across SDK-owned requests without replacing SDK
  headers.
- Missing secrets fail as `secret_not_available`; diagnostics and fingerprints
  remain redacted; distinct startup credentials do not share retained instances.
