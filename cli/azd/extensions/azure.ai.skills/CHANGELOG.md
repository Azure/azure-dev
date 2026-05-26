# Release History

## 0.0.1-preview (Unreleased)

# Release History

## 0.0.1-preview (Unreleased)

- Initial preview release of the `azure.ai.skills` extension.
- Adds the `azd ai skill` command group on top of the versioned Foundry
  Skills API (`Foundry-Features: Skills=V1Preview`):
  - `azd ai skill create <name>` — creates a skill and uploads its first
    default version via `POST /skills/{name}/versions`. Modes: inline
    (`--description` + `--instructions`), SKILL.md file (`--file ./SKILL.md`),
    or ZIP package via `multipart/form-data` (`--file ./skill.zip`).
  - `azd ai skill update <name>` — uploads a new default version using the
    same inline / SKILL.md modes; ZIP is rejected with a pointer to
    `create --force`. Pass `--set-default-version <ver>` to repoint
    `default_version` at an existing version without uploading new content
    (`POST /skills/{name}`).
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
- Shares the Foundry project-endpoint resolution cascade with `azure.ai.agents`,
  reading `extensions.ai-skills.project.context.endpoint` first and falling
  back to `extensions.ai-agents.project.context.endpoint`.
