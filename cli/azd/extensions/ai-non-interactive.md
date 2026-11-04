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

| Command or area | Interactive decision | Non-interactive input | Behavior when omitted in no-prompt mode |
| --- | --- | --- | --- |
| `microsoft.foundry` provisioning | Azure subscription | `AZURE_SUBSCRIPTION_ID` in the active azd environment | Returns actionable missing-subscription guidance. |
| `microsoft.foundry` provisioning | Azure location | `AZURE_LOCATION` in the active azd environment | Returns actionable missing-location guidance. |
| Projects provisioning and legacy agents provisioning | Model deployments for the current subscription/location | `AI_PROJECT_DEPLOYMENTS` JSON array in the active azd environment (`[]` means no deployments) | With `--no-prompt` / `AZD_NO_PROMPT=true`, saves YAML deployments unchanged when unset or blank. Existing environment overrides are preserved; embedded infrastructure uses them instead of YAML defaults. Ejected user parameters retain precedence. |
| `azd down` with `microsoft.foundry` | Delete the Foundry resource group | `azd down --force` | Refuses deletion and instructs the caller to pass `--force`. |
