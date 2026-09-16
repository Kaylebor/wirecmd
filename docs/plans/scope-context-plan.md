# Scope and invocation context plan

Status: accepted and implemented

## Purpose

Wirecmd resolves one invocation context before starting a configured
capability. Configuration provenance, caller context, instance scope,
materialized provider root, authentication identity, and retained-instance
identity remain separate concepts. MCP and LSP consume the same resolved
context rather than independently interpreting the caller directory.

This plan deliberately does not introduce public context references,
templating, generated files, arbitrary structured values, or broader LSP
configuration.

## Invocation context

The internal context records the caller CWD, canonical CWD, project root,
global Wirecmd root, trusted discovery boundary, discovery mode, and ordered
canonical configuration sources. It crosses the private CLI/daemon boundary;
the daemon validates normalized paths but preserves roots resolved by the
caller because Git and environment context can differ.

The global root is `$XDG_CONFIG_HOME/wirecmd` when `XDG_CONFIG_HOME` is an
absolute path, otherwise `~/.config/wirecmd` on Linux and macOS. Wirecmd
canonicalizes it when present and never creates it as a side effect of context
resolution.

Parsed configuration is cached by daemon generation, discovery mode, and
ordered canonical source paths. Caller CWD is invocation context, not static
configuration identity.

## Project-root resolution

An optional top-level KDL 2 setting controls whether Git participates:

```kdl
wirecmd {
    git-root #false
}
```

It defaults to `#true` and composes strongest-wins. Configuration is loaded
before Git is considered, so an effective `git-root #false` guarantees that
Wirecmd does not launch Git.

A declared `root` wins unconditionally, including when it is outside the
caller CWD. Under automatic discovery, only a `root` declared by the nearest
workspace configuration applies; weaker outer-workspace or global roots do
not leak into that workspace. With global-only discovery, the global source's
root applies. With explicit `--config` paths, the strongest declared root
applies.

Without a declared root, Wirecmd independently considers:

- the directory containing the nearest discovered `.wirecmd/config.kdl`; and
- the current Git worktree root when Git participation is enabled.

Both candidates are canonicalized and must contain the canonical caller CWD.
The deepest valid ancestor wins. Equal candidates are equivalent. If neither
exists, automatic discovery falls back to the nearest trusted boundary and
then to the canonical CWD.

Git is invoked directly from `PATH`, without a shell, as
`git -C CWD rev-parse --show-toplevel`. It inherits Git environment overrides,
but an unrelated result is discarded. The probe is bounded to five seconds;
missing Git, timeouts, non-worktrees, invalid output, and stderr all produce a
quiet fallback. Submodules therefore use their own reported worktree root.

## Workspace scope

Omitted scope remains `workspace`. Its owner and default provider root are the
canonical project root. MCP stdio CWD, LSP selector base and initialization
root, daemon pool ownership, execution fingerprints, age metadata, and LSP
runtime status use that same value. Calls from sibling directories that
resolve to one project reuse eligible retained instances; distinct project
roots remain isolated.

Static help, completion, and MCP server listing do not resolve project context,
probe Git, discover secrets, decrypt stores, or start providers. Direct mode
remains fresh and normal operation remains daemon-required.

## Global scope

`scope "global"` is accepted for MCP and LSP while omitted scope remains
`workspace`. Scope controls lifecycle ownership and the default provider root,
never configuration provenance.

Global stdio and LSP providers use the global Wirecmd root and return
`global_root_unavailable` when it is absent or not a directory. Context-free
global HTTP providers do not require that directory. Equivalent global
providers reuse across projects. Global LSP remains rooted at the global root;
it does not silently attach one process to arbitrary project workspaces.

Workspace age resolution keeps trusted workspace-to-global precedence. Global
age resolution reads only the global store. Batches and retained metadata stay
scope-separated and plaintext-free, while mixed-scope LSP fan-out decrypts
each unique store at most once per operation.

## Qualification

Tests cover root candidates in both nesting orders, explicit and ignored
weaker roots, global-only and explicit discovery, Git enablement and failure,
unrelated Git environment results, submodules, timeouts, trust fallback,
ties, symlink aliases, sibling-CWD reuse, MCP/LSP agreement, daemon caching,
and static-command non-interaction. The global slice adds cross-project reuse,
scope-separated secrets, global LSP behavior, and identity-splitting tests.
