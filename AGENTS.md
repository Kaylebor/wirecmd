# Repository Guidance

## Authority and document roles

- `README.md` is the short public orientation.
- `docs/onboarding-plan.md` defines administrative help and private Fish
  completion. Completion must remain local, read-only, secret-free and must not
  contact the daemon or upstream servers. MCP help and execution use the `mcp`
  namespace; tool-side `--` retains raw-overlay ownership.
- `docs/help-metadata-plan.md` defines conventional suffix help. MCP server and
  tool help is live: it needs the daemon or explicit `--direct` execution;
  persistent MCP metadata is not retained for help or completion.
- `docs/product-thesis.md` is authoritative for product direction and scope.
- `docs/roadmap.md` is the authoritative current-state handoff and decision
  queue. Its future candidates are not accepted implementation milestones;
  each requires the corresponding deliberate plan and authorization.
- `docs/output-plan.md` is authoritative for contextual presentation and color;
  it supersedes earlier milestones' always-JSON presentation wording.
- `docs/validation-plan.md` is the authoritative record of the completed first
  validation milestone and its acceptance criteria.
- `docs/compatibility-plan.md` is the authoritative record of the completed
  supported-protocol qualification; legacy HTTP+SSE remains deferred there.
- `docs/resources-plan.md` is authoritative for the MCP resources slice and
  its SDK-owned pagination, reading, normalization, and redaction boundary.
- `docs/discovery-plan.md` is authoritative for the completed automatic
  configuration-discovery milestone and its acceptance criteria.
- `docs/http-values-plan.md` is authoritative for the completed typed HTTP
  query/header configuration milestone and its acceptance criteria.
- `docs/oauth-plan.md` is authoritative for the completed transparent OAuth and
  encrypted credential-persistence milestone and its acceptance criteria.
- `docs/lsp-plan.md` is authoritative for the completed server-neutral,
  selector-routed multi-provider LSP navigation, signature-help, and inspection
  milestone.
- `docs/release-readiness.md` is authoritative for stable installation,
  compatibility, qualification, versioning, and the manual publication
  boundary.
- `docs/macos-plan.md` is authoritative for the current macOS Apple Silicon
  qualification milestone, Intel CI boundary, and physical M2 release gate.
- Files under `docs/notes/` are non-authoritative working material. Do not turn
  an idea from those files into a requirement without promoting it explicitly
  into an authoritative document.
- When documents disagree, stop and resolve the product-level conflict before
  encoding one interpretation in implementation.

## Product alignment

- Preserve the central boundary: the shell is the agent-facing capability
  interface; MCP is initially an upstream adapter and implementation detail.
- Optimize for lazy discovery and shell composition rather than eager schema
  injection into a harness.
- Treat deterministic non-interactive behavior as canonical. Ordinary calls
  remain non-interactive when either stdin or stderr is not a TTY, or when
  `WIRECMD_NONINTERACTIVE=1` is set. When both are TTYs, an ordinary protected
  HTTP call may transparently open a browser for OAuth and wait for the
  callback; this is the accepted interactive convenience and must remain
  visible only through stderr diagnostics.
- Keep human, agent, script, and CI invocation on the same public contract.
- Normal operation is daemon-backed and must fail clearly when the daemon is
  unavailable. `--direct` is the explicit daemonless path for testing,
  diagnostics, and deliberate one-shot use; never fall back to it silently.
- The CLI contract should remain behaviorally consistent across daemon-backed
  and direct execution where continuity does not change the operation's
  semantics.
- Validate the daemon path early because currently deployed MCP servers often
  depend on initialized sessions or persistent processes. Keep the first daemon
  narrow: connection/process reuse and the minimum lifecycle needed to exercise
  it, not a general service-management subsystem.
- MCP proxy/server compatibility is optional. Do not let it shape or delay the
  agent-facing shell contract.
- Native LSP navigation and read-only inspection are the completed first
  non-MCP capability. Keep them server-neutral: configuration owns executables,
  arguments, environment, language IDs, selectors, and workspace selection;
  Wirecmd must not add a language-server catalog, executable inference,
  presets, or server-specific behavior.

## Scope discipline

- Work toward the current authoritative milestone before expanding product
  breadth.
