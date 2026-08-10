<!-- cspell:ignore agentsV2 azd azdext brownfield DataZoneStandard eastus -->
<!-- cspell:ignore dotenv exterrors greenfield grpcserver idempotency -->
<!-- cspell:ignore modelrouter noopt OpenAI sku structpb toolboxes unsets -->

# Foundry project service ownership technical design

## Source design and implementation boundary

This specification is the engineering design for the product decisions in
[PR #9441](https://github.com/Azure/azure-dev/pull/9441). It builds on the
project provisioning transfer in
[PR #9133](https://github.com/Azure/azure-dev/pull/9133). It does not redefine
the product requirements or the public command grouping selected there.

The implementation has three owners:

- `azure.ai.projects` owns the `azure.ai.project` service, project selection,
  project environment state, managed model deployment declarations, and
  Foundry infrastructure generation.
- `azure.ai.agents` owns agent source, manifests, runtimes, images, agent
  services, and references to existing external model deployments.
- Core `azd` owns only the extension workflow transport and environment APIs
  needed for safe composition.

The public command `azd ai project deployment add` is an intentional exception
to the verb-first guidance in
`cli/azd/docs/extensions/extensions-style-guide.md`. The product design uses
the noun hierarchy to make project ownership visible and to leave room for
other project deployment operations.

## Part 1: End-to-end experience

### 1.1 Initialize a new project

`azd ai project init` initializes the current directory as an azd project when
needed, creates one `azure.ai.project` service, and configures Foundry
infrastructure. It does not select or declare a model.

```bash
azd ai project init
```

The interactive flow asks only for values that cannot be resolved from an
explicit flag, the current environment, or existing project configuration:

```text
? Select how to configure the Foundry project:
> Create a new Foundry project
  Use an existing Foundry project

? Select an Azure subscription: ...
? Select an Azure location: ...

Foundry project configuration added to azure.yaml.
Run `azd ai project deployment add` to add a managed model deployment.
```

For a new project, the resulting service has no endpoint because the endpoint
does not exist until provisioning completes:

```yaml
name: chat-app
metadata:
  template: chat-app@0.0.1
infra:
  provider: microsoft.foundry
services:
  ai-project:
    host: azure.ai.project
```

If `azure.yaml` already declares an infrastructure provider, the command
preserves it. If no provider exists, the command writes
`microsoft.foundry` only when the workspace has no user-owned infrastructure
path or files. It never replaces an existing non-Foundry provider. Explicit
infrastructure ejection is described in section 1.10.

`--no-prompt` deterministically selects a new project when neither
`--project-id` nor `--project-endpoint` is supplied. For project-only setup,
missing Azure subscription and location are deferred until deployment
selection or provisioning. Agent delegation requests immediate Azure context,
so missing values on that path produce a structured error instead of opening a
prompt.

```bash
azd ai project init --no-prompt
```

### 1.2 Adopt an existing project by resource ID

A resource ID is the preferred input for an existing project because it
contains the subscription, resource group, account, and project names needed
by the provisioning provider.

```bash
azd ai project init \
  --project-id "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/ai-rg/providers/Microsoft.CognitiveServices/accounts/ai-account/projects/chat-project"
```

The command validates the resource ID, looks up the project, verifies the
endpoint, and writes only the project-owned service field:

```yaml
services:
  chat-project:
    host: azure.ai.project
    endpoint: https://ai-account.services.ai.azure.com/api/projects/chat-project
```

It also writes the canonical environment values needed by the existing
resource path:

```text
AZURE_SUBSCRIPTION_ID=00000000-0000-0000-0000-000000000000
AZURE_TENANT_ID=11111111-1111-1111-1111-111111111111
AZURE_LOCATION=eastus2
AZURE_AI_PROJECT_ID=/subscriptions/.../projects/chat-project
AZURE_RESOURCE_GROUP=ai-rg
AZURE_AI_ACCOUNT_NAME=ai-account
AZURE_AI_PROJECT_NAME=chat-project
FOUNDRY_PROJECT_ENDPOINT=https://ai-account.services.ai.azure.com/api/projects/chat-project
AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT=https://ai-account.services.ai.azure.com/api/projects/chat-project
AZURE_OPENAI_ENDPOINT=https://ai-account.openai.azure.com
AZURE_AI_DEPLOYMENTS_LOCATION=eastus2
USE_EXISTING_AI_PROJECT=true
```

When the project belongs to a tenant different from the resource tenant, the
command authenticates through the user's access tenant. A subscription chosen
by `PromptSubscription()` therefore always uses
`Subscription.UserTenantId`. An explicitly supplied subscription is resolved
through `LookupTenant()` before credentials are created.

### 1.3 Adopt an existing project by endpoint

An endpoint is accepted for tools that need project connectivity but do not
manage Azure resources:

```bash
azd ai project init \
  --project-endpoint https://ai-account.services.ai.azure.com/api/projects/chat-project
```

The endpoint is normalized with `validateProjectEndpoint` and written to the
project service and active environment. Any stale ARM identity from a previous
resource-ID adoption is removed.

Endpoint-only projects cannot add a managed deployment or generate
brownfield infrastructure because those operations require a complete project
resource ID. The error directs the user to rerun `project init` with
`--project-id`. The first implementation also requires a project ID when agent
init must validate an existing external deployment because the current
`listProjectDeployments` helper uses the ARM deployments client. Endpoint-only
mode remains valid when no resource or deployment reconciliation is needed.
Project init rejects endpoint-only mode before mutation when it would associate
existing managed deployments or network declarations with a different
endpoint. Repeating the same endpoint remains an allowed no-op.

### 1.4 Reconcile an existing project service

Only one `azure.ai.project` service is supported. If one exists, `project init`
reuses its key and updates only `endpoint`. It never replaces `deployments`,
`network`, hooks, `uses`, or unknown fields.

When the requested project differs from the configured project, interactive
mode shows both identities and asks for confirmation:

```text
The current environment points to:
  https://old-account.services.ai.azure.com/api/projects/old-project

Replace it with:
  https://new-account.services.ai.azure.com/api/projects/new-project

? Update the project configuration? (y/N)
```

`--no-prompt` does not switch projects unless the explicit
`--project-id` or `--project-endpoint` identifies the replacement. Explicit
input always wins over defaults. Repeating the same command produces no
`azure.yaml` diff and reports `unchanged`.

If multiple project services exist, the command fails before any file or
environment mutation and lists the conflicting service keys.

### 1.5 Add an azd-managed model deployment

The deployment command selects a model, resolves a deployable version and SKU,
and adds a declaration to the existing project service:

```bash
azd ai project deployment add
```

The interactive sequence is:

```text
? Select a model: gpt-4.1
? Select a model version: 2025-04-14
? Select a deployment type: Global Standard
? Deployment name: chat

Managed deployment `chat` added to services.ai-project.deployments.
```

The command writes the resolved values rather than defaults that could change
between runs:

```yaml
services:
  ai-project:
    host: azure.ai.project
    deployments:
      - name: chat
        model:
          format: OpenAI
          name: gpt-4.1
          version: "2025-04-14"
        sku:
          name: GlobalStandard
          capacity: 10
```

In non-interactive mode, `--model` is required. The model may use the
`publisher/model` form accepted by the current model catalog. The command does
not inherit the agent extension's preferred model. When `--name` is absent,
the deployment name defaults to the resolved catalog model name without its
publisher prefix. A conflict is reported rather than silently adding a suffix.

```bash
azd ai project deployment add \
  --model OpenAI/gpt-4.1 \
  --name chat \
  --no-prompt
```

The command resolves version, SKU, capacity, and location from explicit input,
the model catalog, and quota data. If more than one valid choice remains in
`--no-prompt` mode, it returns an error naming the additional input needed. It
does not silently choose the first catalog entry.

Deployment names are compared case-insensitively because Azure treats them as
case-insensitive. An exact existing declaration is an idempotent success and
preserves its original spelling. A declaration with the same name and
different model settings is a conflict. `--force` may replace an inline
declaration after confirmation rules are satisfied, but it cannot rewrite a
referenced file.

An external deployment that already exists in Azure is not added to
`azure.yaml`. That path remains an agent operation:

```yaml
models:
  chat:
    type: azure_openai
    model: existing-production-deployment
```

### 1.6 Preserve referenced configuration

Deployment item references remain intact:

```yaml
services:
  ai-project:
    host: azure.ai.project
    deployments:
      - $ref: ./deployments/chat.yaml
      - name: embeddings
        model:
          format: OpenAI
          name: text-embedding-3-large
          version: "1"
        sku:
          name: GlobalStandard
          capacity: 10
```

The command resolves the referenced item for comparison. If the requested
deployment is identical, it returns `unchanged`. If the same name has
different settings, it fails and tells the user to edit the referenced file.
If the name is new, the command appends an inline item without changing the
reference.

A service-level `$ref` is safe to read but is not safe to mutate through the
current shallow overlay model. If a requested operation would add or change
`endpoint` or `deployments`, the command fails with instructions to edit the
referenced service file or inline the service first. A no-op may succeed.

### 1.7 Initialize an agent through the project extension

The public agent experience remains one command:

```bash
azd ai agent init
```

The agent extension performs its source and manifest work, then delegates
project setup and each new managed deployment to `azure.ai.projects`.
Delegation is not shown as a second command. The projects extension owns any
project and model prompts, while the agent extension owns agent prompts.

For an agent with one managed model, the final configuration resembles:

```yaml
services:
  ai-project:
    host: azure.ai.project
    deployments:
      - name: chat
        model:
          format: OpenAI
          name: gpt-4.1
          version: "2025-04-14"
        sku:
          name: GlobalStandard
          capacity: 10
  agent:
    host: azure.ai.agent
    project: src
    uses:
      - ai-project
```

The agent request adds the `agentsV2` model capability constraint. A model
available in a location without that capability is excluded even if unrelated
usage data is empty or unknown. For a manifest with multiple managed models,
the agent extension delegates one deployment at a time and injects each final
deployment name into the manifest. Only the first resolved model updates the
legacy `AZURE_AI_MODEL_DEPLOYMENT_NAME` value.

If the agent chooses an existing Azure deployment, it verifies the deployment
and injects the existing name without calling `project deployment add`. When
that external deployment is the first resolved model, agents also persists it
as `AZURE_AI_MODEL_DEPLOYMENT_NAME`.

Existing agent automation remains valid:

```bash
azd ai agent init \
  --project-id "<project-resource-id>" \
  --model OpenAI/gpt-4.1 \
  --infra
```

The agent maps `--project-id` and `--infra` into its project init request and
maps `--model` into a deployment add request when a new managed deployment is
needed. These three project-authoring flags write a deprecation notice to
stderr with the equivalent project command. `--model-deployment` remains an
agent-owned input for an existing external deployment and does not show that
notice. Plain `azd ai agent init` remains supported and shows no deprecation
notice.

`azd ai agent init --output json` writes exactly one JSON document. Nested
project operations never write their own JSON document to stdout:

```json
{
  "serviceName": "agent",
  "projectServiceName": "ai-project",
  "models": {
    "chat": "chat"
  }
}
```

### 1.8 Adopt or scan an existing agent project

Agent adoption treats `azure.ai.project` as an opaque host dependency. It
discovers the single project service key and merges that key into the agent
service's `uses` list. It does not parse or rewrite the project's `endpoint`,
`deployments`, or `network`.

Hand-authored `uses` entries keep their order. The project key is appended only
when no case-sensitive exact entry exists. Existing duplicates are not
silently removed because that would modify unrelated user-authored content.

### 1.9 Migrate a pre-split configuration

The provisioning provider continues to accept the legacy
`azure.ai.agent` and `microsoft.foundry` hosts during migration. When no
`azure.ai.project` service exists and exactly one legacy service contains
project-owned fields, `project init` creates a dedicated project service and
copies only raw `endpoint`, `deployments`, and `network` values. It does not
remove the legacy fields.

```text
Foundry project configuration was copied to services.ai-project.
Legacy project fields were left unchanged for compatibility.
```

The new key follows the normal deterministic naming rules. A legacy service
with a service-level `$ref` is not automatically copied because doing so would
materialize or relocate referenced content. The provider compatibility path
continues to work, but project init fails without mutation and tells the user
how to make the split explicit.

This compatibility is removed only after the coordinated extension rollout
described in section 2.14.

### 1.10 Generate infrastructure

Without `--infra`, the `microsoft.foundry` provider keeps infrastructure
generation internal. Users who want editable files can eject either supported
format during project initialization:

```bash
azd ai project init --infra
azd ai project init --infra=terraform
```

A bare `--infra` means `--infra=bicep`. Because the flag has an optional value,
Terraform uses the equals form. The implementation moves the current
`parseInfraProvider` and `ejectInfra` behavior from agents into projects:

- Bicep writes the current `infra/main.bicep` tree and preserves the Foundry
  provider behavior.
- Terraform writes the current Terraform tree, stamps
  `infra.provider: terraform`, and removes a starter `infra.path`.
- Existing user-owned infrastructure is never merged or overwritten.

Agent initialization forwards its existing `--infra` value to project init. It
does not retain another project synthesizer or ejection implementation.

Brownfield ejection is not required for this ownership transfer. Existing
limitations remain tied to issue #9127 and PR #9348. A project that already
uses an incompatible provider receives a structured error before files change.

### 1.11 Partial failure and retry

Each delegated operation first parses and validates the complete request,
resolves Azure choices without changing project files, and computes the full
mutation. Project init then applies environment reconciliation before the
narrow `azure.yaml` mutation. Deployment add applies the `azure.yaml` merge
before its optional default-deployment environment write. Both operations
atomically write the result file last.

If project initialization fails, no deployment or agent service is written. If
one deployment in a multi-model agent succeeds and a later deployment fails,
the successful declaration remains. Rerunning the command recognizes it as an
exact match and resumes with the next model. Agent source already downloaded
to disk also remains available for the retry.

Environment values are persisted one key at a time through the existing
Environment service. An environment write failure can therefore leave a subset
of the desired values, but it leaves `azure.yaml` unchanged and does not write
a success result. The provisioning provider checks that a managed
existing-project declaration has a matching `AZURE_AI_PROJECT_ID`, so a partial
identity fails closed instead of targeting a different project.

A retry with an explicit project ID or endpoint reconciles both stores to that
target. When no explicit target is present and the environment ID conflicts
with the service endpoint, interactive mode asks whether to update
`azure.yaml` to the environment project or keep the service endpoint and
reconcile the environment as endpoint-only. `--no-prompt` returns
`project_target_mismatch` and requires an explicit `--project-id` or
`--project-endpoint`. The command never chooses one side silently.

If all environment writes succeed and the later service mutation fails, the
same recovery rule applies on retry. This cross-file recovery behavior is
required because separate `.env` and `azure.yaml` saves cannot form one
transaction.

For deployment add, a failure to persist
`AZURE_AI_MODEL_DEPLOYMENT_NAME` may leave a valid deployment declaration.
Rerunning recognizes the declaration as unchanged, retries the environment
write, and then writes the result.

Temporary request and result directories are removed on success, error, and
context cancellation.

### 1.12 Teardown

The ownership split does not change deletion policy:

- A project created through the new-project path is part of the generated
  infrastructure and follows the existing `azd down` behavior.
- A project adopted by resource ID or endpoint has
  `USE_EXISTING_AI_PROJECT=true` and is not deleted by `azd down`.
- External model deployments referenced only by an agent are never deleted.
- Managed deployment declarations follow the project provisioning provider's
  lifecycle.

## Part 2: Technical design

### 2.1 Component boundaries

| Component | Owns after this change | Must no longer own |
|---|---|---|
| `azure.ai.projects` | Project init, project selection, environment identity, project service mutation, managed deployment selection, Foundry synthesis | Agent source, agent service authoring, agent runtime choices |
| `azure.ai.agents` | Source acquisition, manifest processing, existing deployment references, agent service authoring, agent-specific connections | Project service fields, managed deployment declarations, project selection, project synthesis |
| Core `azd` | Workflow execution, structured workflow error transport, project and environment RPCs | Foundry-specific policy or model selection |

The current project command tree is registered in
`cli/azd/extensions/azure.ai.projects/internal/cmd/root.go`. The implementation
adds:

- `newProjectInitCommand` in `internal/cmd/project_init.go`.
- `newProjectDeploymentCommand` and `newProjectDeploymentAddCommand` in
  `internal/cmd/project_deployment_add.go`.
- Request and result validation in
  `internal/cmd/delegated_contract.go`.
- Narrow service reconciliation in
  `internal/cmd/project_service_reconciler.go`.
- Project environment reconciliation in
  `internal/cmd/project_environment.go`.
- Model choice and declaration reconciliation in
  `internal/cmd/project_deployment.go`.

Names above are the required implementation layout. Existing helpers should be
moved into these files rather than copied between extensions.

### 2.1.1 `azure.yaml` schema

This migration does not change
`cli/azd/extensions/azure.ai.projects/schemas/azure.ai.project.json`. The
existing schema already defines `endpoint`, `deployments`, `network`, and
deployment item `$ref` values. The command writes the existing deployment
shape shown in section 1.5.

The project ARM resource ID remains in `AZURE_AI_PROJECT_ID`; it is not added
to the service. This avoids two persisted authorities for identity. The
request and result files are local extension orchestration contracts, not
`azure.yaml` schema.

### 2.2 Public command contracts

#### `azd ai project init`

```text
Usage:
  azd ai project init [flags]

Flags:
      --project-id string        Existing Foundry project ARM resource ID
      --project-endpoint string  Existing Foundry project endpoint
      --infra string[="bicep"]   Eject Bicep or Terraform infrastructure
      --force                    Replace a different configured project
```

`--project-id` and `--project-endpoint` are mutually exclusive. `--infra`
accepts `bicep` or `terraform`; a bare flag resolves to `bicep`. `--force`
removes the interactive replacement confirmation, but it does not bypass
schema validation, infrastructure conflict checks, or the service-level
`$ref` restriction.

The action returns this logical result:

```json
{
  "schemaVersion": 1,
  "producerVersion": "<projects-extension-version>",
  "serviceName": "chat-project",
  "mode": "existing-id",
  "mutation": "created",
  "endpoint": "https://ai-account.services.ai.azure.com/api/projects/chat-project",
  "resourceId": "/subscriptions/00000000-0000-0000-0000-000000000000/resourceGroups/ai-rg/providers/Microsoft.CognitiveServices/accounts/ai-account/projects/chat-project"
}
```

Allowed `mode` values are `new`, `existing-id`, and `existing-endpoint`.
Allowed `mutation` values are `created`, `updated`, `migrated`, and
`unchanged`. `endpoint` and `resourceId` are omitted when unavailable.

#### `azd ai project deployment add`

```text
Usage:
  azd ai project deployment add [flags]

Flags:
      --model string  Model name or publisher/model
      --name string   Deployment name
      --force         Replace a conflicting inline declaration
```

The implementation retains the existing advanced model inputs currently used
by agent initialization when they are needed to disambiguate non-interactive
selection. Inherited azd execution options such as `--no-prompt` and
`--output` are not redefined by the extension. Subscription and location come
from the active environment or interactive selection.

The action returns:

```json
{
  "schemaVersion": 1,
  "producerVersion": "<projects-extension-version>",
  "serviceName": "ai-project",
  "deploymentName": "chat",
  "model": {
    "format": "OpenAI",
    "name": "gpt-4.1",
    "version": "2025-04-14"
  },
  "sku": {
    "name": "GlobalStandard",
    "capacity": 10
  },
  "mutation": "created"
}
```

Allowed `mutation` values are `created`, `replaced`, and `unchanged`.

### 2.3 Delegated request and result files

The extensions compose through the existing `WorkflowService.Run` API, not by
starting a second `azd` process. Two hidden flags add a versioned local IPC
contract to both project actions:

```text
--request-file <absolute-path>
--result-file <absolute-path>
```

The flags are hidden from help and completion. They are not a public extension
API. They allow coordinated versions of Microsoft-owned extensions to exchange
structured data while the core workflow RPC remains `EmptyResponse`.

When `--request-file` is present:

- `--result-file` is required.
- The action rejects overlapping direct action flags.
- The action validates the schema version and every field before invoking
  `scaffoldProject`, `Environment.Select`, or any mutation RPC.
- The workflow step explicitly uses `--output=none`.
- The action does not write to stdout. Human prompts and progress use stderr.
- The result is written to a sibling temporary file, flushed, closed, and
  renamed over `--result-file` only after all requested mutations succeed.
- The parent validates the result schema and semantic values before using it.
- Paths must be absolute, point to regular files within the parent-created
  temporary directory, and must not be symbolic links.

The parent creates the temporary directory with user-only permissions and
removes it with a deferred cleanup. Request files contain identifiers but no
access tokens, connection secrets, or model credentials.

The workflow command also carries SDK-managed workspace options instead of
duplicating them in JSON:

```text
ai project init \
  --request-file=<absolute-path> \
  --result-file=<absolute-path> \
  --output=none \
  --cwd=<project-root> \
  --environment=<environment-name>
```

Agents resolves the same target project root it uses today and passes it as an
explicit child `--cwd`. It passes `--environment` when the caller selected one.
These step-level values override parent globals under the merge rule in
section 2.4. The projects extension derives an environment name only when the
step does not provide one. Deployment add receives the same `--cwd` and
`--environment` values.

#### Project init request

```json
{
  "schemaVersion": 1,
  "source": "azure.ai.agents/init",
  "sourceVersion": "<agents-extension-version>",
  "project": {
    "resourceId": "",
    "endpoint": ""
  },
  "infra": {
    "ejectProvider": "terraform"
  },
  "requirements": {
    "allowedLocations": [
      "eastus2"
    ]
  },
  "resolveAzureContext": true,
  "force": false
}
```

`source` is a low-cardinality enum for diagnostics and telemetry. It does not
select a hidden product default. The allowed values in version 1 are
`azure.ai.agents/init` and `azure.ai.projects/direct`.
`sourceVersion` is required for delegated requests and is used only for
compatibility errors and diagnostics. It is not recorded as telemetry.

An empty `project` object requests the normal new-or-existing selection flow.
`resourceId` and `endpoint` remain mutually exclusive.
`resolveAzureContext` tells the project action whether the caller needs
subscription and location immediately. Agent initialization sets it to
`true`. Direct `--no-prompt` project-only setup sets it to `false`; direct
interactive setup resolves context through its documented prompts. Existing
resource-ID adoption always resolves the tenant and resource regardless of
this field.

`infra.ejectProvider` is optional. Its allowed non-empty values are `bicep`
and `terraform`. An agent invocation forwards the normalized value of its
existing `--infra` flag. An empty value keeps extension-managed
`microsoft.foundry` infrastructure.

`requirements.allowedLocations` is optional. When omitted, project init applies
no consumer-specific location restriction. When present, it must contain at
least one location. The projects extension removes case-insensitive
duplicates, filters the existing-project picker and new-project location
choices, and rejects an explicit project ID outside the allowed set. The
constraint can narrow service-supported locations but cannot expand them.

Agents resolves deployment mode before project init. For code deploy and
prebuilt-image flows, it calls its existing hosted-agent region resolver and
passes the resulting locations in this field. It omits the field for flows
that do not require the hosted-agent restriction. Cancellation and region
lookup failure retain the current agent behavior.

`force` carries explicit caller consent for replacement prompts. It never
bypasses reference safety, schema validation, provider conflicts, or project
target consistency checks.

The result uses the public logical result in section 2.2. The agent uses
`serviceName`, `mode`, and `mutation`; it must not rediscover a project service
by assuming the key is `ai-project`.

#### Deployment add request

```json
{
  "schemaVersion": 1,
  "source": "azure.ai.agents/init",
  "sourceVersion": "<agents-extension-version>",
  "model": {
    "name": "OpenAI/gpt-4.1",
    "deploymentName": "chat",
    "requiredCapabilities": [
      "agentsV2"
    ],
    "allowedLocations": [
      "eastus2"
    ],
    "excludedModelNames": [
      "modelrouter"
    ]
  },
  "setAsDefault": true,
  "force": false
}
```

All arrays are optional and preserve caller order after duplicate removal.
Unknown capability names fail validation. `allowedLocations` narrows the
project-derived locations but cannot expand them. `excludedModelNames` is
compared case-insensitively.

Direct invocation builds the same in-memory request with no required
capabilities and with `setAsDefault=true`. Agent initialization sends
`agentsV2`, sends `setAsDefault=true` only when that managed model is the first
resolved manifest model, and sends one request per managed model. If an
external deployment resolves first, agents persists that name and every
managed request uses `setAsDefault=false`. It forwards its existing `--force`
value to `force`; without that explicit consent, a delegated non-interactive
conflict fails.

The result uses the public logical result in section 2.2. The agent consumes
only the final `deploymentName` and model mapping. It does not duplicate the
declaration in `azure.yaml`.

Version 1 readers reject unknown `schemaVersion` values with a structured
compatibility error that names both installed extension versions and the
minimum compatible version. Request readers reject unknown fields so a typo
cannot fall back to a destructive default. Result readers ignore unknown
fields within version 1 so a newer producer can add diagnostic output without
breaking an older consumer. `producerVersion` is required in delegated
results and is not recorded as telemetry.

### 2.4 Core workflow changes

`cli/azd/internal/grpcserver/workflow_service.go` currently converts every
workflow failure to a plain gRPC `Internal` status. This loses the extension
error code, category, suggestion, and links created by
`pkg/azdext.WrapError`.

Add a host-to-extension wrapper in
`cli/azd/grpc/proto/errors.proto`:

```proto
message WorkflowErrorDetail {
  ExtensionError error = 1;
}
```

The wrapper is necessary because `ExtensionError` is documented as an
extension-to-host message. The nested value still represents the extension
that failed inside the host workflow. The `WorkflowService.Run` response in
`cli/azd/grpc/proto/workflow.proto` remains `EmptyResponse`.
`WorkflowService.Run` must:

1. Call `azdext.WrapError(err)` for the workflow error.
2. Put the returned value in `WorkflowErrorDetail` and attach it as a gRPC
   status detail.
3. Preserve the current gRPC code selection for callers that do not understand
   the detail, except map context cancellation and deadline errors to their
   standard gRPC codes.
4. Return that status without logging a second user-facing error.

Add an `azdext` helper that walks wrapped gRPC errors, extracts
`WorkflowErrorDetail`, and passes its nested value to `azdext.UnwrapError`.
The agents and projects extensions call this helper whenever
`WorkflowService.Run` returns an error. This preserves the originating
structured error at the top-level command.

`workflowCmdAdapter.ExecuteContext` in `cli/azd/cmd/container.go` currently
appends explicitly changed global parameters, including `--output=json`, after
child arguments. This can override an explicit child value. Add a merge helper
that treats the workflow step as the higher-priority source:

1. Parse long flag names from the step arguments in both `--name=value` and
   `--name value` forms.
2. Drop an inherited global argument when the step already supplies that flag.
3. Append only the remaining inherited arguments.
4. Preserve the current special handling for `--environment`.

Every delegated project step supplies `--output=none`.
`scaffoldProject` supplies the same option to its nested `azd init` workflow.
The projects commands register `none` as an accepted output value and avoid
calling a formatter in delegated mode. This makes the parent agent command the
only JSON producer while still inheriting `--no-prompt`, tracing, and other
execution options that the step does not override.

Cancellation from the parent command is already carried through the workflow
context. File writes and Azure calls must use that context and return its
error without converting cancellation into a dependency failure.

`ProjectService.UnsetServiceConfig` also needs an exact-key correction before
the projects extension relies on it for mode transitions. The current server
constructs `services.<service-name>.<path>` and passes that string to the
dot-path config API. A valid service key such as `my.agent` is therefore
interpreted as two nested keys.

Keep the existing RPC shape and mutation lock, but implement the unset in the
same exact-key form as `SetServiceConfigValue`:

1. Load the raw `services` map.
2. Index `services[req.ServiceName]` without parsing the service name as a
   config path.
3. Call `config.NewConfig(serviceConfig).Unset(req.Path)` only within that
   service map.
4. Save and reload through the existing `ProjectService` path.

A missing nested path remains an idempotent success. This core change is
required before project init may unset `endpoint` on a discovered service with
a dotted key. It does not add a new RPC or change the meaning of `req.Path`.

### 2.5 Environment value deletion

Mode changes require real deletion from `.env`, not an empty string. Add
`UnsetValue` to the Environment service in
`cli/azd/grpc/proto/environment.proto` and implement it in
`cli/azd/internal/grpcserver/environment_service.go`:

```proto
rpc UnsetValue (UnsetEnvRequest) returns (EmptyResponse);

message UnsetEnvRequest {
  string env_name = 1;
  string key = 2;
}
```

The server:

1. Resolves the environment using the same rules as `SetValue`.
2. Calls `environment.DotenvDelete(key)`.
3. Saves the environment.
4. Returns `EmptyResponse`.

Deleting a missing key is an idempotent success. Invalid environment names or
keys use the same errors as `SetValue`. The generated client exposes
`Environment().UnsetValue`.

The projects extension batches the desired set and deletion operations in
memory, removes keys from the deletion set when they also have a new value,
applies sets in stable key order, then applies deletions in stable key order.
Each RPC persists one key. On the first failure, the action stops before the
project service mutation and result write. A later invocation recovers through
the explicit-target or interactive mismatch rules in section 2.7.

This feature does not add a bulk environment mutation API. Such an API would
reduce partial environment states, but it would not make the separate
environment and `azure.yaml` saves transactional. The required safety property
is deterministic recovery without provisioning a different project.

### 2.6 Project and environment creation

Move the project-neutral initialization helpers from
`cli/azd/extensions/azure.ai.agents/internal/cmd/init.go` into the projects
extension:

- `ensureProject`
- `deriveEnvName`
- `scaffoldProject`
- `writeFoundryProvider`

The projects implementation calls `Project.Get` to detect the workspace. When
no project exists, it invokes `scaffoldProject` only after the delegated
request has passed schema and argument validation. It creates `azure.yaml`
only on that path. It selects or creates the environment before writing
project environment values.

`writeFoundryProvider` remains a narrow update. It writes
`infra.provider: microsoft.foundry` for a newly scaffolded workspace and
removes only the starter `infra.path` created by that same scaffold. For an
existing workspace:

- An existing `microsoft.foundry` provider is unchanged.
- An empty provider with no `infra.path` and no owned infrastructure files may
  be set to `microsoft.foundry`.
- An empty provider with `infra.path` or existing infrastructure files is
  treated as user-owned infrastructure and returns a conflict.
- When project resource generation is required, any other provider returns a
  conflict.
- Endpoint-only adoption that needs no resource reconciliation leaves the
  provider unchanged.

The helper does not overwrite a user-owned path, module settings, hooks,
metadata, or service fields. It receives whether `scaffoldProject` created the
workspace in this invocation so it never infers ownership from a path name
alone.

Move `parseInfraProvider`, `ejectInfra`, and their Bicep and Terraform helpers
from `cli/azd/extensions/azure.ai.agents/internal/cmd/init_infra.go`. Preserve
the current path safety, existing-file conflict, generated-file ownership, and
provider stamping rules.

### 2.7 Project mode resolution and Azure lookup

The project action resolves intent in this order:

| Priority | Input | Result |
|---|---|---|
| 1 | Explicit `--project-id` or delegated `project.resourceId` | `existing-id` |
| 2 | Explicit `--project-endpoint` or delegated `project.endpoint` | `existing-endpoint` |
| 3 | Existing service endpoint and active `AZURE_AI_PROJECT_ID` | Reuse `existing-id` when they match; otherwise apply the mismatch recovery rule |
| 4 | Active environment `AZURE_AI_PROJECT_ID` only | Reuse `existing-id` after lookup and reconcile the service endpoint |
| 5 | Existing single project service endpoint only | Reuse `existing-endpoint` after validation |
| 6 | Interactive choice | Prompt for new or existing |
| 7 | `--no-prompt` with no existing identity | `new` |

Before accepting a project, apply delegated
`requirements.allowedLocations`. Filter interactive existing-project and
new-location choices before prompting. Validate an explicit project ID after
ARM lookup and reject it when its location is outside the allowed set.

An invalid explicit value is a hard error. The action never falls back to a
lower-priority value after explicit input fails validation. When inferred
service and environment identities differ, neither one silently wins:

- Interactive mode displays both identities. The user may update the service
  to the environment project or keep the service endpoint and clear the stale
  ARM identity into endpoint-only mode.
- `--no-prompt` returns `project_target_mismatch` and names the explicit
  `--project-id` and `--project-endpoint` recovery forms.
- An explicit target remains highest priority and, after normal replacement
  confirmation rules, reconciles both stores to that target.

Move the project resource ID parser and generic project picker from
`cli/azd/extensions/azure.ai.agents/internal/cmd/init_foundry_resources_helpers.go`
to the projects extension. The parser must continue to accept resource group
and provider casing differences while validating the exact
`Microsoft.CognitiveServices/accounts/<account>/projects/<project>` shape.

Project lookup returns one normalized structure:

```go
type resolvedProject struct {
    Mode              projectMode
    ResourceId        string
    SubscriptionId    string
    UserTenantId      string
    ResourceGroupName string
    AccountName       string
    ProjectName       string
    Location          string
    Endpoint          string
    OpenAIEndpoint    string
}
```

`UserTenantId` is always the tenant used to create credentials. It is never
populated from `Subscription.TenantId` after `PromptSubscription()`.

Endpoint-only mode parses the account and project names only for display and
environment compatibility. Parsed names are not treated as a verified ARM
identity.

### 2.8 Environment state reconciliation

The projects extension becomes the sole writer for project identity values.
Agent-specific values remain with the agents extension.

| Key | Project mode owner | Rule |
|---|---|---|
| `AZURE_SUBSCRIPTION_ID` | `new`, `existing-id` | Set when context is resolved; retain in endpoint-only mode because it is shared azd context |
| `AZURE_TENANT_ID` | `new`, `existing-id` | Set to the user access tenant when context is resolved; retain in endpoint-only mode because it is shared azd context |
| `AZURE_LOCATION` | `new`, `existing-id` | Set when new-project context is resolved; in existing-ID mode seed from the project only when the value is absent |
| `AZURE_AI_PROJECT_ID` | `existing-id` | Set to the canonical ARM ID; unset in other modes |
| `AZURE_RESOURCE_GROUP` | `new`, `existing-id` | Set from resolved Azure context; clear stale adopted values on a mode switch |
| `AZURE_AI_ACCOUNT_NAME` | `existing-id` | Set from ARM lookup; unset in other modes |
| `AZURE_AI_PROJECT_NAME` | All modes | Set when known; unset when switching to `new` before provisioning |
| `FOUNDRY_PROJECT_ENDPOINT` | Existing modes | Set to the normalized endpoint; unset for a new project that is not yet provisioned |
| `AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT` | Existing modes | Mirror the normalized project endpoint; unset for a new project that is not yet provisioned |
| `AZURE_OPENAI_ENDPOINT` | `existing-id` | Set from the verified account; unset in other modes |
| `AZURE_AI_DEPLOYMENTS_LOCATION` | `new`, `existing-id` | Set from the selected or verified project location; unset in endpoint-only mode |
| `USE_EXISTING_AI_PROJECT` | All modes | Set to `true` for existing modes and `false` for new mode |
| `AZURE_AI_MODEL_DEPLOYMENT_NAME` | Project init, deployment add, and agent external-model path | Unset when project identity changes; set for the first resolved model only |

The following values stay outside project init's identity reconciler. Their
current agent or provisioning paths continue to own them:

- `AZURE_CONTAINER_REGISTRY_NAME`
- `AZURE_CONTAINER_REGISTRY_ENDPOINT`
- `APPLICATIONINSIGHTS_CONNECTION_STRING`
- `AZD_AGENT_DEPLOYMENT_MODE`
- `AZD_AGENT_PROTOCOL`
- `AZD_AGENT_RUNTIME`

Mode transitions use this matrix:

| From | To | Required cleanup |
|---|---|---|
| `new` | `existing-id` | Replace generated context with verified project ID, names, endpoints, and location |
| `new` | `existing-endpoint` | Remove the project resource group and deployment location; retain shared subscription, tenant, and Azure location values |
| `existing-id` | `new` | Remove project ID, account name, project name, all project endpoint aliases, and adopted resource group before selecting new context |
| `existing-id` | `existing-endpoint` | Remove project ID, account name, OpenAI endpoint, adopted resource group, and deployment location |
| `existing-endpoint` | `new` | Remove endpoint and parsed project name, then select new Azure context |
| `existing-endpoint` | `existing-id` | Replace parsed values with verified ARM values |

Cleanup is based on the old service endpoint and the presence of
`AZURE_AI_PROJECT_ID`, not on a new hidden mode variable. Only the
project-owned keys listed in the table are candidates for cleanup. Other
environment values are preserved. Every transition to a different normalized
project identity also clears `AZURE_AI_MODEL_DEPLOYMENT_NAME`; a no-op init
keeps it.

`AI_PROJECT_DEPLOYMENTS` remains the lifecycle projection produced by
`cli/azd/extensions/azure.ai.projects/internal/cmd/project_service_config.go`.
It is not directly persisted by `project init`.

Project init writes only the active project and environment. It does not call
`setProjectContext` or modify `extensions.ai-projects.context` in user config.
`azd ai project set` remains the explicit command for changing that global
default.

### 2.9 Project service discovery and naming

Service discovery must share the host constants already defined in
`cli/azd/extensions/azure.ai.projects/internal/provisioning/provisioning_provider.go`:

- `FoundryProjectHost`
- `FoundryProjectServiceHosts`
- `FoundryLegacyProvisioningHosts`
- `FoundryProvisioningServiceHosts`

Do not introduce a second list in the command package. Move the constants to a
small internal contract package only if command implementation creates an
import cycle. The current `internal/cmd/root.go` already imports
`internal/provisioning`, so the expected implementation reuses the exported
provisioning constants directly.

Before mutation:

1. Load the project through `Project.Get`.
2. Find services whose host is in `FoundryProjectServiceHosts`.
3. Return the existing key when exactly one exists.
4. Continue to legacy discovery when none exists.
5. Return a structured ambiguity error when more than one exists.

For a new service, choose the key in this order:

1. The existing project service key, when one was found.
2. The sanitized Foundry project name when it is non-empty and unused.
3. `ai-project` when unused.
4. The first unused `ai-project-<n>`, starting at 2.

Sanitization uses the same service-name rules as the core project service. A
key collision is checked against all services, not only Foundry hosts.

`Project.AddService` in
`cli/azd/internal/grpcserver/project_service.go` replaces the full existing
service object. The projects extension may call it only when the chosen key
does not exist. Every update to an existing service uses
`SetServiceConfigValue` or `UnsetServiceConfig`.

### 2.10 Narrow project service reconciliation

The reconciler keeps persisted and semantic views separate:

1. Call `Project.Get` for the project path and its environment-expanded service
   view.
2. Call `Project.GetConfigSection` with path `services` for the persisted
   service map before environment substitution.
3. Index the returned map by the exact service key. Do not construct a dotted
   path from the key, because valid service names may contain `.`.
4. Deep-clone the service maps before passing them to
   `foundry.ResolveFileRefs`.

The persisted view determines reference safety and supplies every value used
in a mutation payload. The resolved view is used only for discovery, equality,
and validation. Values returned by `Project.Get` must never be written back to
`azure.yaml`, because doing so could replace `${VAR}` templates with their
current value or an empty string.

For `project init`, the desired field set is limited to:

```text
services.<project-key>.endpoint
```

For `project deployment add`, the desired field set is limited to:

```text
services.<project-key>.deployments
```

The reconciler does not reconstruct a typed service and does not call
`SetServiceConfigSection` with a partial map. It carries the current raw
service values through each narrow mutation, preserving unknown schema fields,
hooks, `uses`, `network`, and values added by newer extension versions. Comment
preservation remains whatever `ProjectService` currently provides and is not a
new guarantee of this feature.

Both `SetServiceConfigValue` and `UnsetServiceConfig` must treat the service
name as an exact map key. The core correction in section 2.4 is therefore a
prerequisite for mode transitions on existing services whose keys contain `.`.

#### Service-level references

`foundry.ResolveFileRefs` in `cli/azd/pkg/foundry/includes.go` applies a shallow
overlay. Writing an inline `deployments` array over a service-level `$ref`
would hide the entire referenced array.

When the raw service has `$ref`:

- Resolve it for discovery and equality checks.
- Succeed when the requested operation is a no-op.
- Reject any endpoint or deployment mutation.
- Return the reference path and suggest editing that file or inlining the
  service.

The initial implementation does not use `foundry.YAMLDocument` with the
`foundry.EditRefFile` target from
`cli/azd/pkg/foundry/includes_edit.go`. That would create a second direct file
write path outside `ProjectService` locking and cache invalidation.

#### Deployment item references

For each deployment item:

1. Preserve the raw item and its index.
2. Resolve an item-level `$ref` for semantic comparison.
3. Normalize the deployment name for case-insensitive lookup.
4. Reject duplicate names already present in the resolved configuration.
5. Treat a semantically identical request as `unchanged`.
6. Reject a conflicting referenced item, including when `--force` is set.
7. Replace a conflicting inline item only when `--force` is set.
8. Append a new inline item after all existing items.

Semantic equality includes name, model format, model name, model version, SKU
name, and capacity. It ignores YAML key order. Unknown fields on the existing
item are outside the command's managed shape. When all managed fields match,
the command returns `unchanged` and preserves those unknown fields.
Replacement requires explicit `--force` and preserves no unknown fields, so
the confirmation names those fields before proceeding.

#### Legacy service migration

When there is no project service, inspect hosts in
`FoundryLegacyProvisioningHosts`. A migration is eligible only when exactly one
legacy service has at least one raw `endpoint`, `deployments`, or `network`
field and has no service-level `$ref`.

Create the new project service with:

- `host: azure.ai.project`
- The raw `endpoint`, when present
- The raw `deployments` array, including item-level references
- The raw `network` object

Do not remove or edit the legacy service. Do not copy `project`, `language`,
`docker`, hooks, `uses`, or unknown agent fields. If multiple eligible legacy
services exist, return an ambiguity error. If a single legacy service uses a
service-level `$ref`, leave it in the provider compatibility path and return
`project_service_ref_update_unsupported` without changing the workspace.

### 2.11 Managed deployment selection

Move project and model logic out of these agent files:

- `cli/azd/extensions/azure.ai.agents/internal/cmd/init_models.go`
- `cli/azd/extensions/azure.ai.agents/internal/cmd/init_foundry_resources_helpers.go`
- `cli/azd/extensions/azure.ai.agents/internal/cmd/init_infra.go`

The projects extension owns the parts currently represented by
`getModelDeploymentDetails`, the model catalog and quota helpers used by that
function, project location resolution, deployment name validation, and the
managed declaration write.

The agent extension keeps `ProcessModels` because it walks the agent manifest,
distinguishes managed from existing deployments, and injects the selected
deployment names into agent model configuration. Its model resolver becomes
an interface with two paths:

- Verify and return an existing Azure deployment.
- Delegate creation of a managed declaration and consume the result.

The selector applies filters in this order:

1. Resolve the project location or allowed new-project locations.
2. Load catalog models available in those locations.
3. Apply `requiredCapabilities`.
4. Apply `excludedModelNames`.
5. Join quota and usage only to the same location and model.
6. Remove choices with known insufficient quota.
7. Ask for model, version, SKU, and capacity only when unresolved.
8. Validate the final tuple again immediately before mutation.

Unknown or empty usage from another location must never make a model eligible.
Unknown usage in the selected location may remain selectable only when the
service API treats the SKU as having no enforceable quota check. Otherwise it
produces a quota-unavailable error.

Direct `project deployment add` uses no hidden preferred model. Agent requests
must pass `agentsV2`, preserving the current `agentModelFilter` behavior.

The resolved deployment is converted to the existing
`synthesis.Deployment` shape used by
`cli/azd/extensions/azure.ai.projects/internal/synthesis/synthesizer.go`.
No second deployment schema is introduced.

### 2.12 Agent initialization changes

`configureModelChoice` in
`cli/azd/extensions/azure.ai.agents/internal/cmd/init.go` is split into
agent-owned manifest work and project workflow calls.

The new order is:

1. Resolve or download agent source.
2. Read and validate the agent manifest.
3. Resolve deploy mode and prebuilt-image input, then compute any project
   location restriction required by that agent mode.
4. Invoke delegated `project init` with the location restriction.
5. For each model, verify an existing deployment or invoke delegated
   `project deployment add`.
6. Inject final deployment names into the manifest or generated agent config.
7. Complete agent-specific registry, protocol, runtime, and account
   network work.
8. Author the agent service.
9. Merge the returned project service key into agent `uses`.

The projects extension does not accept an agent manifest or write agent source.
The agent extension does not pass a prebuilt project service map.

Retain `persistFirstDeploymentName` only for the case where the first resolved
model is an existing external deployment. Managed deployments rely on
`setAsDefault` in the projects request, so agents must not write the same value
again.

Split `configureFoundryProjectEnv` in
`cli/azd/extensions/azure.ai.agents/internal/cmd/init_foundry_resources_helpers.go`
instead of moving it wholesale. The projects extension takes the project
identity writes in section 2.8. The agents extension retains
`configureExistingProjectAcr`, ACR and Application Insights connection
selection, and `foundryAccountNetworkInjected`.

After project init succeeds, agents reads the verified project identity from
the active environment and performs those agent-specific lookups. This
preserves the current behavior that disables remote build for a
network-injected existing account without adding agent state to the project
result contract. A new project that has not been provisioned has no existing
account network to inspect.

Code deploy and `--image` currently restrict project selection to hosted-agent
regions. Agents keeps the hosted-agent region lookup and passes those locations
through `requirements.allowedLocations`; projects owns applying the constraint
to project selection. This preserves the existing explicit-ID and interactive
eligibility checks without moving agent deployment policy into projects.

Existing compatibility flags map directly to delegated request fields:

| Agent flag | Delegated behavior |
|---|---|
| `--project-id` | Project init `project.resourceId` |
| `--infra[=<provider>]` | Project init `infra.ejectProvider` |
| `--model` | Deployment add `model.name` for a managed deployment |
| `--force` | Both requests' `force` field |
| `--model-deployment` | No project request; validate and reference externally |

Deprecation notices use stderr and are suppressed from structured stdout. Flag
removal is outside this feature and requires a separately announced
compatibility change.

In `cli/azd/extensions/azure.ai.agents/internal/cmd/resource_services.go`:

- Remove project service creation and project endpoint mutation from
  `emitResourceServices`.
- Remove managed deployment creation and the legacy deployment reader.
- Keep connection and toolbox handling until those sibling owners define
  separate contracts.
- Change `setServiceUses` from replacement to ordered merge.
- Never call `AddService` for a key returned by project init.

In `cli/azd/extensions/azure.ai.agents/internal/cmd/init_adopt.go`:

- Remove `foundryDeployments` and `verifyAzureYamlDeployments`.
- Stop interpreting project endpoint and deployment fields.
- Discover one `azure.ai.project` host as an opaque dependency.
- Preserve the existing ambiguity error when more than one host is present.

The `azure.ai.agents/internal/synthesis` copy is removed after the parity tests
prove that the projects synthesizer accepts every supported pre-split input.
The agent extension invokes the host infrastructure workflow instead of
calling project synthesis directly.

### 2.13 Provisioning safety

The projects provisioning provider already chooses brownfield behavior from
`AZURE_AI_PROJECT_ID` in
`cli/azd/extensions/azure.ai.projects/internal/provisioning/foundry_provisioning_provider.go`.
The ownership change adds a target consistency check before synthesis or
deployment:

1. Parse the service endpoint into account and project names when present.
2. Parse `AZURE_AI_PROJECT_ID` with the shared resource ID parser when present.
3. If managed deployments or other project resources require brownfield
   targeting, require a project ID.
4. When both values exist, compare account and project names
   case-insensitively.
5. Fail with a reconciliation error when they differ.

This prevents a partial environment transition from provisioning against the
old ARM project while `azure.yaml` names a new endpoint.

Endpoint-only mode remains valid for commands that use only the data-plane
endpoint. It is not silently upgraded to a resource-managing mode.

The provider continues to accept `FoundryLegacyProvisioningHosts` during the
rollout. `findFoundryProjectService` uses the shared host contract and keeps
its current single-service requirement.

### 2.14 Infrastructure ownership and rollout

The change must ship in compatibility stages. Publishing only the new projects
command before changing the agents writer would leave an older
`emitResourceServices` path able to replace the new project service and remove
`network`, hooks, `uses`, references, or unknown fields.

#### Stage A: safe legacy writers

Release `azure.ai.agents` with no public behavior change, but change its
existing project authoring path to:

- Discover by host instead of assuming a key.
- Use narrow config mutations.
- Merge deployments by case-insensitive name.
- Merge `uses` without replacing unrelated entries.
- Preserve references and unknown project fields.

This version remains compatible with the current projects extension and core
azd.

#### Stage B: coordinated ownership transition

Release together:

- Core azd with structured workflow errors and `Environment.UnsetValue`.
- `azure.ai.projects` with project init, deployment add, environment
  reconciliation, and delegated contract version 1.
- `azure.ai.agents` with delegated project operations and a direct dependency
  on the compatible `azure.ai.projects` prerelease.
- `microsoft.foundry` with dependency versions that select the compatible
  projects and agents extensions.

Update `cli/azd/extensions/azure.ai.agents/extension.yaml` so its
`azure.ai.projects` dependency selects the first version that implements
delegated contract version 1. The existing extension dependency resolver is
the primary compatibility gate: normal install and upgrade operations reject
or reconcile an installed projects version outside that constraint. Update the
meta-package constraints to select the same pair.

No new runtime extension-capability API is required. Request
`schemaVersion` and `sourceVersion` continue to detect contract skew after the
command is available. Users who explicitly install or upgrade with
`--no-dependencies` bypass the dependency guarantee and may receive the
structured compatibility or missing-command error from delegation.

Raise each extension's minimum azd version from the current `>=1.27.1` to the
first core version containing both required APIs. Validate the minimum in
extension manifests and registry metadata.

As of the source design, the relevant package versions are
`azure.ai.projects` `1.0.0-beta.5`, `azure.ai.agents` `1.0.0-beta.9`, and
`microsoft.foundry` `1.0.0-beta.2`. The implementation uses the next
coordinated prerelease versions rather than hard-coding a partial combination.

#### Stage C: duplicate removal

After the coordinated versions are available and telemetry shows no
compatibility regression:

- Remove project selection and persistence code from agents.
- Remove the agents synthesis copy.
- Remove agents fallback project authoring.
- Announce the version in which legacy service-host provisioning fallback will
  be removed.

The projects command keeps delegated schema version 1 readers for at least one
coordinated extension release cycle after agents stops sending an older shape.

Stage C is a separate follow-up change, not part of the initial Stage B
implementation assignment. The release owner may approve it only after:

- At least one complete coordinated prerelease cycle has shipped.
- The exact-version compatibility matrix in section 2.18 is green.
- No confirmed regression remains open for a supported install, upgrade,
  agent-init, project-init, or provision path.
- The release issue records the telemetry comparison window and baseline used
  to evaluate delegated compatibility errors and command failure rates.

If any condition is not met, the compatibility readers, legacy host support,
and agents fallback code remain in place.

### 2.15 Errors

Both extensions use `cli/azd/pkg/azdext/extension_error.go` and their existing
`internal/exterrors` constructors. Add specific stable codes only where a
caller or support workflow needs to distinguish remediation:

| Code | Category | Condition | Suggestion |
|---|---|---|---|
| `invalid_project_init_request` | validation | Conflicting or malformed project input | Correct the named flag or request field |
| `unsupported_delegated_schema` | compatibility | Unknown request or result version | Upgrade the named extension |
| `delegated_contract_io_failed` | internal | Request or atomic result file operation fails | Retry and verify access to the system temporary directory |
| `project_service_ambiguous` | validation | More than one project service or migration source | Keep one project service |
| `project_switch_confirmation_required` | validation | Non-interactive implicit project replacement | Pass the explicit replacement ID or endpoint |
| `project_service_ref_update_unsupported` | validation | A service-level `$ref` would be mutated | Edit the referenced file or inline the service |
| `deployment_name_conflict` | validation | Same deployment name with different settings | Choose another name or use `--force` for inline content |
| `deployment_ref_conflict` | validation | Conflicting item is referenced | Edit the referenced deployment file |
| `project_location_unsupported` | validation | Selected project does not meet a delegated location restriction | Choose a project or new-project location supported by the calling experience |
| `managed_deployment_requires_project_id` | dependency | Endpoint-only project requests managed resources | Reinitialize with `--project-id` |
| `project_reconciliation_requires_project_id` | dependency | Endpoint-only init would move managed project declarations to another endpoint | Reinitialize with `--project-id` before changing project identity |
| `project_target_mismatch` | validation | Endpoint and ARM ID name different projects in non-interactive mode | Pass the project to keep with `--project-id` or `--project-endpoint` |
| `unsupported_infra_provider` | validation | Requested eject format is unknown | Pass `--infra=bicep` or `--infra=terraform` |
| `infra_provider_conflict` | validation | Project setup would replace a user-owned provider | Remove `--infra`, use a compatible workspace, or wait for multi-provider support |

Azure API failures retain status and request identifiers in diagnostic details,
but user-facing messages do not expose tokens or response bodies. A child
error crossing `WorkflowService.Run` retains its code, category, message,
suggestion, and links through the gRPC detail described in section 2.4.

### 2.16 Telemetry

Record only low-cardinality fields:

| Field | Values |
|---|---|
| `ai.project.operation` | `init`, `deployment.add` |
| `ai.project.source` | `direct`, `agent.init` |
| `ai.project.mode` | `new`, `existing.id`, `existing.endpoint` |
| `ai.project.mutation` | `created`, `updated`, `migrated`, `unchanged` |
| `ai.project.deployment.action` | `created`, `replaced`, `unchanged`, `external` |
| `ai.project.infra.mode` | `extension`, `bicep`, `terraform`, `existing`, `unsupported` |

Do not record subscription IDs, tenant IDs, resource groups, account names,
project names, endpoints, resource IDs, deployment names, file paths, or model
names.

Each new field is classified as `SystemMetadata` for `FeatureInsight`. Update:

- `cli/azd/internal/tracing/fields/fields.go`
- `docs/reference/telemetry-data.md`
- The metrics audit schema and matrix
- The telemetry privacy checklist
- Telemetry field and redaction tests

Fixed enums do not require hashing. Errors record the stable error code, not
the resource value embedded in a message.

### 2.17 Documentation updates

Update the projects extension README and command help with:

- The distinction between project init and deployment add.
- Resource-ID and endpoint-only capabilities.
- Non-interactive requirements.
- Managed versus external deployments.
- The service-level `$ref` mutation limitation.
- The intentional `project deployment add` command hierarchy.

Update `cli/azd/docs/environment-variables.md` to identify
`azure.ai.projects` as the writer for the project values in section 2.8.
Keep `docs/reference/environment-variables.md` synchronized where it lists the
same values. Update `docs/architecture/extension-framework.md` only for the
generic structured workflow error and environment unset capabilities, not for
Foundry-specific policy.

Record the preview commands in `docs/reference/feature-status.md`. If delegated
output isolation becomes the recommended pattern for other extension
commands, add it to `docs/guides/adding-a-new-command.md`; otherwise keep that
detail in the projects extension documentation.

### 2.18 Test strategy

#### Core azd tests

Add targeted tests for:

- `WorkflowService.Run` preserving every `ExtensionError` field in status
  details.
- The client helper finding a detail through wrapped gRPC errors and returning
  `azdext.UnwrapError`.
- A legacy client receiving the existing gRPC code without understanding the
  detail.
- Workflow cancellation and deadline errors retaining their standard gRPC
  codes.
- `workflowCmdAdapter` forwarding explicit global options while a delegated
  child option shadows a same-named global option.
- Delegated projects and nested scaffold workflows using `--output=none`.
- One valid JSON document from a parent with `--output json`.
- `ProjectService.UnsetServiceConfig` removing a nested value from a service
  whose exact key contains `.` without changing a similarly named nested map.
- `Environment.UnsetValue` deleting an existing dotenv key.
- `Environment.UnsetValue` succeeding for a missing key.
- Environment save and validation failures propagating without a
  success-shaped response.

#### Projects extension unit tests

Add table-driven tests for:

- Public command registration, help, examples, and structured output.
- Direct flags taking precedence over service and environment defaults.
- Mutual exclusion and delegated schema validation before `scaffoldProject`.
- Request version, source version, unknown-field, and result producer version
  validation.
- Direct `--no-prompt` choosing new mode with no project or Azure context.
- Delegated agent init requiring subscription and location when
  `resolveAzureContext` is true.
- Delegated project location constraints filtering existing projects and new
  locations, and rejecting an ineligible explicit project ID.
- Direct deployment add requiring `--model` in `--no-prompt`.
- Bare `--infra` resolving to Bicep and explicit Terraform resolving only
  through the equals form.
- Bicep and Terraform ejection preserving the current generated-file and
  provider stamping behavior.
- Existing user-owned infrastructure rejecting an unsafe merge before writes.
- Resource-ID parsing across provider and resource-group casing.
- Credentials using `UserTenantId` for prompted subscriptions.
- Explicit subscriptions calling `LookupTenant()`.
- All six environment mode transitions and every set or unset in section 2.8.
- Default deployment cleanup when project identity changes and preservation on
  a no-op init.
- Environment mutation failure returning no delegated result or project config
  diff.
- Existing service reuse and deterministic new service naming.
- Mode-transition cleanup on a project service whose key contains `.`.
- Multiple project services failing before mutation.
- New service creation using `AddService` only for an absent key.
- Existing service updates preserving hooks, `uses`, `network`, unknown fields,
  and unrelated deployment items.
- Persisted service reads preserving `${VAR}` templates and indexing dotted
  service keys without treating them as config paths.
- Eligible legacy service migration and unchanged legacy content.
- Legacy ambiguity and service-level `$ref` migration refusal.
- Service-level `$ref` no-op and mutation refusal.
- Item-level `$ref` equality, conflict, and unrelated inline append.
- Case-insensitive deployment duplicate detection with spelling preservation.
- Inline replacement requiring `--force`.
- Model capability filtering with `agentsV2`.
- Cross-location quota data never enabling a model in another location.
- Deterministic version, SKU, and capacity resolution in `--no-prompt`.
- Endpoint-only managed deployment rejection.
- Endpoint-only project init rejecting a target change with deployment or
  network declarations while allowing a same-target no-op.
- Interactive project endpoint and ARM ID mismatch recovery in both
  directions.
- Non-interactive mismatch rejection without an explicit target and recovery
  with an explicit target.
- Default-deployment environment failure preserving the declaration and
  succeeding on retry.
- Atomic result writing and cleanup on success, error, and cancellation.
- Telemetry containing only approved enum values.

Retain and extend
`cli/azd/extensions/azure.ai.projects/internal/provisioning/contract_parity_test.go`
so shared host values stay aligned. Extend synthesis parity tests before
removing the agents copy.

#### Agents extension unit tests

Add tests for:

- Project init delegated before any model or agent service mutation.
- Deploy mode and image eligibility resolved before project init, with hosted
  agent locations forwarded only for restricted modes.
- One deployment request per managed manifest model.
- Every managed agent request carrying `agentsV2`.
- Existing external deployment validation without a deployment add request.
- External-first model selection persisting the default name and making every
  later managed request use `setAsDefault=false`.
- Existing account network injection still disabling agent remote build after
  project delegation.
- Existing project ACR and Application Insights selection still running before
  agent service authoring.
- The first resolved managed model setting `setAsDefault` and later models not
  setting it.
- Child result service keys used instead of assuming `ai-project`.
- Explicit delegated working directory and environment reaching project init.
- Project workflow failure preventing agent service authoring.
- Partial multi-model failure preserving prior project declarations and
  allowing retry.
- `setServiceUses` preserving hand-authored entries and appending the project
  key once.
- Adopt and scan treating the project service as opaque.
- No project endpoint or deployment parsing remaining in agent adoption.
- JSON output containing one document and no child document.
- Temporary delegated files containing no credentials and always being
  removed.

#### End-to-end tests

Exercise these scenarios against exact extension versions:

- New project init, managed deployment add, and provision.
- New project init with Bicep ejection.
- New project init with Terraform ejection.
- Existing project resource-ID adoption and managed deployment add.
- Endpoint-only adoption and expected managed-deployment rejection.
- One-command agent initialization with one managed model.
- Agent initialization against a project initialized in a previous command.
- Agent manifest with multiple managed models.
- Agent manifest referencing an existing external deployment.
- Pre-split legacy service migration without deleting legacy fields.
- Existing project service with custom hooks, `uses`, `network`, unknown
  fields, and deployment item references.
- Repeating every successful init path with no second diff.
- `--no-prompt --output json` producing one valid document.
- Guest-tenant subscription authentication through the user access tenant.
- A forced service mutation failure after environment reconciliation followed
  by a successful retry.
- Stage A agents with the prior projects version, then coordinated Stage B
  versions from `microsoft.foundry`.
- Direct agents installation and upgrade selecting a projects version that
  satisfies the delegated-contract dependency.

The authenticated compatibility owner is
`eng/pipelines/ext-azure-ai-agents-live.yml`. Extend that pipeline instead of
creating a second live-Azure pipeline. The current job installs locally built
agents and projects binaries by writing `0.0.0-test` entries directly into
`~/.azd/config.json`. That remains acceptable for the generic golden path, but
it bypasses dependency resolution and cannot satisfy the exact-version cases
above.

Add a compatibility matrix with these requirements:

1. Package candidate extensions with the repository's existing local-registry
   or bundle tooling so registry entries retain the versions and dependency
   constraints from `extension.yaml`.
2. Install through `azd extension install` without `--no-dependencies`; do not
   hand-author installed extension records for compatibility jobs.
3. Start each combination from a clean `AZD_CONFIG_DIR`.
4. Assert the installed IDs and versions through
   `azd extension list --installed --output json` before invoking a product
   command.
5. Pin every prior released artifact by exact version. Never use `latest` in
   this matrix.

The matrix contains:

| Combination | Version set | Required assertion |
|---|---|---|
| Stage A backward compatibility | Candidate Stage A agents plus the prior released projects version | Existing agent init and provisioning remain successful without a new projects command |
| Stage B meta-package | Candidate `microsoft.foundry` only | Normal dependency resolution installs the coordinated agents and projects versions, then the live golden path succeeds |
| Stage B direct agents | Candidate agents install and upgrade | The resolver selects a projects version satisfying delegated contract version 1 |
| Dependency bypass | Candidate agents with `--no-dependencies` | A missing or incompatible projects command produces the documented structured compatibility or missing-command error |

Add `E2E_BASE_AZD_CONFIG_DIR` to the agents live runner. When set, the runner
copies that seed instead of the ambient `~/.azd`; document it in the live E2E
README. Each matrix job installs its exact combination into a different seed.
The existing per-mode private config copy and cleanup behavior then remain
unchanged.

Dependency-resolution assertions that do not require Azure belong in core
functional tests using a temporary file registry. The live pipeline proves only
the command and provisioning combinations that require Azure. The release owner
records both results in the Stage B release issue and blocks publication when
either set fails.

### 2.19 Implementation and merge plan

This feature is delivered as independently reviewable PRs. Each PR includes the
tests, telemetry, help, and documentation required by the behavior it changes;
those are not deferred to a final cleanup PR.

| Order | Pull request | Scope | Prerequisites | Required owner or reviewer |
|---|---|---|---|---|
| 1 | Stage A safe writer | Make the current agents writer use host discovery, narrow mutations, case-insensitive deployment merge, ordered `uses` merge, and reference preservation without changing public behavior | None | Agents component owner |
| 2 | Core composition APIs | Add structured workflow error transport, child-flag precedence, `Environment.UnsetValue`, and exact-key `ProjectService.UnsetServiceConfig`, including generated SDK changes | None | Core azd maintainer |
| 3 | Projects init ownership | Add delegated contract plumbing, project init, project mode and environment reconciliation, narrow service migration, infrastructure ejection, and provisioning target consistency | Core composition APIs | Projects component owner |
| 4 | Projects deployment ownership | Add deployment add, move model and quota selection, enforce location and capability filters, and reconcile managed declarations | Projects init ownership | Projects component owner |
| 5 | Agent delegation | Delegate project init and managed deployments, retain external deployment handling, isolate JSON output, and stop Stage B project authoring in agents | Projects init and deployment ownership; Stage A version released | Agents component owner |
| 6 | Stage B compatibility release | Set exact manifest and registry constraints, raise minimum azd versions, run the compatibility matrix, and publish the coordinated meta-package set | Core and extension behavior PRs merged; PM decisions in Part 3 recorded | Release owner |
| 7 | Stage C cleanup | Remove duplicate agents selection, persistence, synthesis, and fallback code | Stage C gates in section 2.14 satisfied | Agents and projects component owners plus release owner |

PRs 2 through 4 may be developed concurrently after their shared interfaces are
agreed, but they merge in dependency order. PR 5 must not add a temporary
fallback that bypasses the delegated contract. PR 6 owns release metadata and
go/no-go evidence; an implementation contributor must not infer compatible
versions or approve Stage C from source state alone.

## Part 3: Dependencies that need PM confirmation

1. **Infrastructure provider composition.** The command can eject Bicep and
   Terraform, but `azure.yaml` still selects one infrastructure provider. PM
   must confirm that a workspace with an existing non-Foundry provider fails
   rather than attempting an unsafe merge. Multi-provider composition remains
   separate work.
2. **Brownfield generated infrastructure.** Issue
   [#9127](https://github.com/Azure/azure-dev/issues/9127) and
   [PR #9348](https://github.com/Azure/azure-dev/pull/9348) affect how adopted
   project resources appear after infrastructure ejection. PM must confirm
   whether the ownership transition waits for those experiences or ships with
   the current brownfield limitations documented.
3. **Shared-resource teardown.** Issue
   [#6215](https://github.com/Azure/azure-dev/issues/6215) tracks broader
   existing-resource lifecycle behavior. PM must confirm that this feature
   preserves the current `USE_EXISTING_AI_PROJECT` teardown boundary rather
   than expanding shared-resource ownership.
4. **Sibling service ownership.** Connections and toolboxes remain in the
   agents path during this change. PM must confirm whether follow-up ownership
   designs belong to the projects extension or separate extensions before the
   Stage C cleanup removes all compatibility code.
5. **Coordinated release train.** PM and release owners must confirm that core
   azd, `azure.ai.projects`, `azure.ai.agents`, and `microsoft.foundry` can ship
   as one compatible set. If not, Stage B must remain disabled until the
   meta-package can prevent an unsafe version combination. The release owner
   records the selected versions, compatibility-matrix runs, and Stage B
   go/no-go decision in the release issue described in section 2.19.

## Part 4: New open questions

No unresolved technical questions remain for the first implementation. The
scope decisions that can change product behavior or release timing are listed
in Part 3 and require PM confirmation before Stage B ships.

## Summary of required changes

### Core azd

- Add a host-to-extension workflow error detail around `ExtensionError`.
- Preserve and unwrap extension errors across `WorkflowService.Run`.
- Make explicit workflow step flags override inherited global flags.
- Add `Environment.UnsetValue` and generated client support.
- Make `ProjectService.UnsetServiceConfig` exact-key safe for dotted service
  names.
- Test delegated stdout ownership, cancellation, error compatibility, and
  dotenv deletion.
- Raise the core API version consumed by coordinated extension releases.

### `azure.ai.projects`

- Register `project init` and `project deployment add` in `internal/cmd/root.go`.
- Add public flags, JSON output, and hidden request and result file flags.
- Implement delegated schema version 1 validation and atomic result writes.
- Move project scaffolding, selection, ARM lookup, tenant, location, and
  project environment logic from agents.
- Move Bicep and Terraform infrastructure ejection from agents.
- Implement deterministic project service discovery and key selection.
- Implement narrow endpoint and deployment reconciliation through
  `ProjectService`.
- Read persisted services through `Project.GetConfigSection("services")` and
  keep expanded values out of mutation payloads.
- Implement legacy service migration without deleting legacy fields.
- Enforce service-level and item-level `$ref` rules.
- Apply delegated project location restrictions to project lookup and
  selection.
- Move managed model catalog, capability, quota, SKU, capacity, and deployment
  selection from agents.
- Preserve `agentsV2` filtering for delegated agent requests.
- Add project target consistency checks to the provisioning provider.
- Consolidate shared host constants and synthesis parity.
- Add structured errors and low-cardinality telemetry.
- Update command, feature status, environment, telemetry, and extension
  framework docs.
- Add the unit and end-to-end coverage in section 2.18.

### `azure.ai.agents`

- First release the Stage A narrow project writer and ordered `uses` merge.
- Resolve agent project-location requirements before project delegation.
- Replace project initialization with the delegated project init workflow.
- Replace managed declaration authoring with delegated deployment add calls.
- Keep existing external deployment validation in `ProcessModels`.
- Remove project parsing from adopt and scan.
- Author the agent service only after all project operations succeed.
- Make the agent command the sole JSON producer.
- Remove duplicate project selection, persistence, infrastructure, and
  synthesis code after the coordinated transition.
- Pin the direct projects dependency to delegated contract version 1.
- Add delegation, retry, multi-model, adoption, and output isolation tests.

### `microsoft.foundry` and release metadata

- Pin compatible projects and agents extension prereleases.
- Raise minimum azd versions after the core APIs ship.
- Prevent partial Stage B extension combinations.
- Document the legacy host fallback removal window.
