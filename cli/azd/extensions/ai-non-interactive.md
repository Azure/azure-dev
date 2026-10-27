# AI extension non-interactive input reference

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
| `ai agent init` | Source-build registry for a new Foundry project | `--acr-resource-id` or persisted `AZURE_CONTAINER_REGISTRY_RESOURCE_ID` | Creates a registry when source builds need one. An explicit selector is saved by project setup without a registry picker. |
| `ai agent init` | Application Insights | Foundry project service configuration | The current init flow delegates creation or reconciliation to the provisioning provider without prompting. |
| `ai agent init` | Values referenced by `${NAME}` in `azure.yaml` | `azd env set NAME value` before running init, or existing azd environment values | Leaves unresolved optional references for explicit configuration and reports required missing values. |
| `ai agent add` | Current project, Agent service key, and definition | Positional `<name>` and required `--file`; optional `--project` and `--source` | Accepts YAML or JSON definitions and writes a service-level root `$ref` to `--file`, relative to the directory containing `azure.yaml`. Runs without prompts or Azure authentication and rejects missing or multiple `azure.ai.project` services before writing. |
| `ai agent deploy` | Build from the Dockerfile or use the configured pre-built image | `azd ai agent init --image <registry/image[:tag]>` persists the image choice; for an existing service with an image, run `azd env set AZD_AGENT_SKIP_ACR true` | Builds a new image from the Dockerfile. |
| `ai agent eval generate` | Agent service and Foundry project | `--agent` and `--project-endpoint` | Outside an azd project, required unresolved context fails without prompting. |
| `ai agent eval generate` | Suite name, instruction, dataset, evaluators, model, sample count, and output | `--name`, `--gen-instruction`, `--gen-instruction-file`, `--dataset`, repeatable `--evaluator`, `--eval-model`, `--max-samples`, and `--out-file` | Uses documented defaults; required unresolved generation inputs fail with guidance. |
| `ai agent eval generate` | Replace an existing config | `--reset-defaults` | Keeps the existing config unless reset was explicitly requested. |
| `ai agent eval run` | Agent service and Foundry project | `--agent` and `--project-endpoint` | Uses `eval.yaml` and azd environment state; outside a project the endpoint must be explicit. |
| `ai agent eval run` | Run name and reuse of an existing eval | `--name`; existing eval state | Uses the config name and reuses the existing eval. |
| `ai agent eval list`, `show` | Foundry project | `--project-endpoint` | Resolves the project endpoint from explicit or environment state and never prompts for an agent. |
| `ai agent eval update` | Agent service and Foundry project | `--agent` and `--project-endpoint` | Resolves only from explicit/project/environment state and never prompts. |
| `ai agent eval update` | Dataset and evaluator updates | `--dataset-only` or `--evaluator-only` | Updates every locally changed asset type when neither limiting flag is supplied. |
| `ai agent optimize` | Agent, endpoint, models, dataset, evaluators, and candidate count | Positional agent or `--agent`; `--project-endpoint`; `--config`; `--dataset`; `--eval-model`; `--optimize-model`; repeatable `--evaluator`; `--max-candidates` | Uses config and detected project state; required unresolved values fail with guidance. |
| `ai agent optimize` | Instruction and skills directory | Instruction/skill paths in the optimize config or detected agent metadata/project directories | Requires an instruction and auto-uses a detected skills directory, or no skills when none is found. |
| `ai agent delete` | Destructive confirmation | `--force` | Refuses deletion unless `--force` is present. |

## `azure.ai.projects`

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
outside the Foundry resource group (which `azd down` deletes). This is supported
by Bicep, not Terraform eject in reuse mode. `azd down` never deletes the
external registry.

Without a selection, provisioning creates a registry only when source builds
need one. Code deployments and prebuilt images do not need a source-build
registry. Explicit empty or malformed IDs, code/prebuilt-only configurations,
conflicting connection selectors, and Terraform reuse fail rather than silently
ignoring the selection. No registry picker is added.

A deployed project keeps its registry choice across repeated provisioning.
Registry migration is not supported: changing the selector or resetting
`AZD_FOUNDRY_ACR_MODE` cannot switch a deployed project to another registry.
Use a separate environment for a different deployment. After a successful
`azd down`, a new deployment can select another registry. Down preserves the
external registry and its images, and removes only access grants and related
resources that azd can establish it owns. Administrator-managed grants remain.

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
