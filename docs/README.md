# Wirecmd documentation

Start with the repository [README](../README.md) for an introduction,
installation, and the first commands to run.

## Use Wirecmd

- [Configuration reference](configuration.md) documents the implemented KDL
  surface, discovery and composition rules, transports, secrets, and LSP
  selectors.

## Understand and maintain the project

- [Product thesis](product-thesis.md) defines the product direction and scope.
- [Current roadmap](roadmap.md) records the shipped baseline and open decision
  queue.
- [Development guide](development.md) defines repository workflow,
  verification, dependency decisions, review, and change hygiene.
- [Release readiness](release-readiness.md) defines installation,
  qualification, versioning, and publication policy.

## Design records and working material

- [`plans/`](plans/) contains authoritative contracts, completed milestone
  records, and qualification plans. A file named as a plan is not necessarily
  future work; check its status at the top of the document. The accepted plans
  are:

  - [Age-backed secrets](plans/age-secrets-plan.md): provider routing, store
    discovery, resolution, reuse, and the security boundary.
  - [Onboarding and completion](plans/onboarding-plan.md): administrative help,
    MCP command structure, and private Fish completion.
  - [Live help metadata](plans/help-metadata-plan.md): conventional suffix help
    and live MCP server and tool help.
  - [Output](plans/output-plan.md): contextual presentation, JSON, and color.
  - [MCP resources](plans/resources-plan.md): listing, reading, pagination,
    normalization, and redaction.
  - [Legacy HTTP+SSE](plans/sse-plan.md): SDK-backed compatibility and the OAuth
    boundary.
  - [Configuration discovery](plans/discovery-plan.md): trusted automatic
    discovery and explicit configuration behavior.
  - [HTTP values](plans/http-values-plan.md): typed query and header values.
  - [OAuth](plans/oauth-plan.md): transparent authorization and encrypted
    credential persistence.
  - [LSP](plans/lsp-plan.md): server-neutral navigation, signature help,
    inspection, routing, and lifecycle.
  - [Initial validation](plans/validation-plan.md): acceptance criteria and the
    completed first validation milestone.
  - [Protocol compatibility](plans/compatibility-plan.md): supported MCP
    protocol qualification.
  - [macOS qualification](plans/macos-plan.md): CI coverage, Apple Silicon
    physical qualification, and release gates.
  - [Scope and invocation context](plans/scope-context-plan.md): shared project
    root resolution plus workspace and global lifecycle ownership.
- [`notes/`](notes/) contains non-authoritative research, validation evidence,
  and working material. Nothing there becomes a product requirement without
  explicit promotion into an authoritative document.
