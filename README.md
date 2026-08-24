# Wire Command (`wirecmd`)

This repository explores a shell-native capability runtime for agents, humans,
scripts, and CI. Its first upstream adapter is the Model Context Protocol
(MCP), but MCP is not intended to be part of the harness-facing contract.

The intended dependency direction is:

```text
agent / human / automation
          |
          v
        shell
          |
          v
 capability CLI and optional local daemon
          |
          v
   MCP servers and future adapters
```

A shell-capable agent should be able to discover capabilities lazily, inspect
only the help and schemas it needs, invoke them deterministically, and compose
results with ordinary shell tools. A harness should not need native MCP
integration or eager injection of every configured tool schema.

The canonical Go module and repository path is
`github.com/Kaylebor/wirecmd`. Once releases exist, the command is intended to
be installable with:

```sh
go install github.com/Kaylebor/wirecmd@latest
```

The module path deliberately does not depend on a vanity domain. A project
website such as `wirecmd.dev` may be added independently later.

The project is currently in pre-implementation validation. The first milestone
is to test the agent-facing interaction model, not to deliver broad MCP feature
parity.

## Project documents

- [Product thesis](docs/product-thesis.md) defines the authoritative product
  direction and boundaries.
- [Validation plan](docs/validation-plan.md) defines the first falsifiable
  implementation spike.
- [Exploratory design notes](docs/notes/exploratory-design.md) retain ideas and
  research that are useful but not committed requirements.

Repository working instructions are in [AGENTS.md](AGENTS.md).
