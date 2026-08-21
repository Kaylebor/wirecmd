# Repository Guidance

## Authority and document roles

- `README.md` is the short public orientation.
- `docs/product-thesis.md` is authoritative for product direction and scope.
- `docs/validation-plan.md` is authoritative for the current milestone and its
  acceptance criteria.
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
- Treat deterministic non-interactive behavior as canonical. Interactive
  behavior may be an explicit convenience, never an unexpected prompt during
  an ordinary call.
- Keep human, agent, script, and CI invocation on the same public contract.
- A daemon must remain optional and behavior-preserving. It may accelerate or
  broker lifecycle-sensitive work, but ordinary calls must not require it
  unless a capability intrinsically requires long-lived state.
- Validate the daemon path early because currently deployed MCP servers often
  depend on initialized sessions or persistent processes. Keep the first daemon
  narrow: connection/process reuse and the minimum lifecycle needed to exercise
  it, not a general service-management subsystem.
- MCP proxy/server compatibility is optional. Do not let it shape or delay the
  agent-facing shell contract.

## Scope discipline

- Work toward the current validation milestone before expanding protocol or
  product breadth.
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

## Architecture and implementation

- Keep the agent-facing command and result contracts independent of MCP wire
  revisions, transports, and SDK types.
- Prefer the official Go MCP SDK for protocol behavior. Do not duplicate
  negotiation, transport, authorization, or revision behavior it already
  implements correctly.
- Keep upstream adapters behind a narrow semantic boundary, but do not build
  unused adapters or a speculative extension framework.
- Preserve a lossless structured invocation path even when ergonomic flags are
  projected from schemas.
- Keep stdout machine-composable. Send diagnostics to stderr and never print
  secrets, tokens, credentials, or unredacted secret-bearing URLs.
- Structured errors must distinguish user action, authentication, invocation,
  upstream protocol, transport, configuration, and internal failures where an
  agent would recover differently.

## Verification

- Tie tests and demonstrations to the acceptance criteria in
  `docs/validation-plan.md`.
- Exercise real agent-facing discovery and composition, not only unit tests or
  direct SDK calls.
- Record exact commands, server fixtures, observed outputs, latency conditions,
  and token/context measurements used for comparisons.
- Treat comparisons with existing clients and direct harness MCP exposure as
  evidence, not as requirements to copy their feature sets.
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
- Put unresolved alternatives, research fragments, and speculative mechanisms
  in `docs/notes/` with an explicit non-authoritative label.
- Do not commit generated artifacts, credentials, local runtime state, or test
  tokens.
- Do not commit or push unless explicitly requested.
