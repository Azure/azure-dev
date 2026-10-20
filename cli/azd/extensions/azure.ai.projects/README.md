# Foundry Projects

Manage Microsoft Foundry Project resources from your terminal. (Preview)

## Extension telemetry API

Extension code can report best-effort usage events through the shared
`pkg/foundry/telemetry` package:

```go
reporter := telemetry.NewReporter(azdClient.Telemetry(), nil)
reporter.Report(ctx, telemetry.Event{
  Name: "project.operation.completed",
  Attributes: map[string]string{
    "operation": "provision",
  },
})
```

The example is illustrative; this extension does not currently emit a product
usage event through this interface. Existing provisioning span attributes are
unchanged by this adapter. Add an event only after its product question, bounded
values, documentation, and privacy review are agreed. Before emitting, declare each
`ext.*` attribute in `cli/azd/extensions/telemetry/fields.go` — reuse an existing
declaration only when its meaning, allowed values, classification, and purpose are
identical, otherwise declare a distinct key — as enforced by
`go test ./extensions/telemetry`.

`Report` has no return value and never changes command or provider behavior. It
uses a one-second timeout, does not retry, and does not log attribute values or
transport error details. Put approved event builders and finite-value types in
`internal/telemetry/events.go`; do not call `ReportUsage` directly from command,
service-target, or provisioning code. Never include project endpoints, ARM IDs,
deployment names, resource names, paths, URLs, or other customer content. The azd
host records events only for extensions installed from the official registry.

## Non-interactive automation

See the shared [AI extension non-interactive input reference](../ai-non-interactive.md)
for every prompt's flag, environment/configuration input, or deterministic
no-prompt behavior.

## `azure.yaml` ownership

This extension owns `host: azure.ai.project` services and the `microsoft.foundry` provisioning provider. A project service carries account-level settings such as an existing project endpoint, model deployments, and private networking.

```yaml
infra:
  provider: microsoft.foundry

services:
  my-project:
    host: azure.ai.project
    endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          name: GlobalStandard
          capacity: 50
```

When `endpoint` is omitted, `azd provision` creates a Foundry account and project. When it is set, provisioning reuses that project and reconciles the declarations that can be applied to an existing account.

### Deployment preview

`azd deploy --preview` performs no work for an `azure.ai.project` service, just
like its normal package/publish/deploy lifecycle. The provider returns no readable
message and does not emit an unsupported-preview warning. It does not read Azure
resources, initialize deployment state, or perform provisioning.

The host still displays its own per-service progress line, for example
`Done: Previewing service ai-project`. The provider message is empty and omitted
from host JSON; `data` contains `status: "noOp"`, `scope: "deployment"`, and
`infrastructurePreviewed: false`. This is not a comparison of project resources,
model deployments, networking, or RBAC; those remain provisioning-owned.

### Developer access

The built-in provisioning template does not assign a role to the developer. Before deploying a prompt, hosted, or voice agent, the agent extension checks project access and attempts to grant Foundry User when needed. If that grant is denied, it warns and deployment continues; an administrator may need to assign the role.

User-authored on-disk Bicep templates can still declare their own role assignments. Supply any required `principalId` and `principalType` values in the template's parameter file; the provider does not generate them.

### Infrastructure layers

For projects that use `infra.layers`, declare exactly one layer with
`provider: microsoft.foundry` and leave the root provider available for the
other layers:

```yaml
infra:
  provider: bicep
  layers:
    - name: foundry
      path: infra/foundry
      provider: microsoft.foundry
```

When `azd ai project add --infra` is used with layers, infrastructure is
ejected into the Foundry layer's configured path and module. The root
`microsoft.foundry` provider cannot be combined with named layers.

When `project add` migrates a legacy Foundry service, retired
`network.mode`, `network.byo`, and `network.managed` fields are rejected
instead of being copied into the new project service. Rewrite the block using
the current `peSubnet` schema (and `agentSubnet` or `isolationMode` as needed)
before retrying.

## Project authoring

Add or adopt a Foundry project in the current azd workspace. If `azure.yaml`
is missing, the command first creates a minimal azd project:

```sh
azd ai project add
azd ai project deployment add --model <model-name>
azd provision
```

`project deployment add` creates the azd workspace and Foundry project
configuration when they are missing, then adds the deployment. In automation,
provide the project identity and Azure environment values required by
`project add`; incomplete non-interactive input fails before the deployment is
changed.

Managed deployment resolution requires one Azure location. Pass `--location`,
set `AZURE_AI_DEPLOYMENTS_LOCATION`, or set `AZURE_LOCATION`. Interactive runs
prompt for a location when none is configured; `--no-prompt` returns actionable
guidance instead of searching all subscription regions.

Use `--force` with an explicit `--project-id` or `--project-endpoint` when
replacing a different configured project. The command rejects `--force`
without an explicit target instead of silently ignoring the flag.

