# Release History

## 0.0.1-preview - Initial Version

- Shares the Foundry project-endpoint resolution cascade with `azure.ai.projects`,
  reading `extensions.ai-projects.context.endpoint` (written by
  `azd ai project set`) first and falling back to the legacy
  `extensions.ai-agents.project.context.endpoint` key.