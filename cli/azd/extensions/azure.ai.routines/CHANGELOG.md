# Release History

## 0.1.0-preview (2026-05-28)

- [[#8241]](https://github.com/Azure/azure-dev/pull/8241) Initial preview
  release of the `azure.ai.routines` extension.
- [[#8241]](https://github.com/Azure/azure-dev/pull/8241) Add the
  `azd ai routine` command group with `create`, `update`, `show`, `list`,
  `delete`, `enable`, `disable`, `dispatch`, and `run list` commands for
  managing Foundry routines from the CLI.
- [[#8241]](https://github.com/Azure/azure-dev/pull/8241) Add YAML/JSON
  manifest support plus timer, recurring, GitHub issue, and custom triggers
  with `agent-response` and `agent-invoke` actions.
- [[#8433]](https://github.com/Azure/azure-dev/pull/8433) Share the Foundry
  project-endpoint resolution cascade with `azure.ai.projects`, reading
  `extensions.ai-projects.context.endpoint` (written by `azd ai project set`)
  first and falling back to the legacy
  `extensions.ai-agents.project.context.endpoint` key.
- [[#8374]](https://github.com/Azure/azure-dev/pull/8374) Improve the
  missing-project guidance to point users to `azd ai agent init` when the
  extension is run outside an initialized azd project.