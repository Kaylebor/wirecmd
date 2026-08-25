# Configuration and Runtime Value Design

Status: non-authoritative working notes

This note preserves accepted intent and unresolved design questions from the
configuration discussion. The exact initial HTTP query/header spelling is now
promoted in the authoritative [HTTP values plan](../http-values-plan.md);
other ideas here remain non-authoritative until explicitly promoted.

## Governing intent

Wire Command should be able to place a secret anywhere an MCP server, or a
future upstream API adapter, legitimately requires one without turning KDL,
templates, or the daemon into general-purpose programming systems.

Configuration should therefore remain structural. Commands are represented as
an executable plus argument and environment collections rather than one shell
string. Endpoints are represented as a base URL plus structured path, query,
and header values rather than one preassembled URL. Generated configuration is
represented as an opaque template plus declared bindings and a materialization
destination.

Every value-bearing leaf should use a common semantic value model rather than
an ordinary string that is reparsed later. The model must at least distinguish:

- literal values;
- secret references;
- explicit runtime/template references; and
- values composed or formatted from declared inputs.

Sensitivity follows a value through composition. A header, argument, URL, or
materialized file containing a resolved secret remains secret-bearing even when
it also contains public text.

### Selected KDL value direction

Use native KDL 2 type annotations for typed value leaves. Initially, an
unannotated value is literal and `(secret)"..."` is a secret reference. The
reference string identifies its provider, for example
`(secret)"env://TOKEN"`; an unannotated literal with the same text remains an
ordinary string.

Use structural child nodes rather than encoded command or URL strings:

- repeated argument children retain argv order;
- named environment, query, and header children form map-like destinations;
- nested nodes may describe formatting or materialization only where a real
  destination requires it.

This uses KDL's existing syntax and keeps secret semantics in Wire Command's
configuration model rather than its parser. Do not introduce wrapper objects,
magic-prefix recognition on ordinary strings, a custom lexer, or a generic
value-expression language for the initial implementation. Further annotations
or richer value forms may be added when a concrete source or destination needs
them.

For the initial HTTP slice, the promoted public spelling is `query NAME=value`
and `header NAME=value` inside an `http "..."` node. The selected invariant is
still annotated typed leaves inside structural commands and endpoints; broader
destination layouts remain example-driven.

For the first stdio slice, exercise only the smallest surrounding shape: a
root, named server, workspace scope, stdio executable, ordered arguments,
environment entries, literals, and annotated environment-secret references.
Treat the exact schema beyond that slice as example-driven rather than something
to complete abstractly.

## Sources, destinations, and phases

Keep three concerns independent:

- A value source supplies a literal, an `env://` reference, or a future
  provider-backed value.
- An injection destination consumes the value as an argument, environment
  entry, HTTP header, query parameter, generated-file binding, or a future
  adapter-specific field.
- A resolution phase determines whether the value is available during config
  loading, invocation, or managed-instance creation.

Adding a provider should not require separate implementations for every
destination. Adding a destination should not require knowing every provider.
The first useful implementation may resolve only environment-backed secrets,
but its internal representation must not collapse secret references into
ordinary strings prematurely.

Resolved secrets must not appear in logs, errors, effective-config output,
diagnostic URLs, or plaintext fingerprints. They should not be persisted or
retained longer than their destination requires.

For instance-start sensitive inputs, the daemon derives an internal identity
using a daemon-local keyed digest over canonical destination identifiers and
resolved values. The ephemeral key need not survive daemon restart because live
instances do not survive it either. This identity is not the public effective
configuration fingerprint and must not be logged or persisted. It exists only
to prevent callers with different resolved credentials or other sensitive
startup inputs from reusing the same retained process. Invocation-time secrets
that are applied independently per request do not split the instance.

## KDL and templates

KDL describes static configuration topology. Templates and resolved values may
vary materialized values, but must not create or remove servers, change
transport or scope, or otherwise rewrite the configuration graph.

Only explicitly template-capable fields are interpreted as templates. Other
strings remain literal even when they contain template-looking syntax such as
`{{ ... }}`. Do not globally preprocess KDL.