Use `--new-project` when a new project must be created even if the workspace
already has a project endpoint or project ID configured:

```sh
azd ai project add --new-project
```

`--new-project` cannot be combined with `--project-id` or
`--project-endpoint`.

To use an existing project in automation, initialize it with its full ARM
resource ID. This stores the project identity in the active azd environment
and allows managed deployment declarations to be reconciled:

```sh
azd ai project add --project-id "<project-resource-id>"
azd ai project deployment add --model <model-name>
```

An endpoint-only project is suitable for configuration that does not manage
resources on the existing project. If managed deployments are already
declared, endpoint-only setup stops before clearing the project ID.
Use the full project resource ID before adding managed deployments.

### Host agents on a customer-owned AKS cluster

For a newly created Foundry account, the experimental `agentHosting` input on the
`azure.ai.project` service identifies the customer-owned cluster:

```yaml
infra:
  provider: microsoft.foundry

services:
  my-project:
    host: azure.ai.project
    agentHosting:
      clusterResourceId: ${AZURE_AKS_CLUSTER_ID}
```

`hostingType` defaults to `ManagedCluster` and `name` to `aks`. Optional
`storageAccountResourceId` and `agentSubnetResourceId` select existing resources.
Select an existing container registry through `AZURE_CONTAINER_REGISTRY_RESOURCE_ID`.
Explicitly empty, null, or malformed resource selections are errors. The public
`hostingManagementIdentityResourceId` and `workloadIdentityResourceId` properties
are rejected, including empty or null values: preparation owns two distinct
user-assigned identities.

The workflow is `azd init`, `azd provision`, then `azd deploy`. Provision first
collects read-only resource and scoped authorization evidence. Mandatory `FAIL`
or `UNVERIFIED` results stop dependency creation and Foundry deployment. Preview
does not create identities, grants, or other resources. Referenced subscriptions
must have the same owning tenant; same-tenant cross-subscription references are
not rejected merely for using different subscriptions.

Permission checks include the selected standard-template model deployments,
project connections and target-scope nested deployments, the future project's
registry grant, and the existing optional developer Cognitive Services User
assignment with its actual recipient and principal type. A future project
principal remains unknown during conditional-delegation checks.

**This onboarding path is not production-ready.** An authoritative read-only
private-preview admission contract and supported source-bound network evidence
are not available in the production adapters. ARM readability, deployment
validation, role assignment readback, and local connectivity are not substitutes
for those gates. Provision therefore reports these missing contracts rather than
claiming a successful onboarding. Terraform eject is unavailable for this path
because it cannot run the required extension preparation lifecycle.

When no agent subnet is supplied, the bounded default topology plans an unused
private IPv4 `/27` in the qualifying agent pool's existing VNet, in the target
region. Its address inventory includes current subnets, AKS Pod/Service ranges,
and known connected peering ranges. Gateway-transit and route-server topologies
are not supported by this resolver. Preparation creates only that journaled
subnet with the `Microsoft.App/environments` delegation; it does not rewrite
existing subnets, routes, NSGs, peerings, AKS settings, or network policy.
New paths still need source-bound verification after creation. Broader selected
private-network mutations require administrator preparation.

After all preflight gates pass, preparation creates or resumes only its journaled,
owner-tagged dependencies and exact scoped grants. Missing Storage and registry
resources are created with safe defaults; supplied resources are not rewritten.
Actual Storage, companion-subnet, and registry paths require post-creation network
evidence before deployment. Registry checks cover source-bound DNS, TCP 443, and
TLS to its login server and all enabled dedicated data endpoints; this does not
establish kubelet image-pull authorization or data-plane role propagation.
Storage does not need to be dedicated to this workflow. Preserve the environment
value `AZD_FOUNDRY_BYO_AKS_PREPARATION`: it records owned resource IDs and grant
intent/results for interrupted-operation recovery, not credentials or proof of
data-plane propagation. Final template binding preserves existing resource-group
tags across retries while honoring explicit customer tag changes. Conflicting
private ownership tags, including case-insensitive aliases, are rejected.
Provision uses two internal ARM stages: create the owned account/project, prepare
its actual managed identity's journaled registry grant, then configure its
registry connection. This adds no user command. The private account target,
deployment attempts and ownership markers are persisted for recovery.
BYO on-disk Bicep must retain the standard deployment resource bindings;
supported selections belong in configuration or parameter inputs. Altered
templates are rejected before preparation. The actual ARM template hash and
target parameters must match the persisted attempt before cleanup; a planned
account's absence alone is not sufficient evidence. Explicit resource-group and
location parameters must agree with the environment's resolved target.
Foundry failures retain nested deployment-operation errors and correlation IDs;
unavailable diagnostic reads are reported separately from the original failure.

