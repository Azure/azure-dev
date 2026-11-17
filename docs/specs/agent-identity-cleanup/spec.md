# Agent identity cleanup on `azd down`

<!-- cspell:ignore Entra azdext envkey predown postdown exterrors -->

- **Issue:** Relates to [#10964](https://github.com/Azure/azure-dev/issues/10964)
- **Components:** `cli/azd/extensions/azure.ai.agents`, `cli/azd`
- **Status:** Draft

## Context and approach

Deploying an agent to Microsoft Foundry provisions a per-agent identity in Microsoft Entra (`instance_identity`, named `{account}-{project}-{agentName}-AgentIdentity`), and Digital Worker agents also attach a blueprint (`blueprint`). Today, `azd down` removes prompt agents in `predown`, destroys Azure Resource Manager (ARM) resources in the resource group, and removes owned Azure Bots and local session files in `postdown`. Hosted and voice agents are never deleted through Foundry before the Foundry project is destroyed. Because Microsoft Entra directory objects live at the tenant level outside the Azure resource group, destroying the Foundry project leaves Entra agent identities behind and consumes tenant identity quota across repeated `azd up` and `azd down` runs.

This design applies three rules:

1. **Supported Foundry APIs only:** Delete agents and their backing Entra identities only through supported Foundry APIs. Never call Microsoft Graph directly or require extra Graph permissions.
2. **Verified creation ownership:** Delete an agent on `azd down` only when the active `azd` environment has durable proof that `azd` created that exact agent in that exact Foundry project.
3. **Non-blocking teardown:** If Foundry cleanup fails, finish infrastructure teardown, print an actionable warning with agent and identity IDs, and keep retry details when recovery remains possible.

| Scenario | Ownership state in `.env` | `azd down` behavior |
| --- | --- | --- |
| Agent created by this `azd` environment | `AGENT_<KEY>_OWNED="true"` + matching endpoint | Delete agent via Foundry before infra destroy; clear `.env` keys |
| Pre-existing or shared agent updated by `azd` | `AGENT_<KEY>_OWNED="false"` | Keep agent and Entra identity; print brief skip note |
| Legacy deployment before ownership tracking | `AGENT_<KEY>_OWNED` unset, agent/identity keys set | Keep agent and Entra identity; print warning with identity IDs |
| Foundry delete fails during teardown | `AGENT_<KEY>_OWNED="true"` | Continue infra destroy; print warning with identity IDs; keep state if retryable |

---

## End-to-end experience

### 1. First deploy creates an agent and records ownership

When `azd up` or `azd deploy` creates a brand-new agent in Foundry, the extension records verified ownership alongside the deployed target and Entra identity identifiers in `.azure/<env>/.env`:

```ini
AGENT_MY_AGENT_NAME="my-agent"
AGENT_MY_AGENT_PROJECT_ENDPOINT="https://my-account.services.ai.azure.com/api/projects/my-project"
AGENT_MY_AGENT_OWNED="true"
AGENT_MY_AGENT_INSTANCE_IDENTITY_CLIENT_ID="00000000-0000-0000-0000-000000000001"
AGENT_MY_AGENT_INSTANCE_IDENTITY_PRINCIPAL_ID="00000000-0000-0000-0000-000000000002"
```

No extra prompt appears during deploy.

### 2. Redeploy updates a new version and keeps ownership

Running `azd deploy` again publishes a new version of `my-agent`. Because the environment already owns `my-agent` at the matching `AGENT_MY_AGENT_PROJECT_ENDPOINT`, `AGENT_MY_AGENT_OWNED="true"` is preserved and `AGENT_MY_AGENT_INSTANCE_IDENTITY_*` updates to the latest version output.

If a first deploy created the agent in Foundry and failed later while writing optional outputs, ownership was saved right after creation, so a retry deploy still recognizes the agent as owned.

### 3. Pointing at an existing agent preserves it

When a project deploys against an agent name that already existed in the Foundry project before this environment deployed it, `azd` publishes the new version and records `AGENT_MY_AGENT_OWNED="false"`:

```text
Agent "shared-agent" already exists; publishing a new version.
```

During `azd down`, `azd` skips Foundry agent deletion for `shared-agent` so shared or pre-existing agents and their Entra identities are not removed:

```text
Skipping agent "shared-agent": not created by this azd environment.
```

### 4. Standard teardown (`azd down`) removes owned agents and identities

After the user confirms `azd down` (or passes `--force` or `--purge`) and before ARM deletes the Foundry project, `azd` deletes each verified owned agent through Foundry, then destroys infrastructure, owned Activity Azure Bots, and local session state:

```text
Deleting all resources and deployed code on Azure (azd down)
Local application code is not deleted when running 'azd down'.

? Total resources to delete: 6. Are you sure you want to continue? Yes
Deleted agent "my-agent" from Foundry (Entra identity client ID: 00000000-0000-0000-0000-000000000001).
Deleting your resources can take some time.
...
Cleaned up saved agent context for agent "my-agent"
```

Once deletion succeeds, `azd` clears the `AGENT_MY_AGENT_*` deployment, identity, and ownership keys in the selected environment.

### 5. Legacy environments without ownership proof warn and skip

Environments deployed before ownership tracking shipped may have `AGENT_<KEY>_NAME` and `AGENT_<KEY>_INSTANCE_IDENTITY_CLIENT_ID` set without `AGENT_<KEY>_OWNED`. `azd down` leaves these unverified identities untouched and prints an actionable warning:

```text
WARNING: Skipping cleanup for agent "my-agent" (Entra identity client ID: 00000000-0000-0000-0000-000000000001) because creation ownership could not be verified for environment "dev". If this identity is no longer needed, delete the agent before tearing down the Foundry project or remove the identity in Microsoft Entra.
```

### 6. Cancelled, preview-only, or unrelated layer teardown makes no changes

Agents and Entra identities remain untouched when:

- The user answers `No` at the `azd down` confirmation prompt.
- A non-interactive provider run only previews deletion without destroying resources (`SkippedDeletion`).
- The user runs `azd down <layer>` for an infrastructure layer that does not destroy the Foundry project.

### 7. Partial failure during teardown

If Foundry returns an error (such as transient `5xx` or missing data-plane RBAC) while deleting a verified owned agent, `azd down` logs a visible warning, keeps scoped agent and identity metadata for follow-up or retry, and finishes tearing down Azure resources:

```text
WARNING: Failed to delete owned agent "my-agent" from Foundry before infrastructure teardown: HTTP 403 Forbidden. Check Microsoft Entra for orphaned agent identity client ID "00000000-0000-0000-0000-000000000001" (principal ID "00000000-0000-0000-0000-000000000002").
```

---

## Technical design

### Environment keys and ownership invariant

Add an explicit ownership key in `cli/azd/extensions/azure.ai.agents/internal/pkg/envkey/envkey.go`, matching the existing `AgentBotOwned` pattern:

- `AgentOwned(serviceName)` -> `AGENT_<KEY>_OWNED` (`"true"` or `"false"`)

An environment owns a deployed agent if and only if all three conditions hold:

1. `AGENT_<KEY>_OWNED` equals `"true"`.
2. `AGENT_<KEY>_NAME` is non-empty (teardown always uses this runtime-persisted name rather than static `svc.Name`).
3. `AGENT_<KEY>_PROJECT_ENDPOINT` matches the resolved Foundry project endpoint for the selected environment (case-insensitive, trailing slash trimmed).

If the user changes the agent name in `agent.yaml` or switches `FOUNDRY_PROJECT_ENDPOINT` to a different Foundry project, deploy evaluates ownership fresh for the new `(projectEndpoint, agentName)` pair instead of carrying `"true"` onto a different remote agent.

### Authoritative creation capture during deploy

A pre-deploy `GET /agents/{name}` check followed by a generic version upsert cannot prove creation on its own because another caller can create the agent between `GET` and `POST`. Each deploy path must establish ownership from an authoritative create call on `POST /agents`:

- **Container hosted agents (`deployHostedAgent` -> `createAgent` in `internal/project/service_target_agent.go`):** Today, `createAgent` runs `reconcileCreateRequestWithDeployedDigitalWorkerType` (`GetAgent`) and then calls `AgentClient.CreateAgentVersion` (`POST /agents/{name}/versions`) for both new and existing agents. Update `createAgent` so when `GetAgent` returns `404 Not Found`, it calls `AgentClient.CreateAgent` (`POST /agents` in `internal/pkg/agents/agent_api/operations.go`). If `CreateAgent` succeeds, `azd` created the agent. If `CreateAgent` returns `409 Conflict`, treat it as a concurrent external create (`owned = false`) and fall back to `CreateAgentVersion`.
- **Code hosted agents (`deployHostedCodeAgent` in `internal/project/service_target_agent.go`):** `CreateAgentFromZip` (`POST /agents`) establishes `owned = true`; on `409 Conflict` or prior `200 OK` from `GetAgent`, `UpdateAgentFromZip` runs and preserves prior verified ownership only when `(projectEndpoint, agentName)` already has `AGENT_<KEY>_OWNED="true"`, otherwise sets `"false"`.
- **Voice agents (`deployVoiceAgent` -> `deployVoiceAgentRemote` in `internal/project/service_target_agent.go`):** `CreateVoiceAgent` (`POST /agents`) establishes `owned = true`; `UpdateVoiceAgent` preserves existing matching ownership or records `"false"`.
- **Prompt agents (`createOrUpdatePromptAgent` in `internal/project/service_target_prompt.go`):** `CreateAgentWithHeaders` (`POST /agents`) already handles `409 Conflict` by falling back to `UpdateAgentWithHeaders`. Persist `AGENT_<KEY>_OWNED` using the same rule in `registerPromptAgentEnvVars`.

Persist `(AGENT_<KEY>_NAME, AGENT_<KEY>_PROJECT_ENDPOINT, AGENT_<KEY>_OWNED)` immediately after the `POST /agents` call succeeds, before optional post-create steps in `registerAgentEnvironmentVariables` or `postdeployHandler`. That prevents partial deploy failures from stranding an `azd`-created agent as unowned on retry.

### Teardown lifecycle timing and confirmation gating

Data-plane deletion (`DELETE {projectEndpoint}/agents/{agentName}`) has two ordering constraints during `azd down`:

1. It **must run before** `provisionManager.Destroy` deletes the Foundry project ARM resource. By `postdownHandler` (`internal/cmd/listen.go`), the Foundry endpoint no longer exists.
2. It **must run after** deletion is confirmed and scoped. Today, `HooksMiddleware` invokes project `predown` before `downAction.Run` (`cli/azd/cmd/down.go`) prompts for user confirmation, before checking `destroyResult.SkippedDeletion`, and regardless of which `<layer>` argument was passed. `azdext.ProjectEventArgs` (`cli/azd/pkg/azdext/event_manager.go`) also exposes only `Project *ProjectConfig`.

To prevent deleting live agents when a user declines `azd down`, runs a preview-only destroy, or tears down an unrelated layer:

- Introduce a confirmed pre-destroy extension lifecycle hook (or enrich destroy lifecycle dispatch in `cli/azd/cmd/down.go` and `cli/azd/pkg/azdext`) that fires only after confirmation succeeds for a destroy target (or `--force` / `--purge` is active) and passes the target scope / flags (`Force`, `Purge`, target layer or resources).
- Follow the repository's two-PR extension SDK rule: land any `cli/azd` contract addition first, then consume the published SDK version in `cli/azd/extensions/azure.ai.agents` without local `go.mod` `replace` directives.
- Move prompt-agent deletion (`deletePromptAgentOnDown`) onto the same confirmed pre-destroy path so prompt, hosted, and voice agents share identical confirmation safety.

### Foundry deletion, reconciliation, and idempotency

For each `host: azure.ai.agent` service in deterministic service-name order during confirmed teardown:

1. Read selected-environment values via `AZD_ENVIRONMENT` before falling back to the default environment.
2. Evaluate ownership markers:
   - No `AGENT_<KEY>_NAME` and no `AGENT_<KEY>_INSTANCE_IDENTITY_*`: nothing deployed for this environment; skip silently.
   - `AGENT_<KEY>_OWNED="false"`: skip and print a brief informational note.
   - `AGENT_<KEY>_OWNED` unset while `AGENT_<KEY>_NAME` or `AGENT_<KEY>_INSTANCE_IDENTITY_CLIENT_ID` is present: legacy unverified deployment; emit warning with identity IDs and skip deletion.
   - `AGENT_<KEY>_OWNED="true"` with matching `AGENT_<KEY>_PROJECT_ENDPOINT`: proceed with deletion.
3. Call `AgentClient.DeleteAgent(ctx, deployedName, apiVersion, true)` (`DELETE {projectEndpoint}/agents/{agentName}?api-version=...&force=true`) to terminate active sessions and delete the agent and its Foundry-managed Entra identity.
   - Treat `200 OK` and `404 Not Found` as completed cleanup (idempotent across retries).
   - On any other error, emit a structured warning containing `deployedName`, `AGENT_<KEY>_INSTANCE_IDENTITY_CLIENT_ID`, and `AGENT_<KEY>_INSTANCE_IDENTITY_PRINCIPAL_ID` (plus `AGENT_<KEY>_BLUEPRINT_CLIENT_ID` when set), keep the environment markers when the Foundry project remains reachable, and continue teardown.
4. On confirmed deletion, clear `AGENT_<KEY>_*` deployment, endpoint, identity, blueprint, and ownership keys, and clean local session state (`cleanupAgentState` / `cleanupPromptAgentState`). Keep `teardownActivityBots` (`internal/cmd/listen_activity.go`) in `postdownHandler` for owned Azure Bot ARM resources.

Align `DeleteAction.cleanupEnvVars` (`internal/cmd/delete.go`) so `azd ai agent delete` (whole-agent delete, without `--version`) also clears `AGENT_<KEY>_INSTANCE_IDENTITY_CLIENT_ID`, `AGENT_<KEY>_INSTANCE_IDENTITY_PRINCIPAL_ID`, `AGENT_<KEY>_BLUEPRINT_CLIENT_ID`, and `AGENT_<KEY>_OWNED`.

### Telemetry and diagnostics

Record teardown outcome classifications (deleted, skipped unowned, skipped unverified legacy, failed) on the existing `down` extension telemetry path using approved non-identifying enum attributes only. Never emit agent names, endpoint URLs, client IDs, or principal IDs in telemetry attributes; surface identifiers only in local terminal warnings. Also check whether telemetry documentation and privacy review files need updates if any new attribute is added.

---

## Service and cross-component dependencies

1. **Foundry agent-delete Entra contract:** Public Foundry REST documentation for `DELETE /agents/{agent_name}` documents deleting the agent, its versions, and active sessions, but does not state whether it removes the Entra `instance_identity` (and Digital Worker `blueprint`), whether cleanup is synchronous or asynchronous, or what RBAC data-plane actions are required. The Foundry service team must confirm that `DELETE /agents/{agent_name}?force=true` is the supported API that cleans up the Entra agent identity before implementation claims `#10964` is fixed.
2. **Core `azd down` post-confirmation hook:** `cli/azd` currently runs `predown` before destroy confirmation and without layer or preview context (`cli/azd/cmd/down.go`, `cli/azd/pkg/azdext/event_manager.go`). Adding a post-confirmation, layer-aware pre-destroy hook affects core `azd` extension lifecycle behavior and requires a core SDK change before the `azure.ai.agents` PR.
3. **Digital Worker M365 blueprint and agentic user lifecycle ([#9615](https://github.com/Azure/azure-dev/issues/9615)):** Digital Worker deployments persist `AGENT_<KEY>_BLUEPRINT_CLIENT_ID` and can publish an agentic user to Microsoft 365. Deleting a shared blueprint in Entra cascades to child agent identities and paired users, so Foundry and M365 ownership boundaries must be confirmed before touching blueprint-backed resources on `azd down`.

---

## Open questions

### Foundry and Microsoft Entra behavior

1. **Entra identity removal guarantee:** Does Foundry `DELETE /agents/{agent_name}?force=true` deterministically delete (or soft-delete) the per-agent Entra `instance_identity` for hosted container, hosted code, prompt, and voice agents? If asynchronous, what is the expected completion window and how can callers verify completion?
2. **Digital Worker blueprints and M365 publications:** For agents with `activity.useCase: digital_worker`, is the Foundry `blueprint` dedicated 1:1 to that single agent or shared across multiple agents in the project, and does `DELETE /agents/{agent_name}` clean up both the child `instance_identity` and the `blueprint` (or leave M365-published state behind)?
3. **Soft-delete quota vs `azd down --purge`:** Standard Microsoft Entra deletion soft-deletes agent identities for 30 days, and soft-deleted agent identities can still count toward tenant quotas (such as the 250-identity limit). When a user runs `azd down --purge`, does Foundry support a permanent-purge parameter for the Entra identity, or should `azd` document soft-delete retention and manual Entra purge steps?

### Ownership and CLI experience

4. **Agent kind scope parity:** Today `predownHandler` deletes `kind: prompt` agents using only `AGENT_<KEY>_NAME` and `AGENT_<KEY>_PROJECT_ENDPOINT` (without verifying whether `azd` originally created the prompt agent) and skips `prompt-voice` agents. Should `hosted`, `prompt`, and `prompt-voice` agents all enforce the same `AGENT_<KEY>_OWNED="true"` gate?
5. **External or shared Foundry projects (BYO Foundry project):** If an `azd` environment deploys an `azd`-created agent into a shared Foundry project whose ARM resource is not managed or destroyed by this repo's Bicep or Terraform templates, should `azd down` still delete the `azd`-created agent and its Entra identity, or prompt separately before modifying resources inside an external Foundry project?
6. **Multi-environment version collisions:** If environment `dev` creates `my-agent` (`v1`, `AGENT_MY_AGENT_OWNED="true"`) and environment `staging` later deploys `v2` to the same agent name in the same Foundry project, running `azd down -e dev` would delete the whole agent including `v2`. Should `azd down` check whether the remote latest version still matches `AGENT_<KEY>_VERSION` and warn/skip when newer external versions exist unless `--force` is set?
7. **Voice telephony bindings on teardown:** Deleting a voice agent through `azd ai agent delete` currently notes that agent deletion does not guarantee telephony binding cleanup. During `azd down`, should `azure.ai.agents` delete `azd`-created telephony bindings before calling `DeleteAgent` on owned voice agents?
8. **Legacy environment opt-in cleanup:** For pre-existing environments that lack `AGENT_<KEY>_OWNED`, `azd down` warns and leaves the identity untouched. Should users clean those up via `azd ai agent delete` prior to `azd down`, or should `azd down` offer an interactive prompt in interactive mode asking whether to delete unverified agents listed in `.env`?

---

## Summary of required changes

### `cli/azd` (core CLI and extension SDK, if post-confirmation gating is added)

- Add a post-confirmation, non-preview, layer-aware pre-destroy lifecycle event (or extend `ProjectEventArgs` / destroy hooks) in `cli/azd/cmd/down.go` and `cli/azd/pkg/azdext/event_manager.go`.
- Add unit tests covering declined confirmation, `SkippedDeletion` preview runs, `--force`, `--purge`, and single-layer `azd down <layer>`.

### `cli/azd/extensions/azure.ai.agents`

- Add `envkey.AgentOwned` (`AGENT_<KEY>_OWNED`) in `internal/pkg/envkey/envkey.go`.
- Update `createAgent` in `internal/project/service_target_agent.go` to call `AgentClient.CreateAgent` (`POST /agents`) on initial `404 Not Found` and fall back to `CreateAgentVersion` on `409 Conflict`.
- Persist scoped ownership markers immediately after authoritative agent creation across `deployHostedAgent`, `deployHostedCodeAgent`, `deployVoiceAgentRemote` (`internal/project/service_target_agent.go`), and `createOrUpdatePromptAgent` / `registerPromptAgentEnvVars` (`internal/project/service_target_prompt.go`).
- Implement verified agent teardown in `internal/cmd/listen.go` for owned agents via `AgentClient.DeleteAgent` (`force=true`), treating `404 Not Found` as already deleted, warning on legacy unverified or failed deletions without blocking teardown, and clearing `AGENT_<KEY>_*` markers on confirmed success.
- Update `DeleteAction.cleanupEnvVars` in `internal/cmd/delete.go` to clear `AGENT_<KEY>_INSTANCE_IDENTITY_CLIENT_ID`, `AGENT_<KEY>_INSTANCE_IDENTITY_PRINCIPAL_ID`, `AGENT_<KEY>_BLUEPRINT_CLIENT_ID`, and `AGENT_<KEY>_OWNED` on whole-agent delete.

### Tests and documentation

- Add unit tests in `internal/project/` and `internal/cmd/` covering fresh create ownership, redeploy preservation, `409 Conflict` concurrent creation, adopted pre-existing agents, legacy environments without `AGENT_<KEY>_OWNED`, cancelled/preview/layer-scoped `azd down`, `404` idempotency, and `4xx`/`5xx` warning retention.
- Update extension documentation and environment variable references for `AGENT_<KEY>_OWNED` and `azd down` Entra identity cleanup behavior.
- Validate end-to-end against a disposable Foundry test project after confirming the Foundry service deletion contract, observing Microsoft Entra directly to verify identity removal.
