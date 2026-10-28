# Release History

## 1.0.0-beta.4 (2026-10-23)

### Features Added

- Update bundled extensions to `azure.ai.agents` `~1.0.0-beta.22`, `azure.ai.connections` `~1.0.0-beta.11`, `azure.ai.inspector` `~1.0.0-beta.9`, `azure.ai.projects` `~1.0.0-beta.15`, `azure.ai.routines` `~1.0.0-beta.11`, `azure.ai.skills` `~1.0.0-beta.9`, and `azure.ai.toolboxes` `~1.0.0-beta.12`.

### Other Changes

- [[#10571]](https://github.com/Azure/azure-dev/pull/10571) Update the minimum supported azd version to `1.37.0` to match the bundled extensions.

## 1.0.0-beta.3 (2026-09-17)

### Features Added

- [[#10071]](https://github.com/Azure/azure-dev/pull/10071) Update the Microsoft Foundry meta-package to bundle the latest Foundry extension releases and require azd `>=1.32.0`.

## 1.0.0-beta.2 (2026-07-23)

### Features Added

- [[#9277]](https://github.com/Azure/azure-dev/pull/9277) Bump bundled `azure.ai.agents` to `~1.0.0-beta.7` and `azure.ai.projects` to `~1.0.0-beta.3` to complete the `microsoft.foundry` provisioning-provider handoff from agents to projects, and raise `requiredAzdVersion` to `>=1.27.1`.

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8890]](https://github.com/Azure/azure-dev/pull/8890) Bump `requiredAzdVersion` to `>=1.27.0`.

## 0.1.0-preview - Initial Version

- A single extension to install other Foundry extensions. Encapsulates:
  - azure.ai.agents
  - azure.ai.connections
  - azure.ai.inspector
  - azure.ai.projects
  - azure.ai.routines
  - azure.ai.skills
  - azure.ai.toolboxes
