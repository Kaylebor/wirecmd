# MCP Resources

Status: authoritative completed MCP resources slice

## Contract

Wirecmd exposes configured MCP resources through explicit operations beneath
the MCP namespace:

```text
wirecmd mcp SERVER resources
wirecmd mcp SERVER resource-templates
wirecmd mcp SERVER resource URI
```

These operations do not alter tool invocation or exact-call-envelope syntax.
They accept no tool JSON input or projected arguments. Resource and template
lists are deterministic: resources sort by URI then name, and templates sort
by URI template then name. A successful empty list remains successful.

Resource reads retain the upstream content order. Text content is emitted as
text. Binary content is emitted as base64 alongside its URI and MIME type.
Before re-encoding binary data, Wirecmd redacts every known resolved secret.
Resource identifiers retain their useful scheme, authority, path, and template
structure, but redact query values, fragments, and userinfo before output.
URI-template expressions such as `{?id}` remain intact. Malformed base64,
distinguishable contradictory content shapes, and missing resource capability
are structured upstream-protocol failures. The SDK deliberately represents a
URI-only content object and present-empty text with the same public zero-value
shape; Wirecmd preserves that shape as empty text instead of parsing MCP wire
JSON alongside the SDK.

Listing metadata is redacted against all URI-derived sensitive components in
that response; resource reads additionally redact against the requested URI and
each returned content URI. These response-local scopes never alter a retained
daemon instance's base redactor or later tool payloads.

## Ownership and lifecycle

The pinned official Go MCP SDK owns resource pagination, result caching,
transport, authorization, negotiation, and session shutdown. Wirecmd calls
`ClientSession.Resources`, `ResourceTemplates`, and `ReadResource`; it does
not implement cursor handling, caching, or revision-specific protocol logic.

Direct mode opens one session and closes it after the operation. Daemon mode
uses the existing retained selected-server session, serialization, redaction,
credential identity, configuration mismatch, cancellation, reload, and broken
instance behavior. The private daemon protocol includes normalized operation
and URI fields only; SDK wire types never cross it.

## Deferred

Prompts, subscriptions, Tasks, sampling, richer elicitation, resource-specific
completion, caching policy controls, and provider-specific shims remain
separate work. Resource support does not authorize a local MCP transport,
pagination, cache, or OAuth implementation.
