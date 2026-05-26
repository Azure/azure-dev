# Azure AI Routines Extension - Agent Instructions

Use this file together with `cli/azd/AGENTS.md`. This guide supplements the root azd
instructions with the conventions that are specific to this extension.

## Overview

`azure.ai.routines` is a first-party azd extension under
`cli/azd/extensions/azure.ai.routines/`. It runs as a separate Go binary and talks
to the azd host over gRPC.

The user-facing surface is `azd ai routine <verb>` — CRUD over Microsoft Foundry
Routines attached to a Foundry project endpoint.

Useful places to start:

- `internal/cmd/`: Cobra commands and verb implementations
- Project-endpoint resolution comes from the sibling `azure.ai.projects`
  extension (and the shared cascade); do not re-implement it here.

## Build and test

From `cli/azd/extensions/azure.ai.routines`:

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

That `replace` points this extension at your local `cli/azd` checkout instead of
the version in `go.mod`. Do not merge the extension with that `replace` still
present.

## Error handling

Return plain Go errors by default, and wrap lower-level failures with
`fmt.Errorf("context: %w", err)` where useful.

If this extension grows enough to need stable telemetry categories, error codes,
or user-facing suggestions, introduce an `internal/exterrors` package modeled on
the one in `azure.ai.agents` / `azure.ai.toolboxes`:

- Create a structured error once, as close as possible to the place where you
  know the final category, code, and suggestion.
- If `err` is already a structured error, return it unchanged. Do **not** wrap
  it with `fmt.Errorf("context: %w", err)` — during gRPC serialization, azd
  preserves the structured error's own message/code/category, not the outer
  wrapper text.
- Prefer the dedicated helpers at the Azure/gRPC boundary:
  - `exterrors.ServiceFromAzure(err, operation)` for `azcore.ResponseError`
    (data-plane and ARM calls).
  - `exterrors.FromPrompt(err, contextMessage)` for `azdClient.Prompt().*`
    failures.

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

- **`fmt.Print*`** — user-facing output (stdout). Pair with `output.With*Format`
  helpers for styled text.
- **`log.Print*`** — developer diagnostics (stderr). Hidden unless `--debug`
  is set. Never use `log` for anything the user needs to see.
- Do not use `log.Fatal` or `log.Panic` for expected failures — return an error
  instead.

```go
// ✅ log — internal detail the user doesn't need to see
log.Printf("routine show: pending-routine read failed for %q: %v", name, err)

// ✅ fmt — user-facing status and results
fmt.Printf("Created routine %s at version %s.\n", name, version)

// ❌ fmt used for debug noise — user sees internal details they can't act on
fmt.Printf("Parsed endpoint: host=%s, path=%s\n", host, path)  // use log.Printf

// ❌ log used for user-facing info — user never sees it without --debug
log.Printf("No routines found on project")                     // use fmt.Print*
```

## Other extension conventions

- Use modern Go 1.26 patterns where they help readability.
- Reserved azd globals (`--output`, `--no-prompt`) are inherited from `extCtx`,
  not registered as flags on each verb.
- Lowercase-normalize `--output` when reading it from `extCtx` so downstream
  branches can compare with `== "json"`.
- When using `PromptSubscription()`, create credentials with
  `Subscription.UserTenantId`, not `Subscription.TenantId`.

## API spec alignment

The authoritative TypeSpec is in
[`azure-rest-api-specs` PR #43186](https://github.com/Azure/azure-rest-api-specs/pull/43186)
(`specification/ai-foundry/data-plane/Foundry/src/routines/`). The client in
`internal/pkg/routines/` tracks that spec, with a small number of remaining
divergences kept for compatibility with the currently deployed Foundry service:

| Concern | Spec | Live service | Client choice |
|---|---|---|---|
| `github_issue_opened` trigger | renamed in spec | still accepts only `github_issue` | keep `github_issue` wire value (CLI surface is deferred) |
| `AgentsPagedResult<T>` envelope | `data` + `last_id` + `has_more` | `value` + `nextLink` (routines) / `value` + `nextPageToken` (runs) | match service shape |

