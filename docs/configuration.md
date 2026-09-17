# Configuration reference

Wirecmd uses KDL 2 for user and workspace configuration. This page is the
practical reference for the currently implemented configuration surface. The
milestone documents linked at the end remain authoritative for design and
security boundaries.

## Complete example

```kdl
wirecmd {
    root "."
    git-root #true

    secrets {
        age {
            identity "/absolute/path/to/hardware-backed.identity"
        }
    }

    mcp "local" {
        stdio "local-mcp-server" {
            arg "--stdio"
            env LOG_LEVEL="warn"
            env API_TOKEN=(secret)"age://LOCAL_MCP_TOKEN"
        }
    }

    mcp "remote" {
        http "https://example.test/mcp" {
            query tenant="example"
            header X-API-Key=(secret)"age://REMOTE_API_KEY"
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
2. `.wirecmd/config.kdl` files from the nearest trusted workspace root down to
   the caller's current directory.

Trust a workspace before its discovered configuration can execute commands:

```sh
wirecmd config trust /path/to/workspace
wirecmd config untrust /path/to/workspace
wirecmd config trust status /path/to/workspace
wirecmd config trust list
```

Trust is recursive. The nearest trusted ancestor is the discovery boundary;
workspace files above it are ignored. A discovered project configuration with
no matching trust root fails closed. The global user configuration is
inherently trusted.

Automatic discovery accepts only regular, non-symlink workspace configuration
files, and every discovered workspace `.wirecmd` component must be a real
directory, not a symlink. Wirecmd does not inspect or fall back to a top-level
`wirecmd.kdl`.

Repeated explicit paths replace discovery and trust evaluation completely:

```sh
wirecmd --config /path/to/base.kdl --config ./local.kdl mcp
```

Paths are composed in command-line order, weakest first. After changing an
effective configuration used by the daemon, run `wirecmd daemon reload`.
An explicit path may have any filename; only automatic workspace discovery uses
the `.wirecmd/config.kdl` name.

## Project root and scope behavior

`root` is optional. A relative root resolves against the file that declared
the applicable value. Under automatic discovery, only the nearest workspace
configuration may declare the project root; a weaker global or outer-workspace
root does not leak into it. Global-only discovery uses the global declaration,
while explicit `--config` paths use the strongest declaration.

```kdl
wirecmd {
    root ".."
}
```

Without a declared root, Wirecmd compares the directory containing the nearest
discovered `.wirecmd/config.kdl` with the Git worktree root and chooses the
deepest valid ancestor of the caller's canonical CWD. It then falls back to the
nearest trusted boundary and finally the CWD. The canonical project root owns
the `workspace` scope: its stdio process CWD, LSP selector and initialization
root, and retained-instance boundary.

Git participation is optional and defaults on. Disable it in a stronger
configuration layer with KDL 2 boolean syntax:

```kdl
wirecmd {
    git-root #false
}
```

When disabled, Wirecmd does not launch Git. Otherwise Git is only a project
signal; missing Git, non-worktrees, invalid or unrelated results, and a
five-second timeout quietly fall back to the other candidates.

MCP and LSP definitions default to `workspace` scope. Writing
`scope "workspace"` explicitly is also valid. `scope "global"` is also
available and controls lifecycle ownership and the default provider root; it
does not determine where a definition was configured. A global definition may
be declared or overridden in global, workspace, or explicit configuration.

The global root is `$XDG_CONFIG_HOME/wirecmd`, or `~/.config/wirecmd` when
`XDG_CONFIG_HOME` is unset or not absolute. Global stdio MCP and LSP providers
start from that directory; they return `global_root_unavailable` if it is
absent or is not a directory. Context-free global HTTP and SSE MCP providers
do not require the directory. Equivalent global providers can reuse a retained
instance across projects. Global LSP selectors, process CWD, and initialization
remain rooted at the global root and never attach a retained server to an
arbitrary caller project.

For example, a reusable global HTTP provider can be defined in any effective
configuration source:

```kdl
wirecmd {
    mcp "account-service" {
        scope "global"
        http "https://service.example.test/mcp"
    }
}
```

Global `age://` references read only the global encrypted store. Workspace
references retain the trusted workspace-to-global store lookup order. Mixed
scope operations keep their secret batches and retained metadata separate.

## Context templates

Provider-consumed strings can opt into three invocation-context references:

- `${wirecmd.cwd}`: the canonical directory of this Wirecmd call;
- `${wirecmd.project-root}`: the resolved canonical project root; and
- `${wirecmd.global-root}`: Wirecmd's resolved global configuration path,
  whether or not that directory currently exists.

Use the `(template)` annotation explicitly:

```kdl
wirecmd {
    mcp "project-aware" {
        stdio (template)"${wirecmd.global-root}/bin/server" {
            arg (template)"--workspace=${wirecmd.project-root}"
            env CALLER_DIR=(template)"${wirecmd.cwd}"
        }
    }
}
```

`$$` produces one literal `$`. Expansion is single-pass: text introduced by a
resolved value is never expanded again. Unknown or malformed references and
templates with no references are configuration errors. `(template)` and
`(secret)` cannot be combined.

Templates are supported for MCP and LSP stdio executables, arguments, and
environment values; HTTP and SSE endpoints, query values, and header values;
and OAuth client IDs, client secrets, and redirect URIs. They are deliberately
not supported in Wirecmd control fields such as names, scope, `root`,
`git-root`, selectors, implementation metadata, or age identity paths.