`azd down` uses the persisted ownership journal even if the current hosting
configuration changes. It never deletes the resource group. It first verifies
terminal deployment evidence, inventories projects and model deployments, deletes
only tracked models, and confirms deletion of the owned account. Service-owned
federated credentials must disappear before dependency grants are removed; azd
does not delete those credentials itself. Cleanup then deletes only proven-owned
assignments, identities, Storage, registry and prepared subnet IDs. Supplied
resources, reused customer grants and later-added resource-group resources are
preserved. Unknown ownership, an active deployment, unexpected account children,
or an in-use/changed subnet stops cleanup and retains the recoverable journal.
Progress survives permission failures and interruption; retry `azd down` after
resolving the reported condition.

Normal account deletion is soft deletion and can reserve the name for 48 hours.
Only explicit `azd down --purge` requests irreversible purge; model deployments
are removed first to avoid charges persisting through soft deletion. Journals
from versions without authoritative Foundry lifecycle evidence are rejected
rather than used to infer account absence or delete dependencies.

Foundry agent hosting configuration is fixed when the account is created. It
cannot be added to an existing account or changed after creation, so
`agentHosting` cannot be used together with `endpoint`.

To reconcile deployments or a pending container registry on an existing project, set the project's full ARM resource ID in the active azd environment:

```sh
azd env set AZURE_AI_PROJECT_ID "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>"
```

`azd ai agent init` sets this value when initialized against an existing project. An endpoint-only service with no resources to reconcile does not require it.

Split `host: azure.ai.connection` services are reconciled during deploy by the
`azure.ai.connections` extension, never by Project provisioning. This applies
to embedded templates and newly ejected Bicep/Terraform alike. Project synthesis
does not read Connection or Toolbox payloads, environments, or credentials.
The system ACR connection used by the Project's registry remains Project-owned.

### Breaking migration

Upgrade the related Foundry extensions together. On-disk Bicep whose compiled
template declares generic Foundry Connection resources (including inline nested
deployments) is rejected with migration guidance. Remove those resources, their
associated parameters and aggregate readiness outputs, or regenerate the IaC
after saving custom changes. Unrelated user-owned parameters named `connections`
or `connectionCredentials` are allowed and retain normal parameter substitution;
parameter names alone do not identify the removed Foundry contract. Linked
templates are not fetched or inspected by this validation.

Only the generated system ACR connection's name, target, project identity and
registry resource ID wiring are exempt. Other `ContainerRegistry` connections,
including those using `ManagedIdentity`, belong in `azure.ai.connection` services.
Preserve that generated wiring when customizing ejected Bicep, or regenerate it.

Update previously ejected Terraform manually as well, including any
required state handoff to avoid destroying resources when removing declarations.
Extension upgrades do not automatically rewrite user-owned IaC.

Declare Connections and Toolboxes as independent services, keep Agent `uses`
dependencies, and run `azd deploy --all` after provisioning the Project. See the
[Connections migration guide](../azure.ai.connections/README.md#breaking-migration).

## Eject existing-project infrastructure

Generate editable infrastructure for an existing Foundry project with its full
ARM resource ID:

```sh
azd ai project add --project-id "<project-resource-id>" --infra
azd ai project add --project-id "<project-resource-id>" --infra=terraform
```

The default format is Bicep. The generated infrastructure references the
existing account and project without taking ownership of them. It manages only
declared model deployments, the system ACR connection, and any required container
registry resources. Endpoint-only setup cannot eject infrastructure; rerun
`project add` with the full project resource ID.

When an agent needs a registry, ejection preserves the registry state selected
during initialization: it creates a registry when none exists, connects an
existing registry when needed, or references an existing project connection
without managing it. Terraform registry output is always named
`container-registry.tf`; Bicep uses `modules/container-registry.bicep`.
Before writing generated files, existing registry endpoints are normalized to
remove URL credentials, query parameters, and fragments.

Project ejection does not read or emit split Connection payloads or credentials,
including values loaded from a local `$ref`. Keep those on `azure.ai.connection`
services, reconciled by the Connections extension during deployment.

Terraform ejection does not support private networking and cannot adopt a
registry already created by the `microsoft.foundry` provider. After ejection,
future `azd provision` runs use the generated files. To add another managed
model deployment after ejection, edit the generated Bicep
`<module>.parameters.json` or Terraform `<module>.tfvars.json` file directly,
then run `azd provision`; `project deployment add` does not update ejected
parameter files.

When provisioning reports insufficient Cognitive Services quota, check usage for the target region with
`az cognitiveservices usage list --location <region>` or request a quota increase in the Azure portal. If an
existing Foundry project should be reused instead, configure its endpoint and set `AZURE_AI_PROJECT_ID` to the
full project resource ID before retrying.

The `azd ai project set`, `show`, and `unset` commands manage the default Foundry project endpoint context. Use `azd ai project add` to author the project service in `azure.yaml`.