Opaque generated configuration may produce JSON, YAML, TOML, INI, or arbitrary
vendor text. Wire Command renders bytes and passes them to the upstream server;
it does not parse, merge, validate, or reinterpret the generated format.

Templates receive only explicitly declared bindings. Secret bindings must be
possible so opaque configuration can place a credential wherever its consumer
requires it, but templates must not receive an ambient plaintext secret map.
If a secret contributes to rendered output, the complete materialized artifact
is secret-bearing.

### Selected template direction

Use Go's standard `text/template` with its normal syntax, including
conditionals, ranges, formatting, and named templates. Restrict capabilities by
controlling the data and functions exposed to a template rather than by
inventing a reduced grammar or custom renderer.

The template context contains only a small documented runtime structure plus
explicitly declared public and secret bindings. A conceptual split is
`Runtime`, `Values`, and `Secrets`; exact exported field names remain subject to
examples and implementation. Do not expose the process environment, arbitrary
filesystem objects, provider registries, function-valued application objects,
or functions that perform shell, filesystem, network, or secret-provider
operations. Standard pure template operations are acceptable.

Enable strict missing-key behavior. Cache parsed/compiled templates separately
from rendered output. Apply a reasonable rendered-output size limit to catch
accidental expansion. When any secret binding is supplied, conservatively
treat the complete rendered artifact as secret-bearing even if template control
flow does not ultimately print that binding. Never include secret-bearing
rendered output in ordinary diagnostics, and materialize it with private
permissions and deliberate cleanup behavior.

This template language affects only opaque materialized bytes. It does not
preprocess KDL or change the static configuration topology.

### Selected scalar composition direction

Initial resolved values are validated UTF-8 text plus sensitivity metadata.
This matches command arguments, environment values, URL components, query
values, HTTP headers, KDL strings, and text-template output. A secret provider
that returns invalid UTF-8 produces a structured resolution error. Defer an
explicit binary value/source type and encoding rules until a real upstream API
requires them.

Support only two scalar source shapes initially:

- a direct literal or typed reference; and
- a `text/template` value with explicitly declared public and secret bindings.

Use the same selected template mechanism for exceptional composition such as
an authorization scheme plus a secret. Do not add separate prefix, suffix,
format-string, concatenation, or expression-language APIs. A composed result is
secret-bearing when any secret binding is supplied.

Templates compose one scalar destination or an opaque generated file; they do
not collapse an entire command or URL back into an unstructured string. Resolve
and then pass query values through the URL encoder, headers through their
validation path, and arguments as discrete argv entries. Internally, preserve
resolved text together with sensitivity metadata so redaction never depends on
searching content for secret-looking substrings.

Keep template context small and infrastructure-oriented. Candidate runtime
values include `CWD`, `CONFIG_DIR`, `ROOT`, `RUNTIME_DIR`, `INSTANCE_DIR`,
`INSTANCE_ID`, and, only when a concrete server needs it, `ALLOCATED_PORT`.
Compiled-template caching and rendered-value caching are separate concerns.

## File roles and materialization lifecycle

Keep three file roles distinct:

- A generated artifact is rendered and owned by Wire Command. It is ephemeral,
  private, and located beneath the managed instance's `INSTANCE_DIR`.
- An existing path refers to a user- or workspace-owned configuration file.
  Wire Command resolves and passes the path but does not rewrite, relocate, or
  assume ownership of the file.
- A persistent path names durable server state. Its location and lifetime are
  explicit configuration and are not tied to an ephemeral instance directory.

Representative stdio servers predominantly use argv/environment values,
explicit config-file paths, existing global configuration, or CWD/project
discovery. Servers that accept an explicit config path can consume a generated
artifact under `INSTANCE_DIR`; global/project config and durable stores should
retain their native paths. Do not permit arbitrary external destinations for
generated templates initially. A server that proves it requires generated
credentials at a fixed external location is a concrete exception to deliberate
later, not a reason to allow general overwrites now.

