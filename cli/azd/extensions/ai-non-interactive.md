# AI extension non-interactive input reference

<!-- cspell:ignore Abac ABAC -->

`azd` may enable no-prompt mode automatically in CI and supported coding-agent
environments. Every prompt in `azure.ai.agents` and `azure.ai.projects` must
therefore have a deterministic alternative.

When adding or changing a prompt, update this table with its flag,
environment/configuration input, or documented default. A required prompt with
no non-interactive equivalent is a bug.

## `azure.ai.agents`

| Command or area | Interactive decision | Non-interactive input | Behavior when omitted in no-prompt mode |
| --- | --- | --- | --- |
| `ai agent init` | azd environment name | Global `--environment` or `AZD_ENVIRONMENT` | Uses the command's documented derived environment name. |
| `ai agent init` | Source or template | `--template` with an `azure.yaml` project document, `--src`, `--image`, or the hidden automation-only `--kind` | Infers the flow from supplied inputs; otherwise returns actionable missing-input guidance. Legacy standalone agent files are rejected with migration guidance. |
| `ai agent init` | Copy a local `azure.yaml` project | `--template` identifies the source `azure.yaml`; `--src` selects the target directory | Copies the project and its referenced files without an additional prompt. |
| `ai agent init` | Foundry project: existing or new | `--project-id` selects an existing project | Creates or defers a new project configuration when no project ID is supplied. |
| `ai agent init` | Azure subscription | `AZURE_SUBSCRIPTION_ID`, or the subscription embedded in `--project-id` | Defers Azure setup with actionable guidance when the value cannot be resolved. |
| `ai agent init` | Azure location | `AZURE_LOCATION`, `AZURE_AI_DEPLOYMENTS_LOCATION`, or the location of `--project-id` | Uses the configured value or defers Azure setup with actionable guidance. |
| `ai agent init` | Agent name | `--agent-name` or adopted service name | Uses the supplied/inferred name; errors when a name is required and cannot be inferred. |
| `ai agent init` | Deployment mode and code settings | `--deploy-mode`, `--runtime`, `--entry-point`, and `--dep-resolution` | Uses documented runtime/dependency defaults where possible; required unresolved values return guidance. |
| `ai agent init` | Protocols | Repeatable `--protocol` | Uses the documented default protocol set. |
| `ai agent init` | Existing model deployment or new model | `--model-deployment`, `--model`, and `--project-id` | Uses the supplied deployment/model; required unresolved model choices return guidance. |
| `ai agent init` | Azure Container Registry connection | `--acr-connection` with `--project-id` or a persisted `AZURE_AI_PROJECT_ID`; persisted `AZURE_AI_PROJECT_ACR_CONNECTION_NAME`; or persisted `AZURE_CONTAINER_REGISTRY_ENDPOINT` / `AZURE_CONTAINER_REGISTRY_RESOURCE_ID` | Reuses persisted state. With multiple remaining connections, selects the first connection alphabetically. With none, creates a registry during provisioning. |
| `ai agent init` | Source-build registry for a new Foundry project | Enable `AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE=true`, then use `--acr-resource-id` or persisted `AZURE_CONTAINER_REGISTRY_RESOURCE_ID` | Creates a registry when source builds need one and no reuse intent exists. Reuse intent with the switch off fails without mutation; no registry picker is added. |
| `ai agent init` | Application Insights | Foundry project service configuration | The current init flow delegates creation or reconciliation to the provisioning provider without prompting. |
| `ai agent init` | Values referenced by `${NAME}` in `azure.yaml` | `azd env set NAME value` before running init, or existing azd environment values | Leaves unresolved optional references for explicit configuration and reports required missing values. |
| `ai agent add` | Current project, Agent service key, and definition | Positional `<name>` and required `--file`; optional `--project` and `--source` | Accepts YAML or JSON definitions and writes a service-level root `$ref` to `--file`, relative to the directory containing `azure.yaml`. Runs without prompts or Azure authentication and rejects missing or multiple `azure.ai.project` services before writing. |
| `ai agent deploy` | Build from the Dockerfile or use the configured pre-built image | `azd ai agent init --image <registry/image[:tag]>` persists the image choice; for an existing service with an image, run `azd env set AZD_AGENT_SKIP_ACR true` | Builds a new image from the Dockerfile. |
| `ai agent optimize` | Agent, endpoint, models, dataset, evaluators, and candidate count | Positional agent or `--agent`; `--project-endpoint`; GA v1 `--config`; `--eval-config`; `--eval`; `--dataset` as a local file or `name:version`; `--eval-model`; `--optimize-model`; repeatable `--evaluator`; `--max-candidates` | Uses config and detected project state; required unresolved values and ambiguous evaluation selections fail with guidance. |
| `ai agent optimize` | Instruction and skills directory | GA baseline and optimization-space fields, or detected agent metadata/project directories | Preserves explicit GA request inputs; required unresolved baseline inputs fail with guidance. |
| `ai agent delete` | Destructive confirmation | `--force` | Refuses deletion unless `--force` is present. |

