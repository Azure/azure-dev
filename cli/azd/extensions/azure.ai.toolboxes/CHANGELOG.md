# Release History

## 1.0.0-beta.4 (Unreleased)

### Bugs Fixed

- [[#9079]](https://github.com/Azure/azure-dev/pull/9079) Resolve toolbox `${VAR}` references from the service-level `env` object forwarded by azd core. Existing services without `env` keep falling back to the active azd environment.

## 1.0.0-beta.3 (2026-07-16)

### Features Added

- [[#9024]](https://github.com/Azure/azure-dev/pull/9024) `azd deploy` for `host: azure.ai.toolbox` services now supports an `endpoint:` field in `azure.yaml` to reference an existing toolbox by its MCP endpoint URL, skipping version creation. This enables reusing a toolbox managed outside the current project.

### Bugs Fixed

- [[#9013]](https://github.com/Azure/azure-dev/pull/9013) Fix `azd ai toolbox skill add/remove` and `connection add/remove` branching from the toolbox's default version instead of its latest, causing sequential mutations to silently drop earlier changes. Add a `--from-version` flag to override the branch source.

### Other Changes

- [[#9103]](https://github.com/Azure/azure-dev/pull/9103) Pin internal azd module dependency to released version.

## 1.0.0-beta.2 (2026-07-09)

### Other Changes

- [[#8866]](https://github.com/Azure/azure-dev/pull/8866) Remove `Foundry-Features: Toolboxes=V1Preview` opt-in header now that Foundry toolbox APIs are GA.

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8818]](https://github.com/Azure/azure-dev/pull/8818) The `azure.ai.toolboxes` extension now registers an `azure.ai.toolbox` service-target host. `azd deploy`/`azd up` upsert each `host: azure.ai.toolbox` service in `azure.yaml` as a new toolbox version, resolving named `connection` references to their project connection IDs, expanding `${VAR}` references, and publishing the toolbox MCP endpoint to the azd environment.
- [[#8890]](https://github.com/Azure/azure-dev/pull/8890) Bump `requiredAzdVersion` to `>=1.27.0`.

## 0.1.1-preview (2026-06-19)

### Features

- [[#8672]](https://github.com/Azure/azure-dev/issues/8672) `azd ai toolbox create` now writes the new toolbox's versioned MCP endpoint to the active azd environment under the `TOOLBOX_<NORMALIZED_NAME>_MCP_ENDPOINT` variable (the same key agents consume), and `azd ai toolbox delete` clears it when the whole toolbox is removed.

### Bugs Fixed

- [[#8688]](https://github.com/Azure/azure-dev/issues/8688) Resolve the project endpoint that `azd ai agent init` stores. `azd ai toolbox` commands now fall back to `AZURE_AI_PROJECT_ENDPOINT` (after `FOUNDRY_PROJECT_ENDPOINT`) in both the active azd environment and the host environment, so the hosted-agent + toolbox workflow no longer fails with "no Foundry project endpoint resolved" after init.

## 0.1.0-preview (2026-05-28)

Initial release of the `azure.ai.toolboxes` extension for managing Microsoft Foundry Toolboxes from the terminal.

### Features

- `azd ai toolbox create` — Create a new toolbox and its initial version from a JSON or YAML file.
- `azd ai toolbox show` — Show a toolbox version, including its computed MCP endpoint.
- `azd ai toolbox list` — List toolboxes on the Foundry project.
- `azd ai toolbox delete` — Delete a toolbox or a specific version.
- `azd ai toolbox publish` — Promote a version to be the default for a toolbox.
- `azd ai toolbox versions list` — List all published versions for a toolbox.
- `azd ai toolbox connection add/remove/list` — Attach or detach connection-backed tools to a toolbox version. Supported tool types: MCP (RemoteTool), Azure AI Search (CognitiveSearch), RemoteA2A, and GroundingWithCustomSearch.
- `azd ai toolbox skill add/remove/list` — Attach or detach skill references to a toolbox version.