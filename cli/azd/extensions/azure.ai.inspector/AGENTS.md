# Azure AI Inspector Extension - Agent Instructions

Use this file together with `cli/azd/AGENTS.md`. This guide supplements the root azd
instructions with the conventions that are specific to this extension.

## Overview

`azure.ai.inspector` is a first-party azd extension under
`cli/azd/extensions/azure.ai.inspector/`. It runs as a separate Go binary and talks
to the azd host over gRPC.

The single user-facing command is `azd ai inspector launch`. It starts a local HTTP
server that serves an embedded single-page application and proxies HTTP/SSE
traffic to a Foundry agent running locally on the target port.

Useful places to start:

- `internal/cmd/`: Cobra commands and the inspector entry point
- `internal/inspector/`: HTTP server, JSON-RPC over WebSocket, HTTP/SSE proxies,
  embedded SPA assets

## Build and test

From `cli/azd/extensions/azure.ai.inspector`:

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

## Output: `log` vs `fmt`

- **`fmt.Print*`** — user-facing output (stdout). Pair with `output.With*Format`
  helpers for styled text.
- **`log.Print*`** — developer diagnostics (stderr). Hidden unless `--debug`
  is set. Never use `log` for anything the user needs to see.
- Do not use `log.Fatal` or `log.Panic` for expected failures.

## Error handling

This extension does not use an `internal/exterrors` package. Return plain Go
errors, and wrap lower-level failures with `fmt.Errorf("context: %w", err)`
where useful.

Add structured extension errors only if this extension starts needing stable
telemetry categories, error codes, or user-facing suggestions similar to the
`azure.ai.agents` extension.

## SPA assets

The embedded SPA in `internal/inspector/assets/` is a built artifact (Vite output).
It is checked in as-is. When the SPA source is updated, replace the contents of
`internal/inspector/assets/` and bump `version.txt` + `extension.yaml` + add a
CHANGELOG entry.

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
published release artifacts.
