# Development Guide

This guide defines the repository workflow for agents and human contributors.
It complements the product model in [`AGENTS.md`](../AGENTS.md) and the
authoritative product direction in the [product thesis](product-thesis.md).

## Establish authority before changing behavior

Start with the [documentation index](README.md), then read the product thesis,
current roadmap, and any accepted plan that governs the affected feature. The
roadmap's future candidates are a decision queue, not authorized milestones.
Files under `notes/` are non-authoritative evidence and working material.

Validate premises against the current source and environment. Current code is
evidence of implemented behavior, but an implementation accident does not
silently supersede an authoritative product decision. When authoritative
documents disagree, stop and resolve the product-level conflict before
encoding one interpretation.

For answer, explanation, diagnosis, or review requests, inspect and report; do
not implement an unsolicited fix. For change requests, make the smallest
coherent change that preserves the product model and run proportional
verification. Ask only when the answer changes the public contract,
architecture, dependency, format, security boundary, or authorization scope.

## Scope and implementation discipline

- Keep configuration, invocation context, scope, resolved values, and provider
  semantics distinct even when combining them would reduce local code.
- Prefer generic mechanisms only when a demonstrated use needs them. Do not
  pre-build marketplaces, policy engines, workflow systems, speculative adapter
  families, or extension frameworks.
- Do not replace an accepted direction merely because another approach is
  easier. Preserve the evidence and ask before weakening or substituting it.
- Keep upstream adapters behind narrow semantic boundaries and keep public
  contracts independent of SDK and wire representations.
- Preserve user changes in a dirty worktree and avoid destructive Git or
  filesystem operations unless explicitly authorized.

## Dependencies and formats

Adding or removing a library requires a user decision. Before proposing one,
identify the concrete need and inspect credible current candidates using
primary sources. Present API fit, maintenance, license, supported Go version,
transitive impact, and migration cost in proportion to the decision. Do not
edit manifests or lockfiles until the choice is accepted.

Reconsider a library when maintained behavior would otherwise be duplicated,
correctness is unusually sensitive, ecosystem interoperability requires a
standard implementation, or a second demonstrated use makes a local
abstraction costly. Do not add a framework for a deferred feature or one
speculative consumer.

KDL 2 is the current public configuration format. A new user-facing format,
including a temporary shortcut, requires explicit approval. Internal test data
that does not create a persisted public contract is allowed.

## Verification

Match verification to risk and state exactly what ran.

- **Focused checks:** run the smallest deterministic tests covering the changed
  package and behavior while iterating.
- **Cross-cutting checks:** add direct/daemon, configuration-composition,
  identity, redaction, and concurrency coverage when the change crosses those
  boundaries.
- **Full repository checks:** use module verification, complete tests, race
  tests, vet, builds, diff checks, completion tests, and the platform matrix
  when an accepted plan or the change's risk requires them.
- **Release checks:** follow [release readiness](release-readiness.md) exactly;
  passing preparation checks does not authorize a tag or GitHub Release.

Exercise real agent-facing discovery and composition where a milestone depends
on usability, not only unit tests or direct SDK calls. A native LSP claim also
requires a real configured server demonstration; fixtures alone are
insufficient. Record exact commands, versions, fixtures, observed results, and
known environmental limitations for qualification evidence.

Treat credential, network, socket, and permission failures observed in a
sandbox as potentially sandbox-induced. Retry the same diagnostic through the
available approval mechanism before changing credentials, configuration, or
the intended workflow.

## Delegation and review

Delegate substantial independent research, implementation, testing, or review
when it improves evidence or keeps noisy work isolated. Do not delegate simple
questions or routine validation merely to create activity. Give each agent a
self-contained, bounded assignment with the relevant product context,
constraints, files, and expected evidence.

Use independent testing or adversarial review for consequential runtime,
architecture, security, concurrency, and release changes. Reconcile every
review against current source, authoritative documents, primary upstream
sources, and direct evidence before adopting it.

After opening or updating a pull request, leave it unmerged for at least five
minutes so automated review notes can arrive. Then inspect every review,
comment, and unresolved thread. Bypass this hold only when the user explicitly
authorizes it for that PR.

## Change and publication hygiene

- Use `codex/` branch names unless the user requests another convention.
- Do not commit or push unless explicitly requested. Opening, updating,
  merging, tagging, releasing, or otherwise publishing externally also requires
  authorization from the task.
- Do not commit generated artifacts, credentials, decrypted values, local
  runtime state, or test tokens. Never expose secrets in logs or reports.
- Keep unrelated user changes intact. Preview the final diff and use
  non-destructive Git commands.
- Every non-release PR adds or amends an accurate `[Unreleased]` entry in
  `CHANGELOG.md` as part of the same reviewed change. Release preparation
  freezes unrelated merges, curates those accumulated entries into a versioned
  release, and publishes that change through its own reviewed PR as defined by
  release readiness.
- Treat `main` as a merge-only target. Branch ordinary code, test, and
  documentation work from current `main`, and return it through the reviewed
  pull-request path rather than committing directly to `main`.
- Tags and GitHub Releases are separate explicit maintainer actions. CI success
  or a merged release-preparation PR does not authorize either one.

When a decision changes the durable product boundary, update the product thesis
or applicable authoritative plan. Put unresolved alternatives and exploratory
evidence in `notes/` with an explicit non-authoritative label.
