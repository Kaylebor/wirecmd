# Age-backed Secret Resolution

Status: authoritative v0.4.0 milestone

## Objective

Allow a configured MCP or LSP process, HTTP value, or OAuth client secret to
use a value from a user-managed encrypted `age` store without teaching Wirecmd
how to create or edit secret material. This reduces accidental inspection and
plaintext persistence while preserving the shell-facing configuration contract.

The built-in secret registry is deliberately closed: it contains `env` and
`age`, is constructed internally by the CLI and daemon, and is not a plugin or
command-provider API. Arbitrary shell providers, provider plugins, generated
plaintext configuration files, and templates remain deferred.

## Configuration and store format

Automatic workspace discovery uses only these names:

- global configuration: `$XDG_CONFIG_HOME/wirecmd/config.kdl`, or
  `~/.config/wirecmd/config.kdl` when `XDG_CONFIG_HOME` is absent or not
  absolute;
- workspace configuration: `.wirecmd/config.kdl`;
- global encrypted store: `$XDG_CONFIG_HOME/wirecmd/secrets.json.age`, or
  `~/.config/wirecmd/secrets.json.age` under the same fallback rule; and
- workspace encrypted store: `.wirecmd/secrets.json.age`.

Automatic discovery does not inspect a top-level `wirecmd.kdl`, fall back to
it, or emit migration guidance for it. Explicit repeated `--config PATH`
values remain a separate ordered source list and may name any file.

Configure the fixed external `age` executable with repeatable, absolute
identity paths:

```kdl
wirecmd {
    secrets {
        age {
            identity "/absolute/path/to/first.identity"
            identity "/absolute/path/to/second.identity"
        }
    }
}
```

Identity files are managed by the user and are never opened or inspected by
Wirecmd. Empty or relative paths are invalid. Configuration layers compose as
usual: a non-empty stronger identity list replaces the weaker list; an empty
`age` block does not erase a weaker identity list.

Use `(secret)"age://NAME"` in every existing textual secret destination:
stdio or LSP arguments and environment entries, HTTP query/header values, and
an OAuth `client-secret`. `NAME` must match `[A-Za-z_][A-Za-z0-9_.-]*`.
`env://NAME` remains available and preserves the distinction between an unset
and an empty environment variable. Both scheme names are case-insensitive.

Each encrypted store decrypts to exactly one UTF-8 JSON object whose keys use
the same name grammar and whose values are strings. Empty string values are
valid. Nested values, non-string values, duplicate keys, trailing JSON, and
plaintext larger than 1 MiB are invalid. For example, the plaintext structure
is:

```json
{
  "SERVICE_TOKEN": "value-managed-outside-wirecmd",
  "OPTIONAL_EMPTY": ""
}
```

Wirecmd never creates, encrypts, decrypts for display, edits, or re-encrypts a
store. It rejects symlink and non-regular store files. During automatic
workspace discovery it also rejects a `.wirecmd` parent that is a symlink or
not a directory. It reads bounded ciphertext through an opened file before
giving it to `age` on standard input. On Linux and macOS, directory-relative
opening and file-identity checks bind validation and use to the same store
object.

## Resolution and lifecycle

Only selected operations resolve secrets. Static server listing, local
completion, ordinary administrative help, and `lsp status` do no secret-store
discovery or decryption.

For automatic discovery, Wirecmd walks from the caller's current directory up
to the nearest trusted workspace root. It considers each
`.wirecmd/secrets.json.age` from nearest to root, then the global store. Each
requested key is independent: the nearest candidate that contains it wins, and
missing keys fall through to the next candidate. Without a trusted root, only
the global store is considered.

For explicit configuration, Wirecmd considers `secrets.json.age` beside each
selected configuration file in reverse source precedence, then the global
store. It does not walk ancestors or consult trusted-workspace state. Missing
requested keys produce one `secret_not_available` result that identifies only
the requested references.

Before startup, references are normalized, deduplicated, and grouped by scheme
and scope. Each provider receives one batch per scope; an age store is
decrypted at most once for that batch. LSP fan-out unions the selected providers'
age references before resolving and passes each process only its declared
values.

Direct invocation resolves again for every invocation. The daemon retains no
plaintext secret-resolution or decoded-store cache. It retains ciphertext
hashes and opaque, daemon-keyed instance metadata to decide whether an unchanged
retained process can be reused. A candidate-store appearance, removal, or
content change requires fresh grouped resolution; the process is replaced only
when the resolved startup values changed. Daemon reload clears that metadata
and replaces retained processes.

`workspace` batches keep trusted workspace-to-global store precedence.
`global` batches read only the global store. Scope-separated metadata prevents
a workspace-store change from invalidating a global provider. In one mixed
scope LSP operation, an operation-local batch may decrypt a physical store used
by both scopes once, while distributing each provider only its declared values.
The [scope and invocation context plan](scope-context-plan.md) defines scope
ownership and provider roots.

## External age boundary and errors

`age` is optional unless an `age://` reference is selected. Wirecmd resolves
the fixed executable from `PATH`, requires version 1.3.0 or newer, and invokes
`age --decrypt` without a shell, repeating `-i` for each configured identity.
Both binary and armored age files are accepted. Decryption is bounded to 60
seconds; timeout or cancellation stops the complete process group.

Hardware-backed identities may request local authorization, including a TPM
PIN or macOS Touch ID. That authorization is allowed for selected secret
resolution even when `WIRECMD_NONINTERACTIVE=1`; the variable continues to
disable only automatic OAuth browser interaction. Raw `age` stderr, identity
contents, plaintext, and decrypted JSON never enter structured output,
diagnostics, fingerprints, or daemon metadata.

Secret failures use structured configuration errors:

- `age_unavailable` when the executable cannot be used;
- `age_version_unsupported` when it is older than 1.3.0;
- `age_decryption_timeout` after the 60-second bound;
- `age_decryption_failed` for a failed decrypt;
- `secret_store_invalid` for unsafe or invalid store input; and
- `secret_not_available` for requested values absent from every candidate.

## Security boundary and qualification

This feature protects against accidental inspection of configuration and
ordinary diagnostic leakage. It does not protect against a hostile same-user
process, an MCP or LSP process that is authorized to receive a secret, or a
secret already inherited through the process environment. Wirecmd makes no
secure-zeroization claim; its guarantee is no deliberate plaintext persistence
or post-resolution plaintext cache.

Qualification covers registry parsing and batching, store precedence,
malformed and oversized documents, symlink and non-regular rejection, age
version/argv/timeout/cancellation behavior, redaction, direct-versus-daemon
equivalence, unchanged daemon reuse, reload replacement, and LSP fan-out. A
release using this feature also requires a non-secret sentinel exercise with
the Linux TPM-backed identity and on a physical Apple Silicon Mac with the
Secure Enclave identity, recording first-start authorization, retained reuse,
reload, direct execution, and cancellation. v0.4.0 is an explicitly authorized
exception: it was published before the physical Mac run so the tagged build
could be used for that test. Until the result is recorded, no physical-device
qualification is claimed for the Secure Enclave-backed path.
