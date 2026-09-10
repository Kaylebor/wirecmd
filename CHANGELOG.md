# Changelog

All notable changes to this project are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and releases use [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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

[Unreleased]: https://github.com/Kaylebor/wirecmd/compare/v0.2.0...HEAD
[0.2.0]: https://github.com/Kaylebor/wirecmd/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/Kaylebor/wirecmd/releases/tag/v0.1.0
