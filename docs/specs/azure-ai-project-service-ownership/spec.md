<!-- cspell:ignore azd azdext brownfield contoso greenfield grpcserver -->
<!-- cspell:ignore idempotency idempotently noopt noop -->

# Foundry project service ownership and init handoff

## Purpose

This spec completes the second stage of
[issue #9085](https://github.com/Azure/azure-dev/issues/9085). The first stage
moved the `microsoft.foundry` provisioning provider to `azure.ai.projects` in
[PR #9133](https://github.com/Azure/azure-dev/pull/9133). The remaining work is
to move creation and updates of the `host: azure.ai.project` service from
`azure.ai.agents` to `azure.ai.projects`.

The design adds `azd ai project init` as the projects-owned authoring command.
`azd ai agent init` calls that command internally, so a new agent still takes
one user command. The projects extension becomes the only component that
interprets or mutates project service properties.

## Current state and remaining work

| Area | Current state | Remaining work |
| --- | --- | --- |
| Unified Foundry services | [PR #8675](https://github.com/Azure/azure-dev/pull/8675) split projects, agents, connections, and toolboxes into separate services | None for the service split |
| Project service target | `azure.ai.projects` registers `host: azure.ai.project` | Keep the target and its no-op deploy lifecycle |
| Provisioning | `azure.ai.projects` registers `microsoft.foundry` and handles new and existing projects | Remove the remaining provisioning and synthesis copies from agents |
| Project authoring | `azure.ai.agents` creates and updates the project service in `emitResourceServices` | Move create, merge, service naming, endpoint stamping, and model deployment authoring to projects |
| Project command | `set`, `show`, and `unset` manage global endpoint context only | Add a workspace authoring command |
| Agent initialization | Project selection, model selection, environment setup, and IaC eject live in agents | Delegate project setup while preserving one-command agent initialization |
| Adoption | Agents inspect project service properties and legacy shapes | Make agents identify dependencies by host and leave property handling to projects |
| Compatibility | The projects provider can read pre-split Foundry hosts | Keep this fallback until a separately announced removal |
| Brownfield IaC eject | Existing-project eject is tracked by [issue #9127](https://github.com/Azure/azure-dev/issues/9127) and draft [PR #9348](https://github.com/Azure/azure-dev/pull/9348) | Coordinate ownership and command naming before either change merges |
| Shared-resource teardown | Existing-project teardown remains tracked by [issue #6215](https://github.com/Azure/azure-dev/issues/6215) | Do not expand this migration into a general shared-resource solution |

This milestone does not move connection, toolbox, skill, or routine authoring
into projects. Those resources remain with their owning extensions and their
respective ownership issues.

## Part 1: End-to-end experience

### Command surface

`azd ai project` has two different kinds of state. The command names make the
scope explicit.

| Command | Scope | Behavior |
| --- | --- | --- |
| `azd ai project set <endpoint>` | User-global context | Stores a default endpoint for commands that need a project at runtime |
| `azd ai project show` | Resolved context | Shows the endpoint and the source that supplied it |
| `azd ai project unset` | User-global context | Clears the stored default |
| `azd ai project init` | Current azd workspace and environment | Creates or idempotently updates the `azure.ai.project` service |

`set` does not start editing `azure.yaml`. Changing that behavior would make an
existing global command mutate the current repository unexpectedly. `init` is
chosen instead of `add` because the operation supports first-time creation,
reconciliation, and repeated execution.

The public `init` inputs are:

| Input | Meaning |
| --- | --- |
| `--project-id`, `-p` | Select an existing project by full ARM resource ID. The command validates the resource and derives its endpoint. |
| `--project-endpoint` | Author an endpoint-only existing-project reference without ARM discovery. This cannot be combined with project-owned resources that require ARM reconciliation. |
| `--model` | Select a model and author a deployment that `azd provision` will create or update. |
| `--model-deployment`, `-d` | Select an existing deployment from the project chosen by `--project-id`. |
| `--infra[=bicep\|terraform]` | Eject project infrastructure. A value-less flag selects Bicep. |
| `--force` | Confirm an intentional switch between different existing projects in non-interactive use. It never overwrites an unrelated provisioning provider. |
| `--environment`, `--no-prompt`, `--output` | Use the standard extension root flags. |

`--project-id` and `--project-endpoint` are mutually exclusive.
`--model` and `--model-deployment` are also mutually exclusive on the new
command. The deprecated agent flags keep their current precedence during the
compatibility period, then forward only the resolved choice.

### Initialize a new Foundry project

From an empty directory or a minimal azd project, the user runs:

```bash
azd ai project init
```

The interactive flow asks:

```text
? How should the Foundry project be configured?
  Create a new Foundry project
  Use an existing Foundry project

? Select an Azure subscription:
? Select an Azure location:
? Configure a model deployment? Yes
? Select a model:
```

If no `azure.yaml` exists, the command first runs the equivalent of
`azd init --minimal` through the extension workflow service and creates an azd
environment. It then writes:

```yaml
name: contoso-ai

infra:
  provider: microsoft.foundry

services:
  ai-project:
    host: azure.ai.project
    deployments:
      - name: <deployment-name>
        model:
          format: OpenAI
          name: <model-name>
          version: <resolved-version>
        sku:
          name: <resolved-sku>
          capacity: <resolved-capacity>
```

Initialization does not create Azure resources. The completion message says
that the project configuration is ready and directs the user to
`azd provision`.

The model step is optional. Skipping it still creates the project service. This
allows a project to be composed with agents or other Foundry resources later.

### Reuse an existing Foundry project

The preferred scripted flow supplies the ARM resource ID:

```bash
azd ai project init \
  --project-id "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>" \
  --model-deployment <deployment-name>
```

The command validates access, loads the project and deployment, persists the
project context in the active azd environment, and writes:

```yaml
services:
  my-project:
    host: azure.ai.project
    endpoint: https://<account>.services.ai.azure.com/api/projects/<project>
    deployments:
      - name: <deployment-name>
        model:
          format: OpenAI
          name: <model-name>
          version: <model-version>
        sku:
          name: <sku-name>
          capacity: <capacity>
```

The active environment receives the full ID as `AZURE_AI_PROJECT_ID` and the
resolved endpoint as `FOUNDRY_PROJECT_ENDPOINT`. The provider continues to use
the ID when it must reconcile deployments, connections, or a container
registry on an existing account.

An endpoint-only flow is available when no ARM-owned resource needs
reconciliation:

```bash
azd ai project init \
  --project-endpoint "https://<account>.services.ai.azure.com/api/projects/<project>"
```

This writes `endpoint` and does not invent an ARM resource ID. If a deployment,
connection, or registry later requires reconciliation, the command tells the
user to rerun with `--project-id`.

The user-global endpoint written by `azd ai project set` is never consumed
silently by `init`. Interactive mode may display it as a suggested value, but
the user must confirm it. Non-interactive mode requires an explicit flag.

### Add project configuration to an existing azd workspace

When the workspace already has one `azure.ai.project` service, `init` updates
that service in place and keeps its service key. It preserves `network`,
`uses`, hooks, `$ref` entries, and unknown properties.

When no project service exists, the key selection order is:

1. Use the selected existing project's sanitized name when it does not collide.
2. Use `ai-project`.
3. Use the first available `ai-project-<n>` suffix.

The service key is not an Azure resource name. A later run never renames it.

When more than one `azure.ai.project` service exists, the command fails before
mutation and lists the conflicting keys. The provisioning provider supports
one Foundry project per azd project, so choosing one silently would make
`uses` ordering ambiguous.

### Initialize an agent with one command

The user experience remains:

```bash
azd ai agent init
```

The agents extension still owns source discovery, template adoption, agent
name, protocols, runtime, deployment mode, and agent service generation. When
project configuration is needed, it invokes the projects-owned init command
through `WorkflowService.Run`.

Project and model prompts appear inside the same command run. The user is not
asked to run `azd ai project init` first. The resulting graph is:

```yaml
services:
  ai-project:
    host: azure.ai.project
    deployments:
      - name: <deployment-name>
        model:
          format: OpenAI
          name: <model-name>
          version: <resolved-version>
        sku:
          name: <resolved-sku>
          capacity: <resolved-capacity>

  support-agent:
    host: azure.ai.agent
    project: src/support-agent
    uses:
      - ai-project
```

If the workspace was already initialized with `azd ai project init`, agent
initialization reuses the service and skips project selection. It invokes the
project command only when it needs to add model requirements or refresh
missing environment context.

`azd ai agent init --project-id`, `--model`, `--model-deployment`, and
`--infra` remain accepted during the migration window. Agents translates them
to the projects command and prints one deprecation notice that names the new
direct command. Existing scripts continue to work.

### Adopt a unified or pre-split manifest

For a unified manifest that already contains `host: azure.ai.project`, agents
does not decode `endpoint`, `deployments`, `network`, or project `$ref`
content. It passes project requirements to the projects command, then locates
the resulting project service by host and adds its key to the agent's `uses`.

For a pre-split manifest with project settings under `azure.ai.agent` or
`microsoft.foundry`, the existing provider fallback continues to provision it.
When the user runs either init command, projects can copy the recognized
project properties into a new `azure.ai.project` service. The legacy
properties remain during the compatibility window so older extension versions
do not stop working. A warning identifies the new authoritative service and
links to migration guidance.

Automatic migration is additive. It does not delete an agent service, a legacy
host, or unknown legacy properties.

### Non-interactive behavior

A new project without a model can be authored without Azure discovery:

```bash
azd ai project init --no-prompt
```

The command creates the project service and prints the environment values that
must be set before provisioning. It does not prompt or select a subscription
implicitly.

A model lookup for a new project requires deterministic Azure context:

```bash
azd env set AZURE_SUBSCRIPTION_ID <subscription-id>
azd env set AZURE_LOCATION <location>
azd ai project init --no-prompt --model <model-name>
```

If required values are missing, the command fails before changing the project
service. It does not write a placeholder deployment that could provision a
different model later.

For an existing project, `--project-id` supplies the subscription and project
identity. An explicit model deployment must exist and be eligible for that
project. Unknown projects, inaccessible subscriptions, unsupported models, and
insufficient quota are structured failures with corrective guidance.

### Repeated runs and conflicts

An unchanged second run produces no `azure.yaml` diff. Merge rules are:

- Preserve the existing project service key.
- Set `endpoint` only after explicit selection or confirmation.
- Merge deployments by deployment `name`.
- Leave an identical deployment unchanged.
- Require confirmation before replacing a deployment definition with the same
  name but a different model, version, SKU, or capacity.
- In non-interactive mode, reject such replacement unless `--force` is set.
- Preserve deployments that were not part of the current request.
- Preserve `network`, service-level `env`, hooks, `uses`, `$ref` items, and
  unknown properties.
- Never replace the full service object to update one field.

Switching from one existing endpoint to another requires confirmation.
Switching from an existing project to a new project removes `endpoint` only
after confirmation. `--force` permits these two transitions in scripts.

### Infrastructure eject

The direct command becomes:

```bash
azd ai project init --infra
azd ai project init --infra=terraform
```

The command uses the project service as the synthesis source. Bicep remains the
value-less default. Terraform continues to set `infra.provider: terraform`.
The agent-side spelling remains as a forwarding alias during the migration
window.

Existing refusal rules remain: do not overwrite `infra/`, do not choose among
multiple project services, and do not silently drop unsupported network
configuration. Brownfield eject behavior must be coordinated with
[issue #9127](https://github.com/Azure/azure-dev/issues/9127).

### Provision, deploy, and teardown

`azd provision` remains the resource reconciliation step. A missing `endpoint`
creates a Foundry account and project. A present `endpoint` reuses the project
and applies only supported declarations.

The project service's package, publish, and deploy operations remain no-ops.
Project resources are reconciled during provision so `uses` ordering places
them before agents and other dependent services.

`azd down` keeps an existing project referenced by `endpoint`. Resources
created under a greenfield Foundry deployment follow the current provider
destroy behavior. This spec does not redefine shared-resource teardown tracked
by [issue #6215](https://github.com/Azure/azure-dev/issues/6215).

### Partial failure and recovery

The projects command completes discovery and validates the desired merge before
writing `azure.yaml`. It writes environment context before the service so the
service never points at an existing project without its available ARM context.

Project and environment updates are not a cross-file transaction. If a write
fails after an earlier write succeeds, the command reports exactly which state
changed. A rerun reads the persisted state and completes the same desired
merge. It does not create a second service or duplicate a deployment.

When delegated project setup fails, agent initialization stops before adding
or changing the agent service. Downloaded or generated source files remain on
disk. The error tells the user that source scaffolding is safe to reuse and
that rerunning `azd ai agent init` will resume project setup.

## Part 2: Technical design

### Ownership boundary

After this migration, the projects extension owns:

- The `azure.ai.project` service key and property merge.
- New versus existing project selection.
- Foundry project discovery and ARM ID validation.
- Subscription, tenant, and project location setup.
- Model discovery, quota-aware deployment selection, and deployment
  reconciliation input.
- Existing-project endpoint stamping.
- Project-related azd environment values.
- Foundry project synthesis and IaC eject.
- Project command errors, telemetry, and migration warnings.

The agents extension retains:

- Agent source, template, and manifest handling.
- Agent-compatible model constraints, such as supported locations or excluded
  model families.
- Agent runtime, protocol, deployment mode, and image decisions.
- Agent service authoring.
- Agent-specific pending-provision and next-step messages.
- Composition of the agent with connection and toolbox services until their
  ownership issues complete.
- `uses` edges from the agent to service keys returned by owning extensions.

An agent may describe what project capabilities it requires. It may not write
the project service that supplies them.

### Projects-owned init action

`NewRootCommand` in
`cli/azd/extensions/azure.ai.projects/internal/cmd/root.go` registers
`newProjectInitCommand`. The implementation lives in new projects-owned files:

```text
cli/azd/extensions/azure.ai.projects/internal/cmd/project_init.go
cli/azd/extensions/azure.ai.projects/internal/cmd/project_init_request.go
cli/azd/extensions/azure.ai.projects/internal/cmd/project_service_writer.go
```

`projectInitAction.Run` follows this order:

1. Load or create the minimal azd project and active environment.
2. Parse public flags or a delegated init request.
3. Read the current project service and any compatible legacy service.
4. Resolve Azure context only when the request requires it.
5. Resolve new or existing project mode.
6. Resolve model deployment requirements.
7. Build and validate the complete service merge in memory.
8. Persist project environment values.
9. Apply narrow project and service mutations through `azdext.ProjectService`.
10. Eject IaC when requested.
11. Return or write a versioned result for the caller.

The command calls `Project.AddService` only when no project service exists.
Updates use `SetServiceConfigValue` and `UnsetServiceConfig` for owned paths.
This avoids the current full-object replacement risk in
`emitResourceServices`.

### Agent-to-project handoff

The extensions are separate Go modules and release as separate binaries.
Agents must not import a projects internal package or duplicate its
implementation. The handoff uses the existing azd workflow service to invoke:

```text
azd ai project init --request-file <absolute-path> --result-file <absolute-path>
```

`--request-file` and `--result-file` are hidden integration flags. They are not
advertised as a user contract. The request itself is versioned so mismatched
extension versions fail clearly instead of ignoring fields.

The request contains no credentials or connection secrets. A representative
shape is:

```json
{
  "schemaVersion": "1",
  "source": "azure.ai.agents",
  "projectResourceId": "",
  "projectEndpoint": "",
  "model": "",
  "modelDeployment": "",
  "deployments": [],
  "modelFilter": {
    "locations": [],
    "excludedModels": []
  },
  "containerRegistry": "not-required",
  "infraProvider": "",
  "force": false
}
```

The projects command writes the result atomically after all requested
mutations succeed:

```json
{
  "schemaVersion": "1",
  "serviceName": "ai-project",
  "mode": "new",
  "requiresProvision": true,
  "networkInjected": false,
  "deployments": []
}
```

The result includes only data that agents needs for its own decisions. It does
not make the project service shape part of the agents code.

Agents creates both files in a private temporary directory, invokes
`WorkflowService.Run`, reads and validates the result, and removes the
directory on success or failure. After handoff, agents also calls
`Project.Get` and verifies that `serviceName` still identifies exactly one
`host: azure.ai.project` service before writing `uses`.

### Workflow error propagation

`WorkflowService.Run` in
`cli/azd/internal/grpcserver/workflow_service.go` currently converts every
workflow failure to a plain `codes.Internal` status. That would erase the
projects extension's error category, code, suggestion, and links during
delegated agent init.

The workflow service must attach `azdext.WrapError(err)` as a gRPC status
detail when the nested command returns a structured extension error. A matching
helper in `cli/azd/pkg/azdext/extension_error.go` extracts the detail and calls
`azdext.UnwrapError`. Agents returns that error without reclassifying it.
Plain workflow errors keep the current fallback.

This framework change is backward compatible. Older clients ignore unknown
status details, and the `RunWorkflowRequest` signature does not change.

### Project and environment creation

The projects extension takes over the project-neutral parts of
`ensureProject`, `deriveEnvName`, `scaffoldProject`, and
`writeFoundryProvider` from
`cli/azd/extensions/azure.ai.agents/internal/cmd/init.go`.

For a new workspace, projects invokes `azd init --minimal` through
`WorkflowService.Run`. For an existing workspace:

- If `infra.provider` is empty, projects sets it to `microsoft.foundry`.
- If it is already `microsoft.foundry`, projects leaves it unchanged.
- If it names another provider, projects never overwrites it.
- An endpoint-only project with no resources to reconcile may still be added
  under another provider, with a warning that the project is externally
  managed.
- A new project, deployment, network, or registry request under another
  provider fails with guidance. Supporting multiple provisioning owners is a
  separate composition decision.

The current removal of starter `infra.path: ./infra` happens only when the
command itself creates the minimal project. It does not remove a user-authored
path from an existing workspace.

### Project discovery and Azure context

Move the generic project code from:

```text
cli/azd/extensions/azure.ai.agents/internal/cmd/init_foundry_project_setup.go
cli/azd/extensions/azure.ai.agents/internal/cmd/init_foundry_resources_helpers.go
```

The moved code includes `FoundryProjectInfo`, `extractProjectDetails`,
`listFoundryProjects`, `getFoundryProject`, `selectFoundryProject`,
`configureFoundryProjectEnv`, `loadAzureContext`, `ensureSubscription`, and
`ensureLocation`.

Credential creation continues to use the signed-in user's access tenant. A
subscription selected by `PromptSubscription` uses
`Subscription.UserTenantId`. A supplied subscription uses `LookupTenant`
before creating `AzureDeveloperCLICredential`.

Project mode has one source of truth:

- An explicit project ID selects existing mode.
- An explicit endpoint selects endpoint-only existing mode.
- A confirmed interactive selection selects existing mode.
- Otherwise the mode is new.

Environment values from a prior run seed prompt defaults but do not override a
new explicit flag. `--no-prompt` never changes mode from hidden global config.

### Model selection

Move generic deployment discovery and quota selection, including
`listProjectDeployments`, `resolveModelDeployment`,
`selectModelDeployment`, and the generic portions of `selectNewModel`, into
projects.

Agent-specific filtering remains in agents. The handoff request carries the
allowed locations and excluded models. Projects intersects that filter with
models and quota available in the selected location. Usage data from another
location never makes a model eligible.

Public `azd ai project init` uses a project-generic filter. The command can
skip model configuration entirely. Model defaults are a product setting in
projects, not a constant imported from agents.

When an existing deployment is selected, projects records its concrete model,
version, SKU, and capacity in `deployments`. This makes the desired state
reviewable and keeps provision idempotent.

### Service contract and schema

No new `azure.ai.project` property is required for this ownership milestone.
The current schema at
`cli/azd/extensions/azure.ai.projects/schemas/azure.ai.project.json` already
carries:

- `endpoint` for existing-project reuse.
- `deployments` for model desired state.
- `network` for account networking.
- Local `$ref` entries for deployment definitions.
- `additionalProperties: true` for forward compatibility.

The full existing-project ARM ID remains the environment value
`AZURE_AI_PROJECT_ID`. The command sets it whenever selection is based on ARM.
The provider in
`cli/azd/extensions/azure.ai.projects/internal/provisioning/foundry_provisioning_provider.go`
continues to require it only for brownfield operations that cannot be
performed from an endpoint alone.

Adding a `resourceId` property is not part of this change. It would create a
second persisted project identity and requires separate decisions about
portability, endpoint consistency, and precedence over the azd environment.

### Idempotent service merge

The writer loads the service through `Project.Get` and preserves the service's
`AdditionalProperties`. It does not round-trip the entire object through a
narrow struct because that would discard unknown fields.

Owned paths are:

```text
host
endpoint
deployments
```

`host` is set only when the service is created. `endpoint` is changed only by
an explicit project-mode transition. `deployments` is a semantic list keyed by
deployment name.

`network` belongs to projects but is not modified by this version of `init`
unless it came from a project manifest being adopted. This prevents a simple
rerun from resetting hand-authored private networking.

A deployment represented by `$ref` remains a reference. The command resolves
it for collision checks but does not rewrite the referenced file. A new
deployment is appended inline. If a requested deployment conflicts with a
referenced definition, the command reports the reference path and requires the
user to edit it or choose another deployment name.

### Legacy reconciliation

`findFoundryProjectService` in
`cli/azd/extensions/azure.ai.projects/internal/provisioning/foundry_provisioning_provider.go`
keeps its fallback to one legacy `azure.ai.agent` or `microsoft.foundry`
service when no project service exists.

The new init reader uses the same accepted-host constants. It extracts only
project-owned properties from a legacy block and creates a project service.
The provider then prefers that service, while the old block remains readable
by older versions.

Agents removes project decoding from
`cli/azd/extensions/azure.ai.agents/internal/cmd/init_adopt.go`. Adoption treats
non-agent services as opaque service nodes and preserves them. After projects
returns, agents needs only the project service key for `uses`.

### Agent code changes

Split `emitResourceServices` in
`cli/azd/extensions/azure.ai.agents/internal/cmd/resource_services.go`.
Remove project service creation, `resolveProjectServiceKey`,
`existingProjectServiceKey`, `projectNameHint`, and `stampProjectEndpoint`.
Keep agent composition for connection and toolbox services until those owners
provide equivalent init handoffs.

`setServiceUses` merges the returned project key with existing agent
dependencies. It must not replace hand-authored `uses` values or add the same
key twice.

Remove project and model selection branches from `InitAction.Run`,
`configureModelChoice`, and `addToProject` after the delegated result supplies
the selected deployment and project characteristics. Retain small flag
adapters for the compatibility period.

### Synthesis and IaC ownership

Move `init_infra.go`, the project synthesis package, and embedded Bicep and
Terraform templates from agents to projects. Until the move is complete, the
copies in both extensions must remain byte-for-byte identical as required by
their local `AGENTS.md` files.

After the projects command is released and agents depends on that version:

- Delete the agents synthesis copy.
- Delete project-specific IaC error codes and guidance from agents.
- Change all direct guidance to `azd ai project init --infra`.
- Keep the agent flag as a forwarding alias only.

The provisioning provider remains the source of truth for actual resource
creation. Eject and provision must continue to use the same synthesis model so
the generated parameters do not diverge.

### Lifecycle and ordering

The service graph remains:

```text
azure.ai.project
  -> azure.ai.connection
  -> azure.ai.toolbox
  -> azure.ai.agent
```

Connections and toolboxes depend on the project. The agent depends on every
resource it uses. `preprovision` in projects resolves project service
configuration into provisioning input. Provision runs before service deploy.
The project service deploy remains a no-op.

Agent initialization must finish the project handoff before it writes the
agent `uses` edge. A missing or ambiguous project result is a hard failure,
not a reason to emit an unbound agent.

### Telemetry and errors

The projects init span records only low-cardinality values:

```text
invocation_source = direct | azure.ai.agents
project_mode = new | existing_id | existing_endpoint | legacy_migration
mutation = created | updated | noop
model_action = create | reuse | skip
infra_provider = none | bicep | terraform
```

It never records subscription IDs, tenant IDs, resource groups, project names,
endpoints, model deployment names, or file paths.

Project selection, model selection, service merge, environment persistence,
and IaC eject use distinct structured error codes. Delegated failures retain
projects as the originating component. Agents adds context such as
"project setup failed during agent initialization" without replacing the
original suggestion.

Deprecation telemetry counts use of agent-owned project flags. It does not
emit a warning when a user runs plain `azd ai agent init`, because that remains
a supported first-run experience.

### Test strategy

Projects unit tests cover:

- Command registration, flags, exclusions, and `--no-prompt`.
- New workspace and existing workspace behavior.
- New, existing-ID, and endpoint-only modes.
- Service key selection and collision suffixes.
- Exact no-op behavior on a second run.
- Narrow field merge and preservation of unknown properties.
- Deployment merge, conflict, `$ref`, and `--force` behavior.
- Multiple project service rejection.
- Legacy config copy and provider fallback.
- Environment values and user-tenant credential selection.
- Request and result schema version rejection.
- IaC ownership and error text.

Agents unit tests cover:

- Request construction from every init path.
- Compatibility flag translation.
- Project command failure before agent service mutation.
- Result validation and temporary file cleanup.
- Generic adoption without decoding project properties.
- Preservation and deduplication of agent `uses`.
- Absence of direct project `AddService` calls.

Core tests cover structured error status details through
`WorkflowService.Run`.

End-to-end coverage runs:

- Direct greenfield project init followed by provision.
- Direct existing-project init with an existing deployment.
- Plain agent init from an empty directory, proving the one-command flow.
- Agent init into a project initialized separately.
- Adoption of a unified manifest.
- Provision of an unchanged pre-split manifest.
- Two identical init runs with no second-run manifest diff.

## Part 3: Dependencies that need PM confirmation

### Brownfield infrastructure eject

[Issue #9127](https://github.com/Azure/azure-dev/issues/9127) and draft
[PR #9348](https://github.com/Azure/azure-dev/pull/9348) change what eject can
do for an existing project. PM must confirm whether that behavior is required
before this ownership handoff ships, or whether the projects command should
retain the current refusal and pick up brownfield eject later.

### Existing projects under another provisioning provider

An azd workspace can declare only one project-wide `infra.provider`. PM must
confirm the supported experience when an app already uses Bicep or Terraform
and adds a Foundry project. This spec prevents destructive provider
replacement, but full multi-provider composition needs separate core and
provisioning design.

### Shared-resource teardown

[Issue #6215](https://github.com/Azure/azure-dev/issues/6215) tracks deletion
semantics for existing or shared Foundry projects. PM must confirm that this
milestone can ship with the current behavior: referenced projects are kept,
while a general shared-resource ownership model remains out of scope.

### Sibling resource ownership

[Issues #9086](https://github.com/Azure/azure-dev/issues/9086),
[#9087](https://github.com/Azure/azure-dev/issues/9087),
[#9088](https://github.com/Azure/azure-dev/issues/9088), and
[#9089](https://github.com/Azure/azure-dev/issues/9089) cover the same
ownership model for other Foundry resources. PM must confirm whether projects
ships independently or as one coordinated init-handoff milestone. Agents
cannot fully stop authoring all non-agent services until the sibling commands
exist.

### Release train

The projects release must land before or with the agents release that invokes
its init contract. PM and release owners must confirm a coordinated update to
`azure.ai.projects`, the agents dependency in
`cli/azd/extensions/azure.ai.agents/extension.yaml`, and the
`microsoft.foundry` meta-package. The registry must not offer an agents version
whose required projects command is outside its dependency range.

## Part 4: Open questions

1. How many beta releases should keep the project-related flags on
   `azd ai agent init` before they become hidden or are removed?
2. Should direct `azd ai project init` default the optional model prompt to
   yes or no? Agent-delegated init can continue to request a model when the
   selected agent template needs one.
3. Should a future schema add `resourceId`, or should
   `AZURE_AI_PROJECT_ID` remain environment-only? A field would improve
   discoverability but creates precedence and portability questions.
4. Should `azd ai project init --output json` return the full delegated result
   shape, or only a stable user-facing summary that omits internal fields such
   as `networkInjected`?
5. When an explicit model request conflicts with a same-name deployment in a
   `$ref` file, should `--force` be allowed to add an inline override, or
   should referenced definitions always require manual edits?

## Summary of required changes

### `azure.ai.projects`

- Add `azd ai project init` with interactive and non-interactive project setup.
- Add versioned delegated request and result handling for extension callers.
- Move project discovery, Azure context, model selection, and environment setup from agents.
- Add idempotent project service creation and narrow property merge.
- Add additive migration from compatible pre-split project properties.
- Move IaC eject, synthesis code, and embedded templates from agents.
- Update command help, README examples, migration guidance, and changelog.
- Add unit and end-to-end coverage for direct and delegated initialization.

### `azure.ai.agents`

- Delegate project setup through `WorkflowService.Run`.
- Keep plain `azd ai agent init` as a one-command first-run experience.
- Forward project-related flags during the compatibility window.
- Remove project service authoring and property decoding.
- Use the returned project service key only to update agent `uses`.
- Remove duplicated project selection, synthesis, and IaC code after handoff.
- Update help, README guidance, migration warnings, and changelog.
- Add tests proving agents no longer calls `AddService` for the project host.

### azd extension framework

- Preserve structured extension errors across nested workflow commands.
- Add status-detail round-trip tests for workflow command failures.

### Packaging and release

- Raise the agents dependency to the first projects version with init handoff.
- Update the `microsoft.foundry` meta-package dependency set.
- Publish projects before or atomically with agents.
- Verify current and pre-split manifests with the coordinated package set.
