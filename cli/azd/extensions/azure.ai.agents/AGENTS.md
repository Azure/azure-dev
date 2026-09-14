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

## Interactive CLI test scenarios

This extension ships a suite of goal-based scenarios for the
[cli-interactive-tester](https://github.com/coreai-microsoft/cli-interactive-tester)
MCP server under `tests/cli-interactive-tester-scenarios/`. They drive real
`azd ai agent` flows end-to-end (init, provision, deploy, invoke, run, sessions,
files, monitor, endpoint, doctor, down) and are organized by tier:

- **Tier 0** — offline, no Azure auth, no cost (help, version, validation, picker UX)
- **Tier 1** — local-only with Azure auth (init flows)
- **Tier 1b** — deploy-verify: provisions a Tier 1 scaffold to confirm it deploys (incurs Azure cost)
- **Tier 2** — full cloud E2E against a deployed shared agent (incurs Azure cost)

Each scenario carries a set of tags based on what is being tested and how.
See `tests/cli-interactive-tester-scenarios/README.md` for the tag taxonomy,
profile setup, and the human authoring contract, and
`tests/cli-interactive-tester-scenarios/driving-mechanics.md` for the executor
mechanics. Runs are driven through the `foundry-extension-scenario-orchestrator` agent — it routes
to the `foundry-extension-scenario-pr-regression` and `foundry-extension-scenario-suite-run` skills and fans work out
to `foundry-extension-scenario-worker` agents; new scenarios are authored through the
`foundry-extension-scenario-author` agent / `foundry-extension-scenario-authoring` skill.

### Guidance for coding agents

These scenarios are **never run automatically** — they require the
cli-interactive-tester MCP server, a populated `profile.local.yaml`, and
(for Tier 2) real Azure resources. Do not invoke them on your own. Instead:

1. **Surface them to the user** when you make a change that touches a
   user-facing command path covered by an existing scenario (anything under
   `internal/cmd/` that maps to a `cmd:*` tag, or shared helpers used by those
   commands). In your summary, point the user at the relevant scenario(s)
   and suggest they validate the change by selecting the `foundry-extension-scenario-orchestrator`
   agent (for a PR, the `foundry-extension-scenario-pr-regression` skill maps the diff to the
   matching tag set automatically).

2. **Add or update a scenario** when your change introduces a new command,
   flag, prompt, or user-visible flow — or meaningfully alters an existing
   one. Use the `foundry-extension-scenario-author` agent (or the `foundry-extension-scenario-authoring` skill) to
   place the new YAML alongside the others following the tagging taxonomy and
   authoring contract, and mention the new/changed scenario in the PR
   description so reviewers know to exercise it.

3. **Do not modify scenarios to match buggy behavior.** Scenarios are
   user-facing specifications of how the command should behave; if a scenario
   fails because of your change, prefer fixing the code unless the behavior
   change is intentional and documented.

## Documentation examples

`azure.yaml` examples in this extension's markdown docs are validated by
`TestDocExamplesAreValid` (`internal/project/doc_examples_test.go`). Every fenced
YAML block declaring an `azure.ai.agent` service must:

1. Resolve through `AgentDefinitionFromService` without error, and
2. Satisfy `schemas/azure.ai.agent.json`, including required fields, types,
   enums, patterns, and declared properties, and
3. Parse core-owned service fields (`docker`, `k8s`, `infra`, `hooks`, and the
   scalar fields) with the same YAML shapes azd core expects.

azd ignores unknown service properties at runtime, so an undocumented key
deploys cleanly while doing nothing — the test blocks that in our docs.

Snippets that are deliberately incomplete (for example, the network examples in
`docs/private-networking.md`, which omit `kind` because azd falls back to the
on-disk `agent.yaml`) opt out of the "must fully resolve" check with a marker on
the line before the fence:

````markdown
<!-- azd:doc-example partial -->
```yaml
services:
  my-agent:
    host: azure.ai.agent
```
````

Use the marker only when the snippet is intentionally partial. If a complete
example fails, fix the example rather than adding the marker.

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
- `exterrors.FromHost(err, operation, context)` for azd host gRPC service calls
- `exterrors.FromAiService(err, fallbackCode)` for azd host AI service calls
- `exterrors.FromPrompt(err, contextMessage)` for prompt failures

These helpers keep telemetry and user-facing behavior consistent.

### Error codes

Define new codes in `internal/exterrors/codes.go`.

- use lowercase `snake_case`
- describe the specific failure, not the general category
- keep them stable once introduced

## Release preparation

A new extension release ships in two PRs:

### Provider handoff release

The release that removes `microsoft.foundry` from this extension must be coordinated with the provider-bearing `azure.ai.projects` release. Publish both artifacts before updating either registry entry, then update both entries and the `microsoft.foundry` meta-package together.

### PR 1 — Version bump

Bumps the extension to the new version. Touches only:

- `version.txt` — new semver string
- `extension.yaml` — `version:` field
- `CHANGELOG.md` — new release section at the top

Once merged, the team triggers the CI release pipeline, which builds, signs, and publishes the extension binaries as a GitHub release.

### PR 2 — Registry update

After the GitHub release is live, a follow-up PR updates `cli/azd/extensions/registry.json` so azd users can install the new version. The contents of that file are produced by running `azd x publish` against the published release artifacts (which computes the artifact URLs and checksums). The resulting PR should contain only the regenerated `registry.json` entry for the extension, and in some cases updated test snapshots as well.

## Output: `log` vs `fmt`

Extensions write directly to stdout/stderr — there is no `Console` abstraction from azd core.

- **`fmt.Print*`** — user-facing output (stdout). Pair with `output.With*Format` helpers for styled text.
- **`log.Print*`** — developer diagnostics (stderr). Hidden unless `--debug` is set. Never use `log` for anything the user needs to see.
- Do not use `log.Fatal` or `log.Panic` for expected failures — return a structured error via `exterrors` instead.

```go
// ✅ log — internal detail the user doesn't need to see
log.Printf("ARM response: status=%d, id=%s", resp.StatusCode, resourceId)

// ✅ fmt — user-facing status and results
fmt.Println(output.WithSuccessFormat("Agent deployed to %s", endpoint))

// ❌ fmt used for debug noise — user sees internal details they can't act on
fmt.Printf("Parsed resource ID: sub=%s, rg=%s\n", subId, rg)    // use log.Printf

// ❌ log used for user-facing info — user never sees it without --debug
log.Printf("No Foundry projects found in subscription")          // use fmt + output helper
```

## Other extension conventions

- Use modern Go 1.26 patterns where they help readability
- When using `PromptSubscription()`, create credentials with `Subscription.UserTenantId`, not `Subscription.TenantId`
