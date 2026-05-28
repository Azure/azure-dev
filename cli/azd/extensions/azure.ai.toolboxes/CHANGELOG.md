# Release History

## 0.0.1-preview (2026-05-28)

Initial release of the `azure.ai.toolboxes` extension for managing Microsoft Foundry Toolboxes from the terminal.

### Features

- `azd ai toolbox create` — Create a new toolbox and its initial version from a YAML file.
- `azd ai toolbox show` — Show a toolbox version, including its computed MCP endpoint.
- `azd ai toolbox list` — List toolboxes on the Foundry project.
- `azd ai toolbox delete` — Delete a toolbox or a specific version.
- `azd ai toolbox publish` — Promote a version to be the default for a toolbox.
- `azd ai toolbox version list` — List all published versions for a toolbox.
- `azd ai toolbox connection add/remove/list` — Attach or detach connection-backed tools to a toolbox version. Supported tool types: MCP (RemoteTool), Azure AI Search (CognitiveSearch), RemoteA2A, and GroundingWithCustomSearch.
- `azd ai toolbox skill add/remove/list` — Attach or detach skill references to a toolbox version.