# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and releases use [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- A shared invocation-context model that resolves one canonical project root
  from explicit configuration, the nearest workspace configuration, optional
  Git discovery, and trusted fallbacks. The new top-level `git-root` setting
  can disable Git discovery.
- Global scope for MCP and LSP definitions, with global-root defaults,
  cross-project retained reuse, explicit LSP status, and scope-separated age
  secret resolution. Omitted scope continues to default to `workspace`.
- Explicit context templates for provider inputs, exposing canonical caller
  CWD, project root, and global Wirecmd root without introducing a general
  template language or secret interpolation.
- Arbitrary strict JSON LSP initialization options, including explicit `null`,
  whole-document composition, and context expansion limited to JSON string
  values.

### Changed

- Workspace-scoped MCP and LSP startup, LSP selection and initialization,
  retained ownership, fingerprints, and age metadata now consistently use the
  resolved canonical project root.
- Expanded and reorganized the documentation around a dedicated index,
  contributor workflow, product thesis, configuration reference, accepted
  plans, and non-authoritative working notes.
- Updated the contributor workflow so ordinary changes reach merge-only
  `main` through reviewed pull requests and every non-release PR maintains its
  own `[Unreleased]` changelog entry.

## [0.4.0] - 2026-09-15

### Added

- Age-encrypted secret files, with optional hardware-backed identities, for MCP
  and LSP arguments and environment, HTTP query and header values, and OAuth
  client secrets, with scoped batch resolution, strict store validation,
  redaction, and daemon reuse.
- A closed internal secret-provider registry for the built-in `env` and `age`
  schemes.

### Changed

- Workspace configuration discovery now uses `.wirecmd/config.kdl`; automatic
  discovery no longer considers a top-level `wirecmd.kdl`.
- Reworked the README as a concise public introduction, with detailed
  configuration and product contracts kept in their dedicated documentation.
- Restructured top-level command help around the public command families,
  clarified prefix-flag placement, and improved MCP resource discoverability.

### Fixed

- Rejected symlinked or replaced workspace metadata directories during
  automatic configuration and age-store discovery, and rejected non-regular
  age stores without blocking on FIFOs.
- Prevented age identity changes from invalidating retained MCP or LSP
  executions that do not use age-backed secrets.

## [0.3.1] - 2026-09-15

### Changed

- MCP definitions now default an omitted `scope` to `workspace`, matching the
  existing LSP default while preserving explicit scope configuration.

## [0.3.0] - 2026-09-14

### Added

- Legacy HTTP+SSE MCP transport through the official SDK, including typed
  query, header, and static authorization values in direct and retained daemon
  sessions. Transparent OAuth for legacy SSE remains deferred.
- A consolidated KDL configuration reference covering composition, discovery,
  MCP transports, OAuth, and native LSP providers.

### Changed

- Upgraded the official MCP Go SDK to v1.8.0 and extended newest-to-oldest
  compatibility qualification to the supported legacy HTTP+SSE transport.

## [0.2.0] - 2026-09-10

### Added

- Native LSP signature help through the existing selector-routed direct and
  retained daemon sessions.
- MCP resource discovery, resource-template discovery, and resource reads,
  including deterministic JSON and terminal-aware output.

### Changed

- MCP operations now use the explicit `wirecmd mcp` namespace, removing the
  former server-first command ambiguity.

## [0.1.0] - 2026-09-08

### Added

- Shell-friendly direct and daemon-backed discovery, inspection, and invocation
  of MCP tools.
- Composed global and trusted workspace KDL configuration.
- Stdio and Streamable HTTP MCP support, including SDK-backed OAuth with
  encrypted native credential persistence.
- Retained MCP and native LSP sessions, plus selector-routed LSP navigation,
  hover, document symbols, and workspace symbols.
- Terminal-aware pretty output alongside deterministic compact JSON.

[Unreleased]: https://github.com/Kaylebor/wirecmd/compare/v0.4.0...HEAD
[0.4.0]: https://github.com/Kaylebor/wirecmd/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/Kaylebor/wirecmd/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/Kaylebor/wirecmd/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/Kaylebor/wirecmd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Kaylebor/wirecmd/releases/tag/v0.1.0