The deprecated `ai agent eval` command group contains migration-only stubs.
Invocations exit non-zero with installation and replacement-command guidance,
without prompts, authentication, service requests, or file changes. Old arguments
are not forwarded; `--help` remains available. Use the standalone
[`azure.ai.evaluations` extension](./azure.ai.evaluations/README.md) for evaluations.

## `azure.ai.projects`

### Shared existing-ACR preview switch

The agents and projects extensions share one default-off process switch,
`AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE`. Enable it before setup and in every CI
job that provisions, previews, packages, publishes, deploys, ejects, recovers,
or tears down an environment using the new external-registry capability:

```sh
export AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE=true
```

```powershell
$env:AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE = 'true'
```

For example, a GitHub Actions job can declare:

```yaml
env:
  AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE: 'true'
```

The reader accepts only `strconv.ParseBool` true forms. Missing, empty, false,
invalid, and whitespace-padded values leave it disabled; `on` does not enable
it. It reads the current process, including host injection, not core alpha
configuration or dotenv files directly. A host-injected value can override
the parent shell value; see the [process source boundary](azure.ai.agents/docs/specs/preview-feature-flags.md#evaluator-source-boundary).

With the switch off, valid reuse inputs fail with a structured
`feature_not_enabled` compatibility error naming the switch. Pure local
flag errors, including explicit empty values and conflicts, still win.
No project files, environment/config values, saved context, or Azure resources
are changed. This applies to pending environment selections, canonical reuse
bindings with missing legacy aliases, and unfinished access cleanup. **Down,
preview, recovery, and eject require re-enabling the switch**, even for an
already deployed environment. Disabling it is not a cleanup action and does
not transfer ownership or silently choose registry creation.

Automatic registry creation and old existing-project connection, image, and
code workflows remain available when there is no new reuse intent. A verified
legacy azd-created registry ID alone is not reuse intent.

Supported embedded and currently generated ejected Bicep use the
`microsoft.foundry` provider. Retain that provider for mandatory enforcement,
including preview and down. Manually switching the Foundry layer to core
`infra.provider: bicep` is unsupported and outside this contract: core Bicep
preview skips extension lifecycle hooks. Arbitrary direct external IaC
execution is also outside the flag's enforcement contract.

### Registry selection and ownership

For a new Foundry project that builds hosted-agent images from source, pass
`--acr-resource-id "<registry-arm-id>"` to `azd ai agent init`, or use
`azd ai project add --new-project --acr-resource-id "<registry-arm-id>"`.
The projects extension saves the choice in the active environment. An explicit
flag takes precedence over the environment's selection before provisioning. Scripts can
also set `AZURE_CONTAINER_REGISTRY_RESOURCE_ID` with `azd env set` to the full
ARM ID of an existing `Microsoft.ContainerRegistry/registries` resource.
Provisioning reuses it, grants the project identity pull access, and creates the
project connection without changing the registry's networking. Classic registries
receive `AcrPull`; registries with `AbacRepositoryPermissions` receive
`Container Registry Repository Reader` instead (`AcrPull` is not honored there).
Ensure the registry is reachable from the agent network. The registry must be
in the Foundry project's subscription (`AZURE_SUBSCRIPTION_ID`) and outside the
Foundry resource group (which `azd down` deletes). Cross-subscription reuse is
rejected, including environment-only selections and ejected parameters. This is
supported by Bicep, not Terraform eject in reuse mode. `azd down` never deletes the
external registry.

Without a selection, provisioning creates a registry only when source builds
need one. Code deployments and prebuilt images do not need a source-build
registry. Explicit empty or malformed IDs, code/prebuilt-only configurations,
conflicting connection selectors, and Terraform reuse fail rather than silently
ignoring the selection. No registry picker is added.

Registry selection uses one complete environment snapshot. A failed environment
read stops provisioning rather than falling back to registry creation. A
registry in the Foundry teardown group is rejected even during project-only
setup, unless a deployed binding or matching legacy deployment outputs identify
an azd-created registry. Legacy ownership is verified against Azure deployment
state before provisioning or deleting the group. Environment-only selections
are reported as externally owned just like flag-based selections.
Source initialization validates against the root or Foundry-layer resource group
retained by setup before changing project files or environment values. Locations
chosen during provisioning prompts are applied when expanding
`${AZURE_LOCATION}` in service configuration without rereading the registry
selection.

Project-only provisioning preserves a pending registry selection until source
builds need it, including after project-only teardown. Configured project or
account names do not discard an unused selection. Interactive setup revalidates
the selector after resolving the subscription, before saving project services
or environment values.
Provisioning preview checks the same existing pull access as deployment without
creating grants or persisting pending access operations.

After a failed or canceled deployment, ownership recovery for the resource group
and registry access grants is attempted independently. Errors are reported
without skipping the other recovery operation.

A deployed project keeps its registry choice across repeated provisioning.
Registry migration is not supported: changing the selector or resetting
`AZD_FOUNDRY_ACR_MODE` cannot switch a deployed project to another registry.
Use a separate environment for a different deployment. After a successful
`azd down`, a new deployment can select another registry. Down preserves the
external registry and its images, and removes only access grants and related
resources that azd can establish it owns. Administrator-managed grants remain.
After successful teardown, azd removes this environment's subscription-level
Foundry deployment record before clearing lifecycle state. A subsequent provision
starts a new deployment instead of recovering the deleted project. Cleanup
failures retain the recovery state for retry.
Custom deployment roles need `Microsoft.Resources/deployments/delete` at the
project subscription scope to retire that deployment record.

The deployer needs permission to create role assignments on the external
registry (for example, Owner, User Access Administrator, or Role Based Access
Control Administrator scoped to the registry) to grant the project identity
`AcrPull` or `Container Registry Repository Reader` (for ABAC registries). The
image publisher also needs `AcrPush` (or `Container Registry Repository Writer`
on ABAC registries) to push images.

Ejected Bicep parameters include
`existingAcrResourceId = "${AZURE_CONTAINER_REGISTRY_RESOURCE_ID}"` only in reuse
mode, rather than embedding a literal registry ID. Eject uses the established
registry choice when present, otherwise the same registry-ID decision rule.
Create mode supports either Bicep or Terraform eject; reuse mode requires
Bicep eject. Existing deployment bindings also apply to ejected infrastructure;
editing the registry parameter does not enable migration.

| Command or area | Interactive decision | Non-interactive input | Behavior when omitted in no-prompt mode |
| --- | --- | --- | --- |
| `microsoft.foundry` provisioning | Azure subscription | `AZURE_SUBSCRIPTION_ID` in the active azd environment | Returns actionable missing-subscription guidance. |
| `microsoft.foundry` provisioning | Azure location | `AZURE_LOCATION` in the active azd environment | Returns actionable missing-location guidance. |
| Projects provisioning and legacy agents provisioning | Model deployments for the current subscription/location | `AI_PROJECT_DEPLOYMENTS` JSON array in the active azd environment (`[]` means no deployments) | With `--no-prompt` / `AZD_NO_PROMPT=true`, saves YAML deployments unchanged when unset or blank. Existing environment overrides are preserved; embedded infrastructure uses them instead of YAML defaults. Ejected user parameters retain precedence. |
| `azd down` with `microsoft.foundry` | Delete the Foundry resource group | `azd down --force` | Refuses deletion and instructs the caller to pass `--force`. |
