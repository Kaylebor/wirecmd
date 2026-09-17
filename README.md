# Wire Command (`wirecmd`)

Wirecmd makes configured tools available through a predictable shell interface
for agents, humans, scripts, and CI. It discovers capabilities only when they
are needed, keeps stdout composable, and lets ordinary shell tooling remain the
integration layer.

```text
agent / human / automation
          |
          v
        shell
          |
          v
       wirecmd
          |
          v
  MCP servers and language servers
```

Wirecmd currently provides:

- MCP tools and resources over stdio, Streamable HTTP, and legacy HTTP+SSE;
- SDK-backed OAuth for protected Streamable HTTP servers;
- native, selector-routed LSP navigation and read-only inspection;
- KDL 2 configuration with automatic discovery, workspace trust, and optional
  age-backed secret stores; and
- a foreground local daemon that retains upstream sessions between commands.

Normal commands use the daemon and fail clearly when it is unavailable.
`--direct` is the explicit one-shot path for testing and diagnosis; Wirecmd
never falls back to it silently.

## Install

Wirecmd requires Go 1.26 or newer. Install the latest release with:

```sh
go install github.com/Kaylebor/wirecmd@latest
wirecmd --version
```

To install v0.4.0 exactly:

```sh
go install github.com/Kaylebor/wirecmd@v0.4.0
```

Linux and macOS are supported; see [release readiness](docs/release-readiness.md)
for the qualification boundary and runtime prerequisites.

## First call

Save a minimal workspace configuration as `.wirecmd/config.kdl`, replacing the
command with an installed stdio MCP server executable and its actual arguments:

```sh
mkdir -p .wirecmd
```

```kdl
wirecmd {
    mcp "local" {
        stdio "/absolute/path/to/mcp-server"
    }
}
```

Trust the workspace once before automatic discovery can use its configuration:

```sh
wirecmd config trust .
```

For a locally buildable example with `set_value` and `read_value` tools, follow
the [test fixture instructions](testdata/legacy-mcp/README.md) and use the
fixture's absolute executable path.

Start the foreground daemon in one terminal:

```sh
wirecmd daemon run
```

On Linux, `XDG_RUNTIME_DIR` must be an absolute, private, same-user directory;
the login session normally configures it.

Then discover the configured server and invoke its tools from another:

```sh
wirecmd mcp
wirecmd mcp local
wirecmd --help mcp local tool set_value
wirecmd mcp local tool set_value --value hello
wirecmd mcp local tool read_value
```

The daemon retains initialized sessions, so the example value survives across
separate CLI invocations. Use the server's discovered tool names and focused
help for other configurations.

## Configuration

Wirecmd can compose global configuration with trusted `.wirecmd/config.kdl`
files in the current workspace. Repeated `--config PATH` options instead
provide the complete ordered source list and bypass automatic discovery and
trust checks; an explicit path may use any filename.
After changing configuration used by the daemon, run:

```sh
wirecmd daemon reload
```

An MCP definition selects exactly one of stdio, Streamable HTTP, or legacy
HTTP+SSE. LSP definitions supply their executable, arguments, environment,
language IDs, and selectors. Wirecmd resolves one project root from an explicit
`root`, the nearest `.wirecmd/config.kdl`, and optional Git discovery; it does
not infer or install language servers. MCP and LSP definitions default to
`workspace` scope, so `scope "workspace"` is optional. Use `scope "global"`
for a provider whose lifecycle and default root belong to Wirecmd's global
configuration directory rather than the current project. Global HTTP providers
can be context-free; global stdio MCP and LSP providers require that directory.

Provider inputs can explicitly materialize call context with `(template)` and
`${wirecmd.cwd}`, `${wirecmd.project-root}`, or `${wirecmd.global-root}`. This
works in stdio executable, argument, and environment values, HTTP/SSE and OAuth
values, and the corresponding LSP stdio fields. Templates are single-pass and
do not interpolate secrets or change Wirecmd control fields. See the
[configuration reference](docs/configuration.md#context-templates) for the
exact syntax and lifecycle effects.

See the [configuration reference](docs/configuration.md) for the complete KDL
surface, discovery and trust rules, transports, secrets, OAuth, and LSP setup.

## Using Wirecmd

Start with the built-in help:

```sh
wirecmd --help
wirecmd --help daemon
wirecmd --help config
wirecmd --help auth
wirecmd --help lsp
```

Administrative and LSP help is offline. MCP server and focused tool help uses
live upstream metadata, so it requires the daemon or an explicit `--direct`
invocation.

Terminal output is designed for reading; piped output is compact JSON. Agents
using a PTY can request deterministic machine presentation with
`--format json --color never`. Diagnostics go to stderr, and structured errors
include a recovery action when one is available.

`go install` installs only the binary. From a checkout, install Fish completion
manually:

```fish
mkdir -p ~/.config/fish/completions
cp completions/wirecmd.fish ~/.config/fish/completions/wirecmd.fish
```

The completion reads local configuration only and never starts a server,
contacts the daemon, resolves secrets, or opens OAuth.

## Learn more

- [Documentation index](docs/README.md): reading paths for users, contributors,
  and maintainers.
- [Configuration reference](docs/configuration.md): practical setup and exact
  configuration behavior.
- [Age secrets](docs/plans/age-secrets-plan.md): encrypted secret stores and their
  security boundary.
- [Product thesis](docs/product-thesis.md): product direction and boundaries.
- [Current roadmap](docs/roadmap.md): shipped baseline and open decision queue.
- [Development guide](docs/development.md): contributor workflow, verification,
  review, and change hygiene.
- [LSP plan](docs/plans/lsp-plan.md): native navigation and inspection contract.
- [Scope and context plan](docs/plans/scope-context-plan.md): project-root
  resolution, workspace and global ownership, and scope-separated secrets.
- [Context templates and LSP initialization options](docs/plans/context-templates-plan.md):
  explicit context materialization and the accepted initialization JSON contract.
- [Output contract](docs/plans/output-plan.md): terminal, JSON, and color behavior.
- [Onboarding and completion](docs/plans/onboarding-plan.md): help and Fish
  completion contracts.
- [Release readiness](docs/release-readiness.md): installation, compatibility,
  qualification, and publication policy.

Agents should start with the repository primer in [AGENTS.md](AGENTS.md);
contributor and maintainer procedures are in the
[development guide](docs/development.md).

## License

Wirecmd is licensed under the [Apache License 2.0](LICENSE).
