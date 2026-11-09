# Exec secret injection proposal

Status: non-authoritative proposal; not an accepted milestone. Recorded as a
candidate in the [roadmap](../roadmap.md) decision queue. Nothing here changes
an accepted contract.

## Purpose

Wirecmd resolves secrets only for the providers it launches or calls: MCP and
LSP stdio processes, HTTP and SSE values, and OAuth client secrets. A shell
agent that must run an ordinary command with a credential (`psql`, `gh`,
`curl`, a deploy script) has no Wirecmd path for it. Today the credential
either sits in the agent's own shell environment, or the agent is handed the
value directly. Both put the secret within reach of the model context.

The proposal is a configured, named command that Wirecmd runs with secrets
materialized into the child process. The agent names the profile and never
supplies or sees the value.

```
agent -> wirecmd exec NAME -> resolve secrets -> child process
              ^                                       |
              | stdout/stderr, redacted               |
              +---------------------------------------+
```

## Fit with the accepted direction

Applying the decision test in the [product thesis](../product-thesis.md):

1. It is not required by an upstream protocol.
2. Secret resolution, explicit materialization, redaction, and process
   lifecycle are generic runtime invariants Wirecmd already owns.
3. Which command runs and where each value goes is provider semantics and
   belongs in user configuration.

The proposal therefore treats an executed command as a configured provider with
its own definition, not as a policy engine, workflow language, or sandbox. It
reuses the existing closed secret registry (`env`, `age`) and the existing
`(secret)` annotation.

It touches items the roadmap lists as deferred: secret-file templating and
inherited-environment hardening. Arbitrary secret-provider commands and
provider plugins are not part of this proposal. Wirecmd would run a command as
a consumer of secrets, not as a source of them.

## Proposed shape (illustrative)

KDL 2, no new format. Names and syntax are for discussion.

```kdl
wirecmd {
    exec "deploy-db" {
        command "/usr/bin/psql"
        arg "--host" "db.internal"
        env "PGPASSWORD" (secret)"age://DB_PASSWORD"
    }
    exec "gh-api" {
        command "/usr/bin/gh"
        env "GH_TOKEN" (secret)"age://GITHUB_TOKEN"
    }
}
```

```
wirecmd exec deploy-db -- -c 'select 1'
```

The command is an executable plus structured arguments, never a shell string,
matching the existing stdio provider model. Secrets resolve once per
invocation, are passed to the child only, and are not cached by the daemon.

### Delivery destinations

- **Environment value.** The default and preferred destination. It is absent
  from the command line and from `ps`.
- **Standard input.** For tools that read a secret from stdin
  (`--password-stdin` and similar). One secret per profile.
- **Argument.** Allowed because stdio arguments already accept `(secret)`, but
  documented as the weakest option: the value is visible to same-user process
  listings while the child runs and many tools echo arguments in errors.
- **Rendered file.** A private temporary file (`0600`, in a `0700` directory,
  removed after exit) whose path is exposed through an environment value. This
  overlaps the deferred secret-file templating item and needs its own decision.
  A private mount-namespace overlay for tools with hard-coded config paths is
  Linux-specific and is out of scope here.

### Caller-supplied arguments

Arguments after `--` come from the agent. For a command such as `curl` or a
shell, unrestricted caller arguments let a caller route the secret anywhere.
The proposed default is fail-closed: a profile accepts caller arguments only
when it declares so, and declares nothing else is configurable. Whether to
offer a finer constraint, such as a fixed prefix or a declared argument
schema, is an open question and risks becoming a policy engine.

## Output and redaction

Child stdout and stderr stream through Wirecmd. Before bytes reach the caller,
Wirecmd replaces the resolved values and a fixed set of encodings with a stable
marker:

- the exact value;
- base64 at all three alignments, URL-encoded, JSON-escaped, and hex forms;
- matches that span read boundaries, using a held-back tail of `len - 1` bytes.

This is a tripwire for accidents such as verbose logging or an echoed header,
not a security boundary. It does not catch derived or partial values, hashes,
or secrets the child writes to files the caller later reads. The child's exit
code is preserved. A redaction event emits a diagnostic on stderr that names
the profile and secret reference, never the value. Stdout remains composable.

## Security boundary

The boundary matches the [age secrets plan](../plans/age-secrets-plan.md): the
feature protects against accidental inspection and ordinary diagnostic
leakage. It does not protect against a hostile same-user process, a child that
is authorized to receive the secret, or a secret already inherited through the
environment.

Two additional points need decisions because exec makes them consequential.

### Who can define a profile

Per the [discovery plan](../plans/discovery-plan.md), workspace trust is
recursive and directory-based, and future edits inside a trusted directory are
trusted without re-approval. An agent that can write `.wirecmd/config.kdl` in a
trusted workspace could therefore add a profile whose command prints its own
environment. Options:

- allow `exec` only in global configuration, which is inherently trusted and
  outside the workspace;
- allow workspace `exec` only when a content-bound approval exists, which would
  extend the current trust model; or
- allow workspace `exec` without secrets only.

### Who can decrypt

Identity files are user-managed and never read by Wirecmd. If the identity is
an ordinary readable file, a same-user caller can run `age` itself and bypass
Wirecmd entirely. Hardware-backed identities (TPM PIN, Secure Enclave) already
require local authorization per resolve, which is the out-of-band approval step
an agent cannot grant. The proposal recommends them for any profile holding a
sensitive secret, and the documentation should say so.

## Non-goals

- A command allowlist or policy language beyond the fixed profile.
- Shell strings, pipelines, or a workflow language.
- A sandbox for the child or a claim of protection from a hostile same-user
  process.
- Secret-provider plugins, arbitrary provider commands, or a new
  configuration format.
- Persisting or caching plaintext, including in the daemon.

## Open decisions

1. Is a command-execution family in scope, or should this stay a documented
   pattern around `age` and the shell?
2. Global-only profiles, content-bound workspace approval, or secret-free
   workspace profiles.
3. Default caller-argument policy and whether any finer constraint is worth the
   policy-engine risk.
4. Daemon-backed or direct execution. The invariants say normal operation is
   daemon-backed and never falls back silently. A per-invocation child has no
   state to retain, so the daemon's role would be resolution and approval
   only.
5. Rendered-file delivery and its relationship to the deferred templating item.
6. Redaction encoding set and the stderr diagnostic contract.
7. Stdin delivery: one secret per profile, or a declared binding.
8. Naming: `exec` collides with shell vocabulary; `run` or `invoke` may fit
   better.

## Qualification sketch

If promoted, qualification would use sentinel secrets and cover:

- a child that echoes the value, its base64 form, and a value split across
  reads;
- direct and daemon-backed equivalence, including exit codes and stderr;
- cancellation stopping the whole process group;
- a file-delivery cleanup check after normal exit, signal, and daemon restart;
- an untrusted-workspace profile failing closed; and
- a hardware-backed identity authorization exercise on Linux and Apple
  Silicon, recorded under the existing macOS qualification plan.

## Provenance

This note comes from a design discussion about giving shell-capable agents
credentials without exposing them to the model. It is not backed by an
implementation or by tests, and the encoding and approval behavior described
above are design intent.
