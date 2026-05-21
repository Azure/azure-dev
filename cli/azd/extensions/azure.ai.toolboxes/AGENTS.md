# Azure AI Toolboxes Extension - Agent Instructions

Use this file together with `cli/azd/AGENTS.md`. This guide supplements the root azd
instructions with the conventions that are specific to this extension.

## Overview

`azure.ai.toolboxes` is a first-party azd extension under
`cli/azd/extensions/azure.ai.toolboxes/`. It runs as a separate Go binary and talks
to the azd host over gRPC.

The user-facing surface is `azd ai toolbox <verb>` — CRUD plus a `connection`
subgroup for managing the connection-backed tools attached to a Foundry toolbox.

Useful places to start:

- `internal/cmd/`: Cobra commands, verb implementations, and `connection` subgroup
- `internal/pkg/azure/`: `FoundryToolboxClient` (toolbox CRUD data-plane client)
- `internal/foundry/`: Foundry primitives shared with sibling extensions
  (project-endpoint cascade, credential factory, single-connection lookup)
- `internal/exterrors/`: structured error factories and toolbox-specific codes

## Build and test

From `cli/azd/extensions/azure.ai.toolboxes`:

```bash
# Build using developer extension (for local development)
azd x build

# Or build using Go directly
go build
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

That `replace` points this extension at your local `cli/azd` checkout instead of
the version in `go.mod`. Do not merge the extension with that `replace` still
present.

## Package boundaries

`internal/foundry/` holds Foundry data-plane primitives that are currently
duplicated from `azure.ai.agents` and intended to be lifted into a shared module
later. Two rules keep that future lift mechanical:

1. **One-way import contract.** Files under `internal/foundry/` MUST NOT import
   anything under `internal/cmd/`. Files under `internal/cmd/` may import
   `internal/foundry/...` freely.
2. **No toolbox-specific logic inside `internal/foundry/`.** Keep that package
   to primitives any sibling extension would also need (project-endpoint
   resolution, single-connection lookup, credential factory). Toolbox-specific
   concepts (`projectConnection`, `connectionResolver`, the `FoundryToolboxClient`)
   live in `internal/cmd/` and `internal/pkg/azure/`.

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
- user-facing orchestration code classifies the failure with `exterrors.*`

In this extension, that classification usually happens in `internal/cmd/`.

### Most important rule

Create a structured error once, as close as possible to the place where you know
the final category, code, and suggestion.

If `err` is already a structured error, usually return it unchanged.

Do **not** add context with `fmt.Errorf("context: %w", err)` after `err` is
already structured. During gRPC serialization, azd preserves the structured
error's own message/code/category, not the outer wrapper text. If you need extra
context, include it in the structured error message when you create it.

### Choosing an Error Type

| Situation | Prefer |
| --- | --- |
| Invalid input, manifest, or option combination | `exterrors.Validation` |
| Missing environment value, missing resource, unavailable dependency | `exterrors.Dependency` |
| Auth or tenant/credential failure | `exterrors.Auth` |
| User cancellation | `exterrors.Cancelled` |
| Azure SDK HTTP failure | `exterrors.ServiceFromAzure` |
| gRPC failure from an azd host Prompt call | `exterrors.FromPrompt` |
| Unexpected bug or local failure with no better category | `exterrors.Internal` |

### Recommended pattern

```go
func parseInput(path string) error {
    if err := readFile(path); err != nil {
        return fmt.Errorf("read %s: %w", path, err)
    }

    return nil
}

func runCommand() error {
    if err := parseInput("tools.json"); err != nil {
        return exterrors.Validation(
            exterrors.CodeInvalidParameter,
            fmt.Sprintf("invalid input file: %s", err),
            "fix the file and retry",
        )
    }

    return nil
}
```

### Azure and gRPC boundaries

Prefer the dedicated helpers instead of hand-rolling conversions:

- `exterrors.ServiceFromAzure(err, operation)` for `azcore.ResponseError`
  (data-plane and ARM calls).
- `exterrors.FromPrompt(err, contextMessage)` for `azdClient.Prompt().*`
  failures.

These helpers keep telemetry and user-facing behavior consistent.

### Error codes

Define new codes in `internal/exterrors/codes.go`.

- use lowercase `snake_case`
- describe the specific failure, not the general category
- keep them stable once introduced
- co-locate any new `Op*` constant with the matching `ServiceFromAzure` call site

## Release preparation

A new extension release ships in two PRs:

### PR 1 — Version bump

Bumps the extension to the new version. Touches only:

- `version.txt` — new semver string
- `extension.yaml` — `version:` field
- `CHANGELOG.md` — new release section at the top

Once merged, the team triggers the CI release pipeline, which builds, signs, and
publishes the extension binaries as a GitHub release.

### PR 2 — Registry update

After the GitHub release is live, a follow-up PR updates
`cli/azd/extensions/registry.json` so azd users can install the new version.
The contents of that file are produced by running `azd x publish` against the
published release artifacts (which computes the artifact URLs and checksums).
The resulting PR should contain only the regenerated `registry.json` entry for
the extension, and in some cases updated test snapshots as well.

## Output: `log` vs `fmt`

Extensions write directly to stdout/stderr — there is no `Console` abstraction
from azd core.

- **`fmt.Print*`** — user-facing output (stdout).
- **`log.Print*`** — developer diagnostics (stderr). Hidden unless `--debug`
  is set. Never use `log` for anything the user needs to see.
- Do not use `log.Fatal` or `log.Panic` for expected failures — return a
  structured error via `exterrors` instead.

```go
// ✅ log — internal detail the user doesn't need to see
log.Printf("toolbox show: pending-toolbox read failed for %q: %v", name, err)

// ✅ fmt — user-facing status and results
fmt.Printf("Created toolbox %s at version %s.\n", name, version)

// ❌ fmt used for debug noise — user sees internal details they can't act on
fmt.Printf("Parsed endpoint: host=%s, path=%s\n", host, path)  // use log.Printf

// ❌ log used for user-facing info — user never sees it without --debug
log.Printf("No toolboxes found on project")                    // use fmt.Print*
```

## Other extension conventions

- Use modern Go 1.26 patterns where they help readability.
- Reserved azd globals (`--output`, `--no-prompt`) are inherited from `extCtx`,
  not registered as flags on each verb.
- Lowercase-normalize `--output` when reading it from `extCtx` so downstream
  branches can compare with `== "json"`.