Generated destination paths are relative to `INSTANCE_DIR`. Reject absolute
paths, parent traversal, symlink escape, and accidental collisions. Create the
instance directory with private user-only access and secret-bearing files with
private file permissions. Render into a new temporary file within that
directory and rename it into place before starting the server; never update a
running instance's generated file in place.

Generated artifacts live for the complete managed-instance lifetime because a
server may reread them. Remove the instance directory after failed startup or
graceful instance termination. On configuration reload, create a new instance
directory for the new pool and retire the old directory with its instance.
After acquiring the singleton daemon lock, a new daemon may remove stale
instance directories left by an earlier crashed daemon generation. Direct mode
uses the same private temporary layout and removes it when the directly managed
process exits.

Do not initially add configurable modes/ownership, encrypted temporary files,
in-place replacement, a cleanup scheduler, content-aware merging, or claims of
secure deletion from modern filesystems.

## Shared daemon and invocation context

The CLI and daemon are assumed to run within the same host and filesystem
namespace. The daemon can therefore discover, read, merge, cache, and explain
configuration for the caller's directory instead of requiring every CLI
process to send a complete normalized configuration.

Each request still needs explicit invocation context, including at least the
caller working directory, explicit config paths, and an explicit root override
when present. The daemon's own working directory must never stand in for the
caller's.

Filesystem-derived configuration belongs naturally to the shared daemon, but
process-local inputs may differ between simultaneous CLI callers. In
particular, two callers on the same host can provide different values for the
same environment variable. The daemon must not silently substitute its startup
environment for a caller-scoped environment reference.

Because both modes use the same executable and configuration package, the CLI
can resolve the effective definition far enough to identify the selected
server's caller-owned `env://` references. It sends only those resolved values,
along with the non-secret effective-config fingerprint it observed. Listing,
help, and other operations that do not require a server's secrets should not
resolve or transmit them. Preserve missing values separately from present but
empty values, and deduplicate repeated uses of the same reference.

The daemon independently validates supplied references against its cached
effective configuration. It remains authoritative for execution and must not
treat extra caller values as arbitrary environment injection. This duplicates
a small amount of config evaluation but shares one implementation and avoids a
general challenge-response IPC protocol or wholesale environment transfer.

A client/daemon configuration-fingerprint mismatch normally produces a warning
on stderr recommending daemon reload. The daemon may proceed with its cached
configuration when the requested operation and every value it expects remain
unambiguous and satisfied; caller-supplied values that its definition does not
reference are not injected. If the mismatch leaves a required value missing,
changes the selected operation/server, or otherwise makes execution ambiguous,
fail with a structured actionable configuration error rather than guessing.

Secret-bearing daemon RPC fields must be explicit, non-loggable, and protected
by private per-user IPC. `--direct` should apply the same semantic resolution
pipeline locally.

## Configuration cache

The daemon may retain a per-directory configuration cache. Its identity may
need to account for more than a raw directory path, including:

- starting directory;
- explicit config file list;
- global/XDG configuration identity;
- explicit root override; and
- an explicit reload generation.

Cached configuration should retain source paths and provenance so later
hierarchical merging, `CONFIG_DIR`-relative values, and config explanation do
not require a second representation. An initial daemon may assume files remain
stable until explicit reload or restart. Filesystem watchers and dependency
graphs are not required.

## Hierarchical discovery and merge

The public precedence rule is that more local configuration wins. The daemon
may implement this naturally as one local-to-global discovery walk that fills
only unresolved values:

1. explicit config overlays from last to first;
2. the nearest discovered project/workspace config and then its ancestors; and
3. the separate global user config.

Repeated explicit `--config` files are final overlays in command-line order, so
the last supplied file has highest precedence. During the first slice, before
automatic discovery exists, the supplied files are simply the complete source
list. If suppressing automatic discovery later proves useful, prefer an
explicit option rather than making `--config` silently change both source and
precedence behavior.

Track each merge location as absent, present, or removed:

- take a scalar only while absent;
- recursively fill missing children in named maps and structural objects;
- treat an explicitly present ordered collection, including an empty one, as a
  complete replacement;
