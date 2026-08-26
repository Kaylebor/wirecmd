# OAuth qualification evidence, 2026-08-26

Status: non-authoritative implementation evidence.

This run exercised the uncommitted transparent-OAuth milestone against the
managed Cloudflare MCP endpoint and the hosted Figma MCP endpoint. The
qualification used a temporary binary and explicit KDL configuration outside
the repository. No authorization URL, callback query, code, token, client
secret, keyring value, or encrypted record was captured in this file.

## Configuration and startup

The temporary configuration contained two workspace-scoped Streamable HTTP
servers:

```kdl
wirecmd {
    server "cloudflare" {
        scope "workspace"
        http "https://mcp.cloudflare.com/mcp"
    }

    server "figma" {
        scope "workspace"
        http "https://mcp.figma.com/mcp"
    }
}
```

The binary was built from the working tree and a foreground daemon was started
with:

```sh
wirecmd daemon run
```

Listing the explicit configuration returned the two aliases without starting
either upstream connection.

## Cloudflare authorization and persistence

The human caller ran:

```sh
wirecmd --config CONFIG auth login cloudflare
wirecmd --config CONFIG auth status cloudflare
```

Wirecmd dynamically registered the client, emitted the authorization handoff
to stderr, opened the browser, waited for the loopback callback, and returned
one successful final stdout envelope. The user explicitly accepted the
provider consent screen; neither Wirecmd nor the testing agent automated that
consent. Local status reported `authenticated` with registration `dynamic` and
an expiry, without contacting the provider or printing credentials.

Credential reuse was then observed through:

- a separate daemon-backed CLI process;
- a separate `--direct` process;
- a newly started daemon after stopping the original foreground daemon; and
- subsequent tool discovery and calls without another browser flow.

The fresh daemon successfully called the Cloudflare `docs` tool, establishing
encrypted credential persistence across daemon restart. The semantic docs
query was noticeably slower than local listing and focused help, but completed
successfully; this run did not collect enough repeated latency samples to
attribute the delay between Wirecmd and the upstream search service.

## Agent-facing tool trial

A Luna worker received only the compiled executable, explicit config, desired
read-only outcome, and a prohibition against the Cloudflare `execute` tool. It
used both direct and daemon-backed modes and completed:

- server tool discovery, finding `docs`, `search`, and `execute`;
- focused help for `docs` and `search`;
- a projected call with `docs --query ...`;
- an exact-JSON call to the same `docs` capability;
- a bounded read-only OpenAPI query through `search`; and
- a shell pipeline from Wirecmd JSON through `jq`, `rg`, and `head`.

Both projected and exact-JSON docs calls returned nine results. The composed
pipeline selected five relevant result titles. The Cloudflare `docs` response
provided structured data under `result.data`, while `search` returned a JSON
string in `result.messages`; the latter required a second `fromjson`-style
decode for structural shell processing. This is an upstream result-shape UX
observation, not a Wirecmd protocol failure.

The first sandboxed socket attempts reported daemon or callback unavailability
because the harness blocked Unix and loopback sockets. Retrying the exact
commands with host permission made both daemon-backed and direct operations
succeed. Those initial results are sandbox artifacts and must not be treated as
Wirecmd failures.

## Agent-facing logout and recovery

A second Luna worker exercised the credential recovery path with authorization
to delete only Wirecmd's local Cloudflare record:

1. daemon-backed status reported `authenticated`;
2. `auth logout cloudflare` succeeded;
3. local status reported `unauthenticated`;
4. a docs call with `WIRECMD_NONINTERACTIVE=1` returned exit 4,
   `authentication/authorization_required`, and the exact login action;
5. the agent initiated `auth login cloudflare` in a real PTY;
6. Wirecmd opened the browser and waited while the human explicitly approved
   provider consent;
7. the callback completed and final status returned `authenticated`; and
8. a harmless docs call succeeded.

The agent did not inspect or automate the browser, expose the authorization
URL, invoke `execute`, or mutate provider resources. This establishes the
intended human-in-the-loop boundary: a non-interactive agent receives a
structured recovery action, while an interactive login can hand control to the
human browser and resume only after the callback.

## Figma compatibility observation

The same implicit OAuth path against `https://mcp.figma.com/mcp` reached
dynamic client registration but the provider returned `403 Forbidden`:

```json
{"ok":false,"error":{"category":"upstream_protocol","code":"oauth_protocol_failed","message":"... failed to register client: registration failed with status 403 Forbidden: Forbidden","action":"check the upstream OAuth metadata and client registration"}}
```

This is consistent with Figma's documented restriction to clients in its MCP
catalog. It is provider compatibility evidence, not authorization to add a
local OAuth shim. Qualification would require Wirecmd client approval or
provider-issued preregistration details.

## Local store and automated checks

The Linux Secret Service environment was qualified with a disposable random
Wirecmd value using native keyring set, get, and delete operations. Only the
success state was recorded; key material was not printed or retained.

Automated qualification passed:

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go mod verify
go build ./...
git diff --check
```

An independent concurrency and security review found and prompted fixes for
stale token-save resurrection, encoded authorization-URL redaction,
authorization-lock ordering, credential-specific daemon retirement, and
cross-process direct/daemon authorization serialization. The corrected code
and focused race tests passed, and the final review reported no actionable
findings.
