# Transparent OAuth and Credential Persistence

Status: completed authoritative milestone, concluded 2026-08-26

The milestone passed. Deterministic SDK-backed fixtures cover dynamic and
preregistered clients, PKCE and resource binding, discovery fallbacks,
authorization denial, refresh failure, callback validation and cancellation,
credential replacement, daemon retirement, concurrency, encrypted persistence,
and secret-safe output. Real-provider evidence covers Cloudflare DCR and records
Figma's catalog restriction without introducing a compatibility shim.

## Objective

Make OAuth-backed Streamable HTTP servers usable from the same shell contract
as unauthenticated servers. Interactive terminal callers may complete a
browser login transparently; scripts, CI, and headless callers remain
deterministic and receive a structured recovery action.

Wirecmd owns only the shell and daemon boundary, browser handoff, credential
persistence, redaction, and error mapping. The pinned official MCP SDK
(`github.com/modelcontextprotocol/go-sdk v1.7.0`) owns OAuth discovery,
protected-resource and authorization-server metadata, PKCE, dynamic client
registration, preregistered clients, token exchange, refresh, resource
indicators, scopes, issuer validation, and HTTP retry behavior. Wirecmd does
not create a second OAuth or MCP protocol implementation.

## Public contract

Credential administration is exposed as:

```text
wirecmd auth login <server>
wirecmd auth status <server>
wirecmd auth logout <server>
```

These commands use automatic configuration discovery or the supplied ordered
`--config` paths, like ordinary operations. They are daemon-backed by default;
`--direct` deliberately performs the one-shot operation without the daemon.
The administrative forms do not accept call-only argument modes. A server
named `auth` remains callable through `--json` or an exact-call envelope.

`status` reports only local state (`authenticated` or `unauthenticated`),
registration kind, and token expiry when known. It does not contact the
provider and never prints tokens, client secrets, key identifiers, or
sensitive URLs. `logout` deletes Wirecmd's local record and retires matching
daemon sessions; it does not claim provider-side token revocation.

Every operation writes exactly one final newline-terminated JSON envelope to
stdout. Authorization URLs, browser diagnostics, and upstream diagnostics go
to stderr only.

For ordinary protected HTTP calls, a stored token is used and refreshed by the
SDK. If authorization is required, Wirecmd may begin browser authorization
when both stdin and stderr are TTYs. Set `WIRECMD_NONINTERACTIVE=1` to disable
that behavior. Headless ordinary calls return `authorization_required` in the
authentication category with exit code 4 and an action naming `wirecmd auth
login <server>`. Explicit login also requires a local interactive terminal;
otherwise it returns a structured user-action error. A second authorization
for the same identity returns `authorization_in_progress`; flows are not
silently duplicated or joined.

## Configuration

OAuth is implicit for an HTTP server without a configured `Authorization`
header. The SDK uses dynamic client registration in that case. A preregistered
client is configured as follows:

```kdl
server "remote" {
    scope "workspace"
    http "https://example.test/mcp" {
        oauth {
            client-id "wirecmd-client"
            client-secret (secret)"env://OAUTH_CLIENT_SECRET"
            redirect-uri "http://127.0.0.1:8765/callback"
        }
    }
}
```

The `oauth` block requires `client-id` and an exact loopback HTTP
`redirect-uri` with an explicit port; `client-secret` is optional and accepts
the existing literal or `(secret)"env://NAME"` value. Dynamic registration
binds an ephemeral `127.0.0.1` callback. Loopback redirects may use
`127.0.0.1`, `::1`, or `localhost`, but may not contain user information or a
fragment.

A configured `Authorization` header disables OAuth. Combining that header with
an `oauth` block is invalid. Client ID Metadata Documents, custom scopes,
device authorization, client credentials, non-loopback callbacks, and
provider-specific registration modes are deferred.

## Daemon and storage boundary

The daemon owns the loopback listener, SDK authorization transaction, callback
handoff, and persistence for normal calls. It sends an interim private IPC
authorization-URL event; the CLI prints the URL to stderr and attempts to open
it with `xdg-open`. Failure to launch a browser is diagnostic-only while the
URL remains available for manual use. Cancellation, CLI disconnect, SIGINT,
or daemon shutdown cancels the flow and closes the listener.

Credential records are globally reusable for the same resolved endpoint and
configured registration inputs. The pinned SDK does not expose a separate
stable resource/issuer identity at the storage boundary, so this slice uses a
versioned resolved endpoint identity together with registration mode, client
ID, and resolved client secret. This is an explicit SDK exposure limitation,
not a claim that endpoint, resource, and issuer are semantically identical.
If a real provider demonstrates a collision or requires a distinct identity,
record the evidence and deliberate the smallest boundary change before adding
a shim.

Wirecmd stores only a random 256-bit encryption master key in the native
keyring through `github.com/zalando/go-keyring`. Versioned OAuth records are
encrypted with authenticated encryption in the private XDG state directory.
Directories are `0700`, files are `0600`, and state uses locking, ownership
and symlink checks, and atomic replacement. There is no plaintext,
environment-key, or insecure fallback. Keyring unavailability is an explicit
`credential_store_unavailable` user-action failure (exit 8).

All resolved secrets, tokens, authorization codes, client secrets, and
secret-bearing URLs are redacted from SDK errors, callback diagnostics,
daemon stderr, and any structured output. State filenames use opaque derived
identifiers rather than endpoint or secret text.

## Error categories

- `authorization_required`: interactive authorization is disabled (exit 4).
- `authorization_failed`: provider denial, rejected credentials, or refresh
  failure (exit 4).
- `authorization_in_progress`: another flow owns the identity (exit 8).
- `credential_store_unavailable` or `credential_store_unsafe`: native keyring
  or protected state requires user action (exit 8).
- Invalid OAuth configuration is a configuration error (exit 3).
- Malformed or unsupported upstream OAuth metadata is an upstream protocol
  error (exit 6).

## Qualification and acceptance

The milestone must qualify, with local SDK-backed fixtures, dynamic
registration, preregistered public and confidential clients, PKCE, metadata
fallback, refresh persistence, denial, malformed metadata, callback
validation, cancellation, daemon restart, direct/daemon shared credentials,
logout, concurrent-flow rejection, and credential-store failure handling.

It must also verify that static-Authorization and unauthenticated HTTP paths do
not require OAuth credentials, that no known secret appears in captured
stdout/stderr or state paths, and that direct and daemon operations preserve
the public result and error contract. Where provider credentials are
available, qualify one preregistered service (such as Figma) and one dynamic
registration service. Provider quirks are evidence for a later decision, not
permission for an ad hoc compatibility layer.

Run the repository's full tests, race tests, vet, module verification, build
checks, diff checks, and an independent security/concurrency review. Record
real-provider qualification separately without committing credentials or
tokens.

## Deferred work

- Client ID Metadata Documents and provider-specific compatibility guards.
- Provider-side token revocation and device authorization.
- Client-credentials flow, multiple accounts per identity, and custom scope
  policy.
- Remote/headless callback relays, custom browser commands, and non-loopback
  redirect URIs.
- Physical macOS Keychain and browser qualification, tracked by the
  [macOS plan](macos-plan.md), plus platforms beyond Linux and macOS.
- OAuth behavior that requires a stable SDK-exposed resource/issuer identity
  beyond the resolved endpoint identity described above.
