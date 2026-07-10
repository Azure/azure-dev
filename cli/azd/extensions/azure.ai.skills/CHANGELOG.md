# Release History

## 1.0.0-beta.2 (2026-07-09)

### Other Changes

- [[#9027]](https://github.com/Azure/azure-dev/pull/9027) Bump `golang.org/x/crypto` to v0.53.0 (and transitively `golang.org/x/net` to v0.55.0) to address security advisories.

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8818]](https://github.com/Azure/azure-dev/pull/8818) The `azure.ai.skills` extension now registers an `azure.ai.skill` service-target host so `azd deploy`/`azd up` can upsert Foundry skills declared as `host: azure.ai.skill` services in `azure.yaml`.
- [[#8890]](https://github.com/Azure/azure-dev/pull/8890) Bump `requiredAzdVersion` to `>=1.27.0`.
- [[#8651]](https://github.com/Azure/azure-dev/pull/8651) Update Go to 1.26.4 and bump golang.org/x/crypto and golang.org/x/net. Thanks @hemarina for the contribution!

### Bugs Fixed

- [[#8791]](https://github.com/Azure/azure-dev/pull/8791) Fix silent data loss on SKILL.md round-trips: `ParseSkillMd` now extracts `license`, `compatibility`, and `allowed_tools` from front matter. Also removes dead code paths, fixes test-unsafe mutable globals, and adds missing validation.

## 0.1.1-preview (2026-06-05)

- [[#8509]](https://github.com/Azure/azure-dev/pull/8509) Accept `.zip` and
  directory inputs for `azd ai skill update --file`, matching `create` mode
  support and uploading new immutable skill versions from those inputs.

## 0.1.0-preview (2026-05-28)

- Initial preview release of the `azure.ai.skills` extension.
- Adds the `azd ai skill` command group on top of the versioned Foundry
  Skills API (`Foundry-Features: Skills=V1Preview`):
  - `azd ai skill create <name>` — creates a skill and uploads its first
    default version via `POST /skills/{name}/versions`. Modes: inline
    (`--description` + `--instructions`), SKILL.md file (`--file ./SKILL.md`),
    ZIP package via `multipart/form-data` (`--file ./skill.zip`), or a
    directory whose root contains `SKILL.md` (`--file ./skill-src/`) — the
    CLI packages the directory in memory and uploads it as multipart, so the
    output of `azd ai skill download` round-trips back through `create`
    without a manual zip step.
  - `azd ai skill update <name>` — uploads a new default version using the
    same inline / SKILL.md modes; ZIP and directory `--file` inputs are
    rejected with a pointer to `create --force`. Pass `--set-default-version
    <ver>` to repoint `default_version` at an existing version without
    uploading new content (`POST /skills/{name}`).
  - `azd ai skill show <name>` — returns `Skill { id, name, description,
    default_version, latest_version, created_at }`.
  - `azd ai skill list` — paginated, supports `--top` and `--orderby`.
  - `azd ai skill download <name>` — downloads the zip content from
    `GET /skills/{name}/content`, or `GET /skills/{name}/versions/{version}/content`
    when `--version` is passed. Extracts into `./.agents/skills/<name>/` by
    default; `--raw` writes the unmodified zip archive.
  - `azd ai skill delete <name>` — confirmation by default, `--force` to skip.
- Skill names follow the agentskills.io spec:
  `^[a-z0-9]([a-z0-9\-]*[a-z0-9])?$`, max 64 chars (lowercase only).
- Shares the Foundry project-endpoint resolution cascade with `azure.ai.projects`,
  reading `extensions.ai-projects.context.endpoint` (written by
  `azd ai project set`) first and falling back to the legacy
  `extensions.ai-skills.project.context.endpoint` and
  `extensions.ai-agents.project.context.endpoint` keys.
