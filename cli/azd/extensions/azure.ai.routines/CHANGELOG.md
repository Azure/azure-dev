# Release History

## 0.1.0-preview (2026-05-28)

- Initial preview release of the `azure.ai.routines` extension for managing
  Microsoft Foundry Routines from the CLI.
- Adds the `azd ai routine` command group with `create`, `update`, `show`,
  `list`, `delete`, `enable`, `disable`, `dispatch`, and `run list` commands,
  plus YAML/JSON manifest-based authoring with timer, recurring, GitHub issue,
  and custom triggers and `agent-response` / `agent-invoke` actions.
- Uses the shared Foundry project-endpoint resolution cascade with
  `azure.ai.projects` and includes improved guidance when the extension is run
  outside an initialized azd project.