# Azure AI Skills Extension - Agent Instructions

Use this file together with `cli/azd/AGENTS.md`. This guide supplements the root azd instructions with the conventions that are specific to this extension.

## Overview

`azure.ai.skills` is a first-party azd extension under `cli/azd/extensions/azure.ai.skills/`. It runs as a separate Go binary and talks to the azd host over gRPC. It exposes the `azd ai skill <verb>` command group for managing Foundry Skills.

Useful places to start:

- `internal/cmd/`: Cobra commands and top-level orchestration
- `internal/pkg/skill_api/`: typed Foundry Skills REST client, models, SKILL.md parser, and safe ZIP extractor
- `internal/exterrors/`: structured error factories and extension-specific codes

## Relationship to `azure.ai.projects` and `azure.ai.agents`

This extension is intentionally separate from `azure.ai.agents` and shares no code symbols, but it cooperates with `azure.ai.projects` for the Foundry project endpoint:

- This extension **never writes** any project-context global-config key. The persisted endpoint comes from `azd ai project set` (in `azure.ai.projects`).
- This extension reads `extensions.ai-projects.context.endpoint` first, then falls back to the legacy `extensions.ai-skills.project.context.endpoint` and `extensions.ai-agents.project.context.endpoint` keys so users who configured the endpoint via earlier extensions are not forced to re-run `set`.

`AgentCardSkill` (in `azure.ai.agents`) is unrelated to the `Skill` resource managed here and lives in a different Go module.

## Build and test

From `cli/azd/extensions/azure.ai.skills`:

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

This extension uses `internal/exterrors` so the azd host can show a useful message, attach an optional suggestion, and emit stable telemetry. See `cli/azd/extensions/azure.ai.agents/AGENTS.md` "Error handling" section for the full conventions — they apply here unchanged.

Skill-specific error codes live in `internal/exterrors/codes.go`:

- `CodeInvalidSkillName` — name fails the alphanumeric-with-hyphens regex
- `CodeInvalidSkillFile` — SKILL.md front matter unparsable, or `--file` extension unsupported
- `CodeSkillArchiveUnsafe` — `download` rejected an archive entry (zip-slip, symlink, oversized, etc.)
- `CodeSkillOutputCollision` — `download` would overwrite an existing file without `--force`

## Debug logging

Each `--debug` run writes to `azd-ai-skills-<date>.log` in the current working directory. The `skill_api` client deliberately opts out of `IncludeBody` request/response logging until a sanitizer is in place that redacts user-authored `description` and `instructions` fields. Do not enable body logging without that sanitizer.

## File handling

- `--file` is **not** a manifest. It is read at invocation time only; the CLI does not track or re-read it after the command returns.
- `create`: accepts `.md`, `.zip`, or a **directory** whose root contains `SKILL.md`. Mode is inferred from the path: directories take precedence over extension matching so callers can hand the output of `azd ai skill download` straight back. Conflicting modes (inline + `--file`) are rejected. `.md` and inline modes send `inline_content` JSON; `.zip` and directory modes upload `multipart/form-data` with a single `files[]` part. Directory mode packages the directory into an in-memory zip using `skill_api.ArchiveDirectory`, which enforces the same safety caps as `SafeExtract` on the way down (no symlinks / non-regular entries, 10,000-entry / 512 MB total cap).
- `update`: accepts `.md` only. `.zip` and directories are rejected with a structured suggestion to use `create --force` (which deletes the skill and all its versions before re-creating). Pass `--set-default-version <ver>` to repoint `default_version` at an existing immutable version without uploading new content.
- `download`: writes either an extracted directory (default) or the unmodified zip archive (`--raw`). Pass `--version <ver>` to download a non-default version. The server always returns `application/zip` (from `GET /skills/{name}/content` or `GET /skills/{name}/versions/{version}/content`).

## Versioning

Skill versions are immutable. The Skill resource itself only carries
`id`, `name`, `description`, `default_version`, `latest_version`, and
`created_at`; per-version content lives in `inline_content` (or uploaded
files) on each `SkillVersion`.

## Release preparation

Follows the same two-PR convention as `azure.ai.agents`: a version-bump PR that touches only `version.txt`, `extension.yaml`, and `CHANGELOG.md`, followed by a registry-update PR generated by `azd x publish` against the released artifacts.