Materialized values are validated like equivalent literals. They also
participate in retained-instance identity: a `${wirecmd.cwd}` or
`${wirecmd.project-root}` reference may deliberately split otherwise-global
instances when its resolved value differs. Static listing and completion do
not expand templates; `lsp status` resolves only the executable it reports and
does not start the provider.

## MCP definitions

An MCP definition is named with `mcp "NAME"` and must select exactly one
transport. The name is the `SERVER` used by `wirecmd mcp SERVER ...`.

### Standard input and output

```kdl
mcp "local" {
    stdio "executable" {
        arg "first-argument"
        arg (secret)"age://SECRET_ARGUMENT"
        env MODE="local"
        env TOKEN=(secret)"age://SERVER_TOKEN"
    }
}
```

In direct mode, the child inherits the invoking CLI process environment. In
daemon-backed mode, it inherits the environment from when the daemon was
started. Configured `env` entries are then applied. The CLI supplies selected
`env://` values privately to the daemon, while the daemon resolves selected
`age://` values locally. A present-but-empty value remains distinct from an
absent value. Arguments retain their configured order. The same inheritance
boundary applies to LSP stdio processes.

### Streamable HTTP

```kdl
mcp "remote" {
    http "https://example.test/mcp?existing=value" {
        query tenant="acme"
        query token=(secret)"age://QUERY_TOKEN"
        header X-API-Key=(secret)"age://API_KEY"
        header Authorization=(secret)"env://AUTHORIZATION"
    }
}
```

Endpoints must be absolute HTTP or HTTPS URLs with a host and no user
information or fragment. Query names are case-sensitive; header names are
case-insensitive. Structural query entries replace matching endpoint query
keys. `Authorization` values are complete header values—Wirecmd does not add a
scheme. Transport-owned HTTP and MCP headers cannot be configured.
Header values must be valid UTF-8 and cannot contain CR or LF. The reserved
HTTP names are `Host`, `Content-Length`, `Content-Type`, `Accept`, `Connection`,
`Transfer-Encoding`, `Trailer`, `Upgrade`, and `Proxy-Connection`. The reserved
MCP/session names are `Mcp-Protocol-Version`, `Mcp-Session-Id`, `Mcp-Method`,
`Mcp-Name`, `Mcp-Param-*`, and `Last-Event-ID`; matching is case-insensitive.

Without a configured `Authorization` header, a protected Streamable HTTP
server may use the SDK-backed OAuth flow automatically. Most providers use
dynamic client registration and need no OAuth configuration. For a
preregistered client:

```kdl
mcp "remote" {
    http "https://example.test/mcp" {
        oauth {
            client-id "wirecmd-client"
            client-secret (secret)"age://OAUTH_CLIENT_SECRET"
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
        header Authorization=(secret)"age://LEGACY_AUTHORIZATION"
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
provider-root-relative doublestar glob and defaults to `**/*`.
`implementation-id` is optional descriptive metadata; negotiated server
identity and capabilities still come from LSP initialization. Multiple
definitions may match one file and are queried according to their advertised
capabilities. A global LSP definition matches only files beneath the global
root; it does not treat the invocation project as its workspace.

Wirecmd does not derive language IDs, executable names, launch arguments, or
initialization options.

## Secret references

Textual destination values are literals unless annotated as a context template
or secret. Secret references use:

```kdl
env PUBLIC_MODE="development"
env ACCESS_TOKEN=(secret)"env://ACCESS_TOKEN"
```

Wirecmd has two built-in secret schemes: `env://NAME` and `age://NAME`. The
scheme name is case-insensitive; `age` names must match
`[A-Za-z_][A-Za-z0-9_.-]*`. `env://NAME` reads only the invoking CLI
environment, preserving the distinction between an unset and an empty value.
`age://NAME` resolves from encrypted stores described below. Only references
needed by the selected MCP or LSP definition are resolved; static listing,
completion, and LSP status do no secret discovery or resolution. Resolved
values are redacted from Wirecmd-controlled output and never included as
plaintext in configuration fingerprints.

Configure the fixed external `age` executable with one or more absolute,
user-managed identity paths. A non-empty stronger identity list replaces the
weaker list during composition. An empty stronger `age` block inherits the
weaker identity list rather than erasing it:

```kdl
wirecmd {
    secrets {
        age {
            identity "/absolute/path/to/first.identity"
            identity "/absolute/path/to/second.identity"
        }
    }
}
```

`age` is optional unless an `age://` reference is selected. For the complete
store format, lookup order, hardware-prompt behavior, and threat boundary, see
the [age secrets plan](plans/age-secrets-plan.md).

## Composition rules

Each file is parsed as a partial source, then the ordered sources are composed
and the effective result is validated:

- scalar fields use the strongest value that is present;
- MCP and LSP definitions merge by configured name;
- environment and query entries merge by case-sensitive key; header entries
  merge case-insensitively. An override retains its position and a new entry
  appends in source order;
- a non-empty stronger argument list replaces the inherited list completely;
- a non-empty stronger selector list replaces inherited selectors completely;
- switching `stdio`, `http`, or `sse` replaces the entire prior transport and
  does not inherit endpoint values or credentials;
- winning values retain file and semantic-path provenance for diagnostics.

There are no tombstones, includes, explicit-empty argument replacement,
generic templates, or arbitrary secret-provider commands. A source can inherit
omitted fields, but the final effective configuration must be complete.

For the underlying decisions and security boundaries, see the
[discovery](plans/discovery-plan.md), [HTTP values](plans/http-values-plan.md),
[OAuth](plans/oauth-plan.md), [age secrets](plans/age-secrets-plan.md), [SSE](plans/sse-plan.md),
and [LSP](plans/lsp-plan.md) milestones.
