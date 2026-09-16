# Wirecmd Repository Guidance

## What Wirecmd is

Wirecmd is a shell-native, configuration-driven capability runtime and local
lifecycle broker. It gives shell-capable agents a stable way to discover,
inspect, invoke, and compose capabilities without requiring native protocol
support or eager tool injection from their harness.

The immediate caller is usually an agent. A technical human commonly configures
or supervises it, but a properly instructed agent may configure it too. Humans,
agents, scripts, editors, and CI use the same semantic command, result, error,
and exit-code contract.

MCP and LSP are upstream capability families, not the definition of the
product. Wirecmd may support other families when demonstrated needs justify
them. The shell remains the workflow language; Wirecmd must not grow a
competing workflow engine.

## Product model

Keep these concepts separate:

- **Configuration sources** define reusable providers and compose global and
  optional project-specific knowledge with explainable provenance.
- **Invocation context** describes facts Wirecmd resolves for a call, such as
  its current directory and project or global context.
- **Resolved values** deliberately materialize context or secrets into fields
  that a provider consumes.
- **Instance scope** selects the ownership and reuse boundary for a live
  process or connection. It does not determine where configuration was declared
  or which contextual values exist.
- **Materialized provider configuration** is the actual startup or request
  input after composition and resolution.
- **Authentication identity** describes the credentials or upstream principal
  selected for an operation without exposing secret material.
- **Retained-instance identity** combines scope, startup-affecting
  configuration, and sensitive identity. Reuse is valid only when that identity
  agrees.

Global configuration should be able to define a provider once. Project
configuration supplies defaults or overrides only when useful. If contextual
materialization makes two nominally global providers start differently, honest
identity may separate their instances; Wirecmd must not hide that distinction.

Current code is evidence about implemented behavior, not automatic authority
for the product model. When implementation coupling conflicts with this model
or an accepted decision, preserve the evidence and resolve the conflict before
building on it.

## Ownership boundary

Wirecmd owns generic discovery, composition, provenance, context resolution,
explicit materialization, secret handling, process and connection lifecycle,
protocol conformance, normalized output, redaction, and actionable recovery.

The configurator owns arbitrary provider semantics: executable, arguments,
environment, opaque settings, selectors, and how available values are supplied
to the upstream program. Trust the configurator to understand that program;
validate Wirecmd's own syntax, safety boundaries, and lifecycle invariants.

Prefer official protocol SDKs for protocol behavior. Use their highest-level
supported APIs and add a narrow local shim only for a confirmed gap. Do not
duplicate negotiation, transport, authorization, or revision behavior an SDK
already implements correctly.

Known-provider conveniences may be added only after repeated evidence. They
must compile down to the ordinary configuration model rather than create a
second architecture or make the generic path incomplete.

For an unfamiliar requirement, ask in order:

1. Is it required by the upstream protocol?
2. Is it a generic Wirecmd runtime or lifecycle invariant?
3. Is it arbitrary provider behavior that belongs in user configuration?

Encode the first two centrally. Preserve an expressive configuration path for
the third instead of inferring server-specific policy.

## Durable invariants

- Preserve lazy, agent-directed discovery and ordinary shell composition.
- Keep a lossless structured invocation path even when ergonomic projections
  exist.
- Keep stdout machine-composable. Diagnostics belong on stderr, and secrets,
  tokens, credentials, and unredacted secret-bearing URLs must never appear in
  output, logs, fingerprints, IPC metadata, or errors.
- Normal operation is daemon-backed and fails clearly when the daemon is
  unavailable. `--direct` is an explicit one-shot and diagnostic path; never
  fall back to it silently.
- Direct and daemon-backed execution must preserve the same observable contract
  except where real continuity changes operation semantics.
- Do not claim retained continuity after replacement, restart, or identity
  change. Lost state must be reported honestly.
- Keep public command and result contracts independent of upstream wire
  revisions and generated SDK types.
- Treat deterministic non-interactive behavior as canonical. Documented OAuth
  interaction and hardware-backed identity prompts are narrow explicit
  exceptions, not general permission for surprise interaction.
- Prefer the smallest coherent change that preserves this model. Local
  implementation simplicity does not justify collapsing configuration,
  context, scope, or provider semantics.

Wirecmd is not an IDE, agent harness, marketplace, provider catalog, policy
engine, general plugin runtime, security sandbox for hostile same-user
processes, or universal service manager.

## Authority and current state

- [The product thesis](docs/product-thesis.md) defines durable direction and
  product boundaries.
- [The roadmap](docs/roadmap.md) records shipped state and the current decision
  queue; future candidates are not accepted milestones.
- [The documentation index](docs/README.md) routes users and maintainers to
  authoritative feature plans and non-authoritative working notes.
- [The development guide](docs/development.md) defines repository workflow,
  verification, dependency decisions, review, and change hygiene.
- [Release readiness](docs/release-readiness.md) owns qualification, versioning,
  changelog curation, tagging, and publication.

Read the applicable accepted plan before changing a specialized feature.
Files under `docs/notes/` are evidence and working material, never requirements
until deliberately promoted. When authoritative documents disagree, stop and
resolve the product conflict rather than selecting the convenient interpretation.

## Essential working rules

- Ask before a dependency change, new user-facing format, destructive action,
  external publication, or material expansion of an accepted contract.
- Do not commit or push unless explicitly requested. Never commit credentials,
  generated artifacts, local runtime state, or test tokens.
- Feature PRs do not edit `CHANGELOG.md`; release preparation owns changelog
  curation under the release-readiness contract.
- Verify changes proportionally and use independent review for consequential
  behavior, architecture, security, concurrency, and release decisions.
- After opening or updating a PR, leave it unmerged for at least five minutes,
  then inspect all reviews, comments, and unresolved threads. Bypass that hold
  only when the user explicitly authorizes it for that PR.
