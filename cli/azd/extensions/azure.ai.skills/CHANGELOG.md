# Release History

## 0.0.1-preview (Unreleased)

- Initial preview release of the `azure.ai.skills` extension.
- Adds the `azd ai skill` command group with full CRUD over Foundry Skills:
  - `azd ai skill create <name>` — inline (`--description` + `--instructions`),
    SKILL.md file (`--file ./SKILL.md`), or gzip package (`--file ./skill.tar.gz`).
  - `azd ai skill update <name>` — inline or `--file *.md`.
  - `azd ai skill show <name>` — metadata only.
  - `azd ai skill list` — paginated, supports `--top` and `--orderby`.
  - `azd ai skill download <name>` — extracts to `./.agents/skills/<name>/` by
    default, `--raw` keeps the gzip archive.
  - `azd ai skill delete <name>` — confirmation by default, `--force` to skip.
- Shares the Foundry project-endpoint resolution cascade with `azure.ai.agents`,
  reading `extensions.ai-skills.project.context.endpoint` first and falling back to
  `extensions.ai-agents.project.context.endpoint`.
