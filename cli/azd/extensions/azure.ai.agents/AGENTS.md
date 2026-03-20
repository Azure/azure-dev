# Azure AI Agents Extension - Agent Instructions

Use this file together with `cli/azd/AGENTS.md`. This guide supplements the root azd instructions with the conventions that are specific to this extension.

## Overview

`azure.ai.agents` is a first-party azd extension under `cli/azd/extensions/azure.ai.agents/`. It runs as a separate Go binary and talks to the azd host over gRPC.

Useful places to start:

- `internal/cmd/`: Cobra commands and top-level orchestration
- `internal/project/`: project/service target integration and deployment flows
- `internal/pkg/`: lower-level helpers, parsers, and API-facing logic
- `internal/exterrors/`: structured error factories and extension-specific codes

## Build and test

From `cli/azd/extensions/azure.ai.agents`:

```bash
# Build using developer extension (for local development)
azd x build

# Or build using Go directly
go build
```

If extension work depends on a new azd core change, plan for two PRs:

1. Land the core change in `cli/azd` first.
2. Land the extension change after that, updating this module to the newer azd dependency with `go get github.com/azure/azure-dev/cli/azd && go mod tidy`.

For local development, draft work, or validating both sides together before the core PR is merged, you may temporarily add:

```go
replace github.com/azure/azure-dev/cli/azd => ../../
```

That `replace` points this extension at your local `cli/azd` checkout instead of the version in `go.mod`. Do not merge the extension with that `replace` still present.

## Error handling

This extension uses `internal/exterrors` so the azd host can show a useful message, attach an optional suggestion, and emit stable telemetry.

### Default rule

Use plain Go errors by default. Switch to `exterrors.*` only when the current code can confidently answer all three of these:

1. What category should telemetry see?
2. What stable error code should be recorded?
3. What suggestion, if any, should the user get?

That usually means:

- lower-level helpers return `fmt.Errorf("context: %w", err)`
- user-facing orchestration code classifies the failure with `exterrors.*`

In this extension, that classification often happens in `internal/cmd/` and `internal/project/`, not only in Cobra `RunE` handlers.

### Most important rule

Create a structured error once, as close as possible to the place where you know the final category, code, and suggestion.

If `err` is already a structured error, usually return it unchanged.

Do **not** add context with `fmt.Errorf("context: %w", err)` after `err` is already structured. During gRPC serialization, azd preserves the structured error's own message/code/category, not the outer wrapper text. If you need extra context, include it in the structured error message when you create it.

### Choosing an Error Type

| Situation | Prefer |
| --- | --- |
| Invalid input, manifest, or option combination | `exterrors.Validation` |
| Missing environment value, missing resource, unavailable dependency | `exterrors.Dependency` |
| Auth or tenant/credential failure | `exterrors.Auth` |
| azd/extension version or capability mismatch | `exterrors.Compatibility` |
| User cancellation | `exterrors.Cancelled` |
| Azure SDK HTTP failure | `exterrors.ServiceFromAzure` |
| gRPC failure from azd host AI/prompt calls | `exterrors.FromAiService` / `exterrors.FromPrompt` |
| Unexpected bug or local failure with no better category | `exterrors.Internal` |

### Recommended pattern

```go
func loadThing(path string) error {
    if err := parse(path); err != nil {
        return fmt.Errorf("parse %s: %w", path, err)
    }

    return nil
}

func runCommand() error {
    if err := loadThing("agent.yaml"); err != nil {
        return exterrors.Validation(
            exterrors.CodeInvalidAgentManifest,
            fmt.Sprintf("agent manifest is invalid: %s", err),
            "fix the manifest and retry",
        )
    }

    return nil
}
```

### Azure and gRPC boundaries

Prefer the dedicated helpers instead of hand-rolling conversions:

- `exterrors.ServiceFromAzure(err, operation)` for `azcore.ResponseError`
- `exterrors.FromAiService(err, fallbackCode)` for azd host AI service calls
- `exterrors.FromPrompt(err, contextMessage)` for prompt failures

These helpers keep telemetry and user-facing behavior consistent.

### Error codes

Define new codes in `internal/exterrors/codes.go`.

- use lowercase `snake_case`
- describe the specific failure, not the general category
- keep them stable once introduced

## Release preparation

When bumping the extension version for a patch release, update **only** these files:

- `version.txt` — new semver string
- `extension.yaml` — `version:` field
- `CHANGELOG.md` — new release section at the top

**Do NOT update `cli/azd/extensions/registry.json`.** The registry entry (checksums, artifact URLs) is generated automatically by CI after the release build produces the binaries. Editing it manually by hand will result in wrong or placeholder checksums that break installation.

## Other extension conventions

- Use modern Go 1.26 patterns where they help readability
- When using `PromptSubscription()`, create credentials with `Subscription.UserTenantId`, not `Subscription.TenantId`