- Prefer the smallest coherent, reversible change that tests a stated
  hypothesis or satisfies an accepted contract.
- Do not pre-build marketplaces, generic plugin systems, configuration import
  frameworks, policy engines, record/replay systems, advanced schedulers, or
  future adapter families without current milestone evidence.
- Do not mistake implementation convenience for product validation. A
  successful MCP call is necessary but does not demonstrate that an agent can
  discover and compose capabilities effectively.
- Keep exploratory material in `docs/notes/` until evidence and an explicit
  decision justify promotion.
- If an accepted direction proves difficult, unsupported, or in tension with
  another project goal, preserve the evidence and ask the user before changing,
  weakening, bypassing, or substituting that direction. Difficulty is not
  authorization to choose a different product contract, dependency, format,
  workflow, or architecture.

## Architecture and implementation

- Keep the agent-facing command and result contracts independent of MCP wire
  revisions, transports, and SDK types.
- Prefer the official Go MCP SDK for protocol behavior. Do not duplicate
  negotiation, transport, authorization, or revision behavior it already
  implements correctly.
- When MCP behavior is needed, first assume the pinned official SDK supports it
  and verify that assumption against its documentation, source, examples, and
  tests. Use the highest-level supported API. Add a local MCP-specific shim only
  for a confirmed SDK gap, keep it narrow, and record the exact limitation.
- The official SDK also owns OAuth discovery, metadata, PKCE, registration,
  token exchange, refresh, resource indicators, scopes, issuer validation, and
  HTTP retry behavior. Wirecmd owns only the shell/daemon interaction,
  persistence, redaction, and error mapping around those APIs. The pinned SDK
  does not expose a separate stable resource/issuer identity for our storage
  boundary, so the current credential identity is based on the resolved
  endpoint and configured registration inputs; record evidence before adding a
  compatibility shim or broader identity model.
- Do not turn conceptual protocol eras into parallel local protocol stacks. The
  full MVP targets modern `2026-07-28`, legacy initialized stdio/Streamable
  HTTP, and legacy HTTP+SSE by exercising the official SDK from newest to
  oldest.
- Keep upstream adapters behind a narrow semantic boundary, but do not build
  unused adapters or a speculative extension framework.
- The native LSP client owns standard framing and lifecycle, conservative
  initialization, disk-backed document synchronization, position conversion,
  normalized results, selector routing, provider fan-out, contextual status,
  and daemon retention. Keep its public command/result contract independent of
  generated LSP SDK types. Do not add initialization options, unsaved buffers,
  dynamic registration, workspace settings, edits, mutating operations,
  dynamic completion, or raw protocol access without a separate accepted
  milestone.
- Preserve a lossless structured invocation path even when ergonomic flags are
  projected from schemas.
- Preserve structural argument ownership: client flags precede the
  `mcp <server> tool <tool>` path, while arguments after the tool name belong to that
  tool. The narrow trailing-help exception is schema-aware: an explicit
  projected `help` property owns `--help`/`-h`, otherwise Wirecmd renders live
  focused help. Only the live schema may decide that ownership. Handle other
  rare projected-name collisions with warnings and a structurally distinct
  exact-JSON invocation path. The `mcp` namespace keeps MCP-server names and
  future MCP primitives separate from native administrative groups.
- Keep stdout machine-composable. Send diagnostics to stderr and never print
  secrets, tokens, credentials, or unredacted secret-bearing URLs.
- Preserve compact JSON for non-terminal defaults and explicit
  `--format json --color never`. Terminal defaults use contextual pretty
  presentation without changing semantic envelopes or exit codes. Determine
  presentation from stdout independently of OAuth's stdin/stderr checks.
- Keep `wirecmd --version` independent of configuration, daemon, keyring, and
  upstream state. Do not reserve the positional form `wirecmd version`.
- Structured errors must distinguish user action, authentication, invocation,
  upstream protocol, transport, configuration, and internal failures where an
  agent would recover differently.
- KDL 2 is the current preferred configuration direction, subject to deliberate
  parser and usability qualification. Do not introduce another user-facing
  configuration format, including as a supposedly temporary shortcut, without
  first presenting the evidence and alternatives to the user and obtaining a
  decision. Internal test construction that does not create a public or
  persisted configuration contract is allowed.
