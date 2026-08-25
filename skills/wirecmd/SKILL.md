---
name: wirecmd
description: Discover and compose Wirecmd capabilities from the shell, including trusted workspace configuration, focused help, projected arguments, and lossless JSON calls.
---

# Wirecmd

Use Wirecmd as a shell-native capability interface. Keep client flags before
the server and tool names. Treat ordinary operation output as JSON suitable for
inspection, piping, and scripting; send no assumptions about the upstream
protocol into a call.

## Select configuration

When `--config` is omitted, Wirecmd loads the global KDL file at
an absolute `$XDG_CONFIG_HOME/wirecmd/config.kdl`, falling back to
`~/.config/wirecmd/config.kdl`, then composes trusted workspace `wirecmd.kdl`
files from the trusted root to the current directory. Repeated `--config PATH`
options replace discovery completely and preserve their weakest-to-strongest
order.

Manage trust explicitly when a workspace is not yet approved:

```sh
wirecmd config trust [PATH]
wirecmd config untrust [PATH]
wirecmd config trust status [PATH]
wirecmd config trust list
```

The optional path defaults to the current directory. A missing approval is the
recoverable `workspace_untrusted` error (exit 8); no discovered source is
`config_not_found` (exit 3). These commands remain non-interactive. A server
named `config` can still be called through `--json` or an exact-call envelope.

## Discover before calling

Start by listing configured servers, then list the selected server's tools.
Request focused help for a tool before guessing its input shape:

```sh
wirecmd
wirecmd SERVER
wirecmd --help SERVER TOOL
```

Use `--config PATH` in these forms when an explicit source list is required.

Focused help is readable text. It identifies simple projected flags, the
original JSON names and types, and properties that need JSON input or a
fallback path.

## Choose the smallest clear call form

Use projected flags for straightforward top-level values:

```sh
wirecmd --config PATH SERVER TOOL --query 'text' --limit 10 --enabled
```

For a named complex value, pass one JSON value to its documented flag:

```sh
wirecmd --config PATH SERVER TOOL --filters '{"status":["open"]}'
```

Use `--` followed by exactly one JSON object to add properties that cannot be
projected uniquely, such as name-normalization collisions. Do not repeat a key
already supplied by a projected flag:

```sh
wirecmd --config PATH SERVER TOOL --query 'text' -- '{"toolName":"value"}'
```

When the full object is clearer, use a lossless JSON path instead:

```sh
wirecmd --config PATH --json '{"query":"text"}' SERVER TOOL
printf '%s\n' '{"query":"text"}' | wirecmd --config PATH --stdin SERVER TOOL
wirecmd --config PATH SERVER '{"tool":"TOOL","arguments":{"query":"text"}}'
```

## Compose and recover

Pipe ordinary JSON results through standard shell tools when it makes the next
step clearer. Keep calls non-interactive and inspect the structured error
envelope and exit status when one fails; its category, code, and action indicate
whether to correct invocation, configuration, authentication, transport, or an
upstream tool failure.

Normal calls require the local daemon. Use `--direct` only for deliberate
one-shot testing or diagnostics; it does not retain server state between
invocations.
