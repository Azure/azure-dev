# Azure AI Connections Extension - Agent Instructions

Use this file together with `cli/azd/AGENTS.md`. This guide supplements the
root azd instructions with the conventions that are specific to this extension.

## Overview

`azure.ai.connections` is a first-party azd extension under
`cli/azd/extensions/azure.ai.connections/`. It runs as a separate Go binary and
talks to the azd host over gRPC.

The user-facing surface is `azd ai connection <verb>` — `list`, `show`,
`create`, `update`, `delete` against the connection resources attached to a
Foundry project.

Useful places to start:

- `internal/cmd/`: extension boilerplate (context, version, metadata) plus the
  root command that wires in the connection subcommands.
- `internal/connections/cmd/`: connection CRUD verb implementations, ARM/
  data-plane glue (`resolveConnectionContext`, `discoverARMContext`).
- `internal/connections/pkg/connections/`: Foundry data-plane client and
  credential models.
- `internal/foundry/projectctx/`: Foundry project-endpoint cascade and URL
  validation shared with sibling extensions.
- `internal/exterrors/`: structured error factories and extension-specific codes.

## Package boundaries

`internal/foundry/` holds Foundry primitives that are currently duplicated
from `azure.ai.toolboxes` / `azure.ai.agents` / `azure.ai.projects` and intended
to be lifted into a shared module later. Two rules keep that future lift
mechanical:

1. **One-way import contract.** Files under `internal/foundry/` MUST NOT
   import anything under `internal/cmd/` or `internal/connections/`. Files
   under `internal/cmd/` and `internal/connections/cmd/` may import
   `internal/foundry/...` freely.
2. **No connection-specific logic inside `internal/foundry/`.** Keep that
   package to primitives any sibling Foundry extension would also need
   (project-endpoint resolution, URL validation). Connection-specific
   concepts (data-plane models, ARM resource-ID parsing, the connection
   ARM/data-plane clients) live in `internal/connections/` and must not
   leak into `internal/foundry/`.

When this primitive is lifted into a shared module (e.g. promoted out of
`azure.ai.projects` into an exported `pkg/projectctx`), this extension should
drop its `internal/foundry/projectctx/` copy and import the shared package
without any other change.

## Build and test

From `cli/azd/extensions/azure.ai.connections`:

```bash
# Build using developer extension (for local development)
azd x build

# Or build using Go directly
go build

# Run unit tests
go test ./... -count=1
```

If extension work depends on a new azd core change, plan for two PRs:

1. Land the core change in `cli/azd` first.
2. Land the extension change after that, updating this module to the newer azd
   dependency with `go get github.com/azure/azure-dev/cli/azd && go mod tidy`.

For local development, draft work, or validating both sides together before the
core PR is merged, you may temporarily add:

```go
replace github.com/azure/azure-dev/cli/azd => ../../
```

That `replace` points this extension at your local `cli/azd` checkout instead
of the version in `go.mod`. Do not merge the extension with that `replace`
still present.

## Error handling

This extension uses `internal/exterrors` so the azd host can show a useful
message, attach an optional suggestion, and emit stable telemetry.

### Default rule

Use plain Go errors by default. Switch to `exterrors.*` only when the current
code can confidently answer all three of these:

1. What category should telemetry see?
2. What stable error code should be recorded?
3. What suggestion, if any, should the user get?

That usually means:

- lower-level helpers return `fmt.Errorf("context: %w", err)`
- user-facing orchestration code in `internal/connections/cmd/` classifies the
  failure with `exterrors.*`

### Most important rule

Create a structured error once, as close as possible to the place where you
know the final category, code, and suggestion.

If `err` is already a structured error, return it unchanged. Do **not** add
context with `fmt.Errorf("context: %w", err)` after `err` is already
structured — during gRPC serialization the azd host preserves the structured
error's own message/code/category, not the outer wrapper text. If you need
extra context, include it in the structured error message at construction.

### Choosing an Error Type

| Situation | Prefer |
| --- | --- |
| Invalid input, flag value, or auth-type combination | `exterrors.Validation` |
| Missing project endpoint, missing connection, unavailable daemon | `exterrors.Dependency` |
| Auth or credential failure | `exterrors.Auth` |
| Azure SDK HTTP failure (ARM or data plane) | `exterrors.ServiceFromAzure` |
| Unexpected bug or local failure with no better category | plain `fmt.Errorf` |

### Error codes

Define new codes in `internal/exterrors/codes.go`.

- use lowercase `snake_case`
- describe the specific failure, not the general category
- keep them stable once introduced
- co-locate any new `Op*` constant with its `ServiceFromAzure` call site

## Output: `log` vs `fmt`

Extensions write directly to stdout/stderr — there is no `Console`
abstraction from azd core.

- **`fmt.Print*`** — user-facing output (stdout).
- **`log.Print*`** — developer diagnostics (stderr). Hidden unless `--debug`
  is set. Never use `log` for anything the user needs to see.
- Do not use `log.Fatal` or `log.Panic` for expected failures — return a
  structured error via `exterrors` instead.

## Other extension conventions

- Use modern Go 1.26 patterns where they help readability.
- Reserved azd globals (`--output`, `--no-prompt`) are inherited from `extCtx`,
  not registered as flags on each verb.
- The `-p / --project-endpoint` flag is registered once as a persistent flag
  on the extension root command and inherited by every connection subcommand.
