# Release History

## Unreleased

### Features Added

- Register the `azure.ai.project` service target. `azd ai agent init` can now write the Foundry project (and its model deployments) as its own `azure.ai.project` service entry wired to the agent via `uses:`, and this extension registers the host so `azd up`/`azd deploy` succeed for that entry. The project and its deployments continue to be provisioned by Bicep during `azd provision`, so the deploy-time hook is intentionally a no-op.

## 0.1.0-preview (2026-05-28)

Initial preview release of the Foundry Projects extension.

### Features Added

- Added `azd ai project set <endpoint>` to persist a default Foundry project endpoint to the azd global config (`~/.azd/config.json`). Other AI extensions resolve this endpoint when no azd environment variable or explicit flag is available.
- Added `azd ai project show` to display the currently resolved Foundry project endpoint and the source that provided it, for easy debugging.
- Added `azd ai project unset` to clear the persisted Foundry project endpoint from global config (idempotent — safe to run when no endpoint is set).
- Endpoint resolution uses a 5-level cascade: explicit `--project-endpoint` flag → active azd environment's `FOUNDRY_PROJECT_ENDPOINT` → global config (`extensions.ai-projects.context.endpoint`) → host `FOUNDRY_PROJECT_ENDPOINT` environment variable → actionable structured error.
- One-time auto-migration from the legacy `extensions.ai-agents.project.context` key (written by the removed `azd ai agent project set` command) into the new `extensions.ai-projects.context` key.
- All commands support `--output table` (default) and `--output json` for machine-readable output.