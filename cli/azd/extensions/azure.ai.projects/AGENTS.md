# Azure AI Projects Extension - Agent Instructions

Use this file together with `cli/azd/AGENTS.md`. This guide supplements the root azd instructions with the conventions that are specific to this extension.

## Overview

`azure.ai.projects` is a first-party azd extension under `cli/azd/extensions/azure.ai.projects/`. It runs as a separate Go binary and talks to the azd host over gRPC.

It owns the `azure.ai.project` service target and the `microsoft.foundry` provisioning provider. The provider handles greenfield and existing-project provisioning, preview, and teardown.

It owns the Foundry project endpoint context used by other AI extensions (e.g. `azure.ai.agents`). The `azd ai project` commands persist, resolve, and surface the endpoint through a 5-level cascade:

1. Explicit `--project-endpoint` flag
2. Active azd env's `AZURE_AI_PROJECT_ENDPOINT`
3. Global config: `extensions.ai-projects.context.endpoint` in `~/.azd/config.json`
4. Host environment variable `FOUNDRY_PROJECT_ENDPOINT`
5. Structured error with actionable suggestion

The resolver also performs a one-time auto-migration from the legacy `extensions.ai-agents.project.context` key (written by the removed `azd ai agent project set` command) into the new key.

Useful places to start:

- `internal/cmd/`: Cobra commands, the endpoint resolver, and the config store
- `internal/provisioning/`: the `microsoft.foundry` provider
- `internal/synthesis/`: project config synthesis and embedded IaC
- `internal/exterrors/`: structured error factories and extension-specific codes

During the staged ownership migration, `internal/synthesis/` is also present in `azure.ai.agents` for `azd ai agent init --infra`. Keep the production files and templates byte-for-byte aligned until init moves.

## Build and test

From `cli/azd/extensions/azure.ai.projects`:

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
- user-facing orchestration code in `internal/cmd/` classifies the failure with `exterrors.*`

### Most important rule

Create a structured error once, as close as possible to the place where you know the final category, code, and suggestion.

If `err` is already a structured error, usually return it unchanged.

Do **not** add context with `fmt.Errorf("context: %w", err)` after `err` is already structured. During gRPC serialization, azd preserves the structured error's own message/code/category, not the outer wrapper text. If you need extra context, include it in the structured error message when you create it.

### Choosing an Error Type

| Situation | Prefer |
| --- | --- |
| Invalid endpoint, flag value, or persisted blob | `exterrors.Validation` |
| Missing endpoint across all 5 resolver levels, unavailable azd daemon | `exterrors.Dependency` |
| Auth or tenant/credential failure | `exterrors.Auth` |
| azd/extension version or capability mismatch | `exterrors.Compatibility` |
| User cancellation | `exterrors.Cancelled` |
| Azure SDK HTTP failure | `exterrors.ServiceFromAzure` |
| Unexpected bug or local failure with no better category | `exterrors.Internal` |

### Recommended pattern

```go
func loadEndpoint(raw string) (string, error) {
    normalized, _, err := validateProjectEndpoint(raw)
    if err != nil {
        return "", fmt.Errorf("validate %q: %w", raw, err)
    }

    return normalized, nil
}

func runCommand() error {
    endpoint, err := loadEndpoint(rawFlag)
    if err != nil {
        return exterrors.Validation(
            exterrors.CodeInvalidParameter,
            fmt.Sprintf("project endpoint is invalid: %s", err),
            "provide an https:// Foundry project endpoint URL",
        )
    }

    _ = endpoint
    return nil
}
```

### Error codes

Define new codes in `internal/exterrors/codes.go`.

- use lowercase `snake_case`
- describe the specific failure, not the general category
- keep them stable once introduced

## Persisted project context

The endpoint store lives at `extensions.ai-projects.context` in `~/.azd/config.json` and is accessed exclusively through helpers in `internal/cmd/project_context_store.go`:

- `getProjectContext` / `setProjectContext` / `clearProjectContext` — public surface
- `readProjectContext` / `writeMigratedProjectContext` / `clearProjectContextFromConfig` — internal helpers that take a `projectContextConfig` interface so tests can drive them with a fake

When changing the store:

- Keep reads of the legacy `extensions.ai-agents.project.context` key best-effort: a malformed legacy blob must never block resolution from the new key, the flag, or `FOUNDRY_PROJECT_ENDPOINT`.
- `clearProjectContext` must remain idempotent and must clear both the new and legacy keys, even when the previous-endpoint read fails (so users can always recover from a corrupted persisted blob).
- The auto-migration in `readAzdHostedSources` is best-effort: a transient write failure must not break the command the user actually invoked.

## Release preparation

A new extension release ships in two PRs:

### Provider handoff release

The first release that moves `microsoft.foundry` here must be coordinated with the matching `azure.ai.agents` release. Publish both artifacts before updating either registry entry, then update both entries and the `microsoft.foundry` meta-package together. Old agents and new projects versions cannot run together because azd rejects duplicate provider registration.

### PR 1 — Version bump

Bumps the extension to the new version. Touches only:

- `version.txt` — new semver string
- `extension.yaml` — `version:` field
- `CHANGELOG.md` — new release section at the top

Once merged, the team triggers the CI release pipeline, which builds, signs, and publishes the extension binaries as a GitHub release.

### PR 2 — Registry update

After the GitHub release is live, a follow-up PR updates `cli/azd/extensions/registry.json` so azd users can install the new version. The contents of that file are produced by running `azd x publish` against the published release artifacts.

## Output: `log` vs `fmt`

Extensions write directly to stdout/stderr — there is no `Console` abstraction from azd core.

- **`fmt.Print*`** — user-facing output (stdout). Pair with `output.With*Format` helpers for styled text.
- **`log.Print*`** — developer diagnostics (stderr). Hidden unless `--debug` is set. Never use `log` for anything the user needs to see.
- Do not use `log.Fatal` or `log.Panic` for expected failures — return a structured error via `exterrors` instead.

```go
// ✅ log — internal detail the user doesn't need to see
log.Printf("config read at %s returned %d bytes", path, n)

// ✅ fmt — user-facing status and results
fmt.Println(output.WithSuccessFormat("Project endpoint set to %s", endpoint))

// ❌ fmt used for debug noise — user sees internal details they can't act on
fmt.Printf("normalized URL: %s\n", normalized)    // use log.Printf

// ❌ log used for user-facing info — user never sees it without --debug
log.Printf("No project endpoint resolved")        // return an exterrors.Dependency instead
```

## Other extension conventions

- Use modern Go 1.26 patterns where they help readability
- When using `PromptSubscription()`, create credentials with `Subscription.UserTenantId`, not `Subscription.TenantId`
