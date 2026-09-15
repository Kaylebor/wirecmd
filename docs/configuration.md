# Configuration reference

Wirecmd uses KDL 2 for user and workspace configuration. This page is the
practical reference for the currently implemented configuration surface. The
milestone documents linked at the end remain authoritative for design and
security boundaries.

## Complete example

```kdl
wirecmd {
    root "."

    mcp "local" {
        stdio "local-mcp-server" {
            arg "--stdio"
            env LOG_LEVEL="warn"
            env API_TOKEN=(secret)"env://LOCAL_MCP_TOKEN"
        }
    }

    mcp "remote" {
        http "https://example.test/mcp" {
            query tenant="example"
            header X-API-Key=(secret)"env://REMOTE_API_KEY"
        }
    }

    mcp "legacy-events" {
        sse "https://example.test/sse" {
            header Authorization=(secret)"env://LEGACY_AUTHORIZATION"
        }
    }

    lsp "primary" {
        implementation-id "optional-human-readable-identity"
        selector language-id="example" pattern="src/**/*.example"
        stdio "example-language-server" {
            arg "serve"
        }
    }
}
```

Executable names, arguments, endpoints, language IDs, and selectors above are
illustrative. Wirecmd does not infer or install MCP or language servers.

## Files, discovery, and trust

Without `--config`, Wirecmd composes these files from weakest to strongest:

1. `$XDG_CONFIG_HOME/wirecmd/config.kdl`, or
   `~/.config/wirecmd/config.kdl` when `XDG_CONFIG_HOME` is unset or not
   absolute;
2. `wirecmd.kdl` files from the nearest trusted workspace root down to the
   caller's current directory.

Trust a workspace before its discovered configuration can execute commands:

```sh
wirecmd config trust /path/to/workspace
wirecmd config trust status /path/to/workspace
```

Trust is recursive. The nearest trusted ancestor is the discovery boundary;
workspace files above it are ignored. A discovered project configuration with
no matching trust root fails closed. The global user configuration is
inherently trusted.

Repeated explicit paths replace discovery and trust evaluation completely:

```sh
wirecmd --config /path/to/base.kdl --config ./local.kdl mcp
```

Paths are composed in command-line order, weakest first. After changing an
effective configuration used by the daemon, run `wirecmd daemon reload`.

## Root and workspace behavior

`root` is optional. A relative root resolves against the file that declared
the winning value. Without one, Wirecmd uses the caller's current directory.
The effective absolute root identifies the workspace for daemon session
pooling and LSP file routing.

```kdl
wirecmd {
    root ".."
}
```

MCP and LSP definitions default to workspace scope. Writing `scope "workspace"`
explicitly is also valid. No other scope is implemented.

## MCP definitions

An MCP definition is named with `mcp "NAME"` and must select exactly one
transport. The name is the `SERVER` used by `wirecmd mcp SERVER ...`.

### Standard input and output

```kdl
mcp "local" {
    stdio "executable" {
        arg "first-argument"
        arg (secret)"env://SECRET_ARGUMENT"
        env MODE="local"
        env TOKEN=(secret)"env://SERVER_TOKEN"
    }
}
```

Wirecmd inherits the caller environment, then applies configured `env`
entries. A present-but-empty value remains distinct from an absent value.
Arguments retain their configured order.

### Streamable HTTP

```kdl
mcp "remote" {
    http "https://example.test/mcp?existing=value" {
        query tenant="acme"
        query token=(secret)"env://QUERY_TOKEN"
        header X-API-Key=(secret)"env://API_KEY"
        header Authorization=(secret)"env://AUTHORIZATION"
    }
}
```

Endpoints must be absolute HTTP or HTTPS URLs with a host and no user
information or fragment. Query names are case-sensitive; header names are
case-insensitive. Structural query entries replace matching endpoint query
keys. `Authorization` values are complete header values—Wirecmd does not add a
scheme. Transport-owned HTTP and MCP headers cannot be configured.

Without a configured `Authorization` header, a protected Streamable HTTP
server may use the SDK-backed OAuth flow automatically. Most providers use
dynamic client registration and need no OAuth configuration. For a
preregistered client:

```kdl
mcp "remote" {
    http "https://example.test/mcp" {
        oauth {
            client-id "wirecmd-client"
            client-secret (secret)"env://OAUTH_CLIENT_SECRET"
            redirect-uri "http://127.0.0.1:8765/callback"
        }
    }
}
```

`client-id` and `redirect-uri` are required when `oauth` is present;
`client-secret` is optional. Redirect URIs must be exact HTTP loopback URLs on
`127.0.0.1`, `::1`, or `localhost`, with an explicit port and no user
information or fragment. An `oauth` block and an `Authorization` header are
mutually exclusive.

### Legacy HTTP+SSE

```kdl
mcp "legacy" {
    sse "https://example.test/sse" {
        query tenant="acme"
        header Authorization=(secret)"env://LEGACY_AUTHORIZATION"
    }
}
```

The `sse` transport supports the same structural query and header entries as
`http`, including static authentication. Transparent OAuth and `oauth` blocks
are not supported for SSE. Wirecmd never automatically switches between
Streamable HTTP and SSE.

## LSP definitions

```kdl
lsp "typescript" {
    implementation-id "optional-metadata"
    selector language-id="typescript" pattern="**/*.ts"
    selector language-id="typescriptreact" pattern="**/*.tsx"
    stdio "language-server" {
        arg "--stdio"
        env SERVER_MODE="workspace"
    }
}
```

Every LSP definition requires a `stdio` executable and at least one selector.
`language-id` is required on each selector. `pattern` is an optional
workspace-relative doublestar glob and defaults to `**/*`.
`implementation-id` is optional descriptive metadata; negotiated server
identity and capabilities still come from LSP initialization. Multiple
definitions may match one file and are queried according to their advertised
capabilities.

Wirecmd does not derive language IDs, executable names, launch arguments, or
initialization options.

## Secret references

Textual destination values are literals unless annotated as a secret:

```kdl
env PUBLIC_MODE="development"
env ACCESS_TOKEN=(secret)"env://ACCESS_TOKEN"
```

`env://NAME` is the only implemented secret provider. Only references needed
by the selected MCP or LSP definition are resolved. An unset variable is an
error; a variable set to an empty string is present. Resolved values are
redacted from Wirecmd-controlled output and never included as plaintext in
configuration fingerprints.

## Composition rules

Each file is parsed as a partial source, then the ordered sources are composed
and the effective result is validated:

- scalar fields use the strongest value that is present;
- MCP and LSP definitions merge by configured name;
- environment, query, and header entries merge by key; an override retains
  its position and a new entry appends in source order;
- a non-empty stronger argument list replaces the inherited list completely;
- a non-empty stronger selector list replaces inherited selectors completely;
- switching `stdio`, `http`, or `sse` replaces the entire prior transport and
  does not inherit endpoint values or credentials;
- winning values retain file and semantic-path provenance for diagnostics.

There are no tombstones, includes, explicit-empty argument replacement, or
generic templates. A source can inherit omitted fields, but the final effective
configuration must be complete.

For the underlying decisions and security boundaries, see the
[discovery](discovery-plan.md), [HTTP values](http-values-plan.md),
[OAuth](oauth-plan.md), [SSE](sse-plan.md), and [LSP](lsp-plan.md) milestones.