- Adding or removing a library requires a user decision. Before proposing it,
  briefly inspect the current ecosystem and primary sources, then present the
  concrete need, credible candidates, relevant maintenance and compatibility
  evidence, and the smallest reasonable recommendation. Keep the query
  proportional; dependencies are valid implementation choices, not presumed
  failures of YAGNI. Do not edit dependency manifests or lockfiles to add or
  remove a library until the user accepts the choice.
- Reconsider a library only when a concrete current requirement shows that the
  standard library or existing small implementation would duplicate substantial
  maintained behavior, a second real use case forces an abstraction, correctness
  is unusually security/protocol/parser/concurrency sensitive, ecosystem
  interoperability requires a standard implementation, or local maintenance is
  likely to cost more than integration and dependency tracking. Do not add a
  framework for a deferred feature, one speculative consumer, or a small amount
  of straightforward code. At each reconsideration point, show the demonstrated
  limitation, current candidates, API fit, maintenance, license, Go-version and
  transitive-dependency evidence, migration cost, and the smallest recommendation
  before requesting approval.

## Verification

- Tie tests and demonstrations to the acceptance criteria in
  `docs/validation-plan.md`.
- Apply the release gates and manual publication boundary in
  `docs/release-readiness.md`; passing preparation checks does not authorize a
  tag or GitHub Release.
- Treat macOS CI as necessary but insufficient for Apple Silicon support; the
  physical checks and visibility gate are defined in `docs/macos-plan.md`.
- Exercise real agent-facing discovery and composition, not only unit tests or
  direct SDK calls.
- Record exact commands, server fixtures, observed outputs, latency conditions,
  and token/context measurements used for comparisons.
- Treat comparisons with existing clients and direct harness MCP exposure as
  evidence, not as requirements to copy their feature sets.
- Do not claim native LSP qualification from fixture tests alone. Record a
  real configured-server demonstration before marking an LSP milestone
  qualified; `gopls` is a test fixture, never a production dependency or
  default.
- Validate both one-shot and daemon-backed paths where the current milestone
  requires them, including observable-contract equivalence and real continuity
  across separate CLI invocations.

## Multi-agent work

- Proactively delegate substantial independent research, implementation,
  testing, and review work. Do not delegate simple questions, single-file
  edits, or routine validation merely to create parallel activity.
- Assume a subagent receives only this repository guidance and the assignment
  it is given. Brief it with the relevant product context, constraints, files,
  expected evidence, and exact deliverable; never rely on conversational
  context that was not included explicitly.
- Give each subagent one bounded, non-overlapping responsibility. Prefer
  independent read-heavy work and avoid concurrent edits unless file ownership
  is unambiguous.
- Use independent testing or adversarial review for consequential behavior and
  architecture boundaries. Reconcile agent conclusions against current files,
  primary sources, and direct runtime evidence before adopting them.
- Reuse an existing suitable agent when practical, but provide the same
  self-contained context in follow-up assignments.

## Change hygiene

- Update authoritative documents when an accepted decision changes the product
  boundary or current milestone.
- Feature PRs do not edit `CHANGELOG.md`. Release preparation owns the manual
  changelog update: freeze unrelated feature merges; curate `[Unreleased]`
  against the intended release contents into a dated version entry; recreate
  an empty `[Unreleased]` section; roll comparison links from
  `vPREVIOUS...vX.Y.Z` to `vX.Y.Z...HEAD`; then commit and publish that
  preparation through the normal reviewed PR/merge path. Use the same reviewed
  summary for the GitHub Release. GitHub-generated notes are a draft, never an
  authoritative replacement for that review. If an unrelated change lands
  before that PR merges, re-curate against the new `main` before proceeding.
  Changelog headings use `X.Y.Z`; tags, module versions, and binary versions
  use `vX.Y.Z`.
- Put unresolved alternatives, research fragments, and speculative mechanisms
  in `docs/notes/` with an explicit non-authoritative label.
- Do not commit generated artifacts, credentials, local runtime state, or test
  tokens.
- Do not commit or push unless explicitly requested.