- treat a removal tombstone as resolved so broader layers cannot restore it.

Use a native KDL node annotation as the initial keyed-removal form, for example
`(remove)env "OLD_TOKEN"` or `(remove)server "legacy"`. Tombstones apply to
keyed entries. Do not add element-level deletion for ordered collections.

Replacing ordered collections is intentionally conservative. Implicitly
concatenating argv or another order-sensitive structure can produce invalid or
surprising commands. Explicit append/prepend operations may be considered
later for a concrete collection that benefits from composition, but the first
model has no generic collection patch language.

Duplicate keyed entries within one source file are configuration errors rather
than an additional precedence layer. Every winning value and tombstone retains
its source file and semantic path. Relative values retain their originating
`CONFIG_DIR`; merging never reinterprets them relative to a more local file.

An ancestor-stop marker ends project/workspace discovery beyond that source but
does not suppress the distinct global user base. Exact config filenames and the
marker's KDL spelling remain schema/example decisions.

## Reload and invalidation

Changed on-disk configuration, including a changed reference or value used to
start an instance, is one configuration-invalidation concern rather than a
separate secret-rotation subsystem. Initially, the daemon does not watch for
changes. A client-observed fingerprint mismatch warns that reload is advisable
under the compatibility rules above.

An explicit reload invalidates the relevant parsed/effective configuration and
compiled-template caches. Subsequent calls resolve the new static fingerprint
and instance-start inputs and therefore select or create the corresponding new
pool. Previously selected pools receive no new calls; allow active calls to
finish and retire those pools through the normal lifecycle. This requires no
provider-specific rotation scheduler, proactive refresh loop, or semantic diff
engine. Invocation-time values such as per-request HTTP headers continue to be
resolved and applied per call and do not force instance replacement.

## Managed instance pools

Configuration caching and managed-instance pooling are distinct. A cached
effective config describes what could be run; an instance pool owns live MCP
processes or connections.

The original concept permits a pool per resolved scope and configured MCP
server, including per-directory pools when CWD scope or the root fallback makes
that appropriate. This is especially useful for servers that are stateless in
practice. The default maximum pool width is one. Wider pools are opt-in and
assert that instances created from the same effective server definition and
scope are interchangeable.

The default semantic scope is `workspace`, identified by the effective
`ROOT`, rather than by the exact directory from which a call happens. Resolve
the effective root in this documented order:

1. an explicit CLI root override;
2. the effective `root` declared by configuration;
3. the directory containing the nearest discovered project/workspace KDL file;
4. the caller's exact `CWD` when only global or explicit external configuration
   exists.

This deliberately avoids Git, language, or project-type root heuristics. Calls
from sibling directories converge on one workspace pool when they resolve to
the same root. With only the final CWD fallback, sibling directories remain
separate until configuration or `--root` supplies a shared workspace identity.

The initial scope vocabulary is `workspace`, `global`, and `cwd`. Global scope
may share a pool across workspaces when the remaining identity agrees; CWD
scope uses the caller's exact directory. Defer custom scope expressions until a
concrete server requires them.

A logical pool identity consists of the server alias, resolved scope key,
effective static server-configuration fingerprint, and authentication identity.
Only values that affect instance creation contribute. Invocation-time tool
arguments do not split a pool merely because they depend on CWD. Conversely, a
CWD-derived command argument, environment value, or generated file used when
starting an instance changes its effective instance configuration and must
separate the pool unless CWD scope already does so.

The implementation should not try to prove interchangeability or build a
scheduler framework during the first slice. It should, however, avoid an
instance identity based only on server alias. Directory/context, effective
scope, static configuration, and authentication identity may all require
separation. Runtime-generated values such as instance directories or allocated
ports must not affect the static configuration fingerprint.

Loss of state must always be reported honestly rather than hidden by
replacement from another instance.

## Remaining example-driven work

Concrete KDL node names and layout for HTTP endpoints, formatting, generated
files, and template bindings should be settled with the first real fixture for
each destination. Native annotations on value leaves and the surrounding
structural invariants are already selected; do not attempt to finish the whole
schema before those examples exist.
