# Foundry Projects

<!-- cspell:ignore Kata kubelet -->

Manage Microsoft Foundry Project resources from your terminal. (Preview)

Requires azd 1.37.0 or later. Deployment preview uses the stable v1 API.

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

For embedded infrastructure, the active azd environment's `AI_PROJECT_DEPLOYMENTS`
JSON array overrides the portable `deployments` defaults in `azure.yaml`. A value
of `[]` intentionally disables managed model deployments. When unset or blank,
interactive provisioning selects models for the current subscription and location
and saves the complete list to that environment; `--no-prompt` (or
`AZD_NO_PROMPT=true`) saves the YAML defaults unchanged. Invalid non-blank values
produce an error rather than being overwritten. `azd ai project deployment add`
also seeds or updates this environment list. Ejected infrastructure retains
user-owned deployment parameters.

When `azd ai project add` changes the configured project identity, it clears the
previous `AI_PROJECT_DEPLOYMENTS` override, including `[]` and malformed values,
before deployment reconciliation. Reconciliation then starts from the current
`azure.yaml` declarations instead of the previous project's list. Selecting the
same project preserves the override and its validation rules. If replacement
fails, the previous environment values are restored.

Endpoint-only setup (`--project-endpoint`) validates the old override before this
cleanup. Malformed values fail validation, and a non-empty deployment list blocks
switching to a different project endpoint. Use `--project-id` to replace the
project and reconcile managed deployments, or `--new-project` to configure a new
project. An override of `[]` does not itself block endpoint-only replacement;
other endpoint-only validation still applies.

Embedded provisioning also initializes a missing or blank
`AZURE_AI_MODEL_DEPLOYMENT_NAME` from the first effective deployment, including
in a newly created environment. Existing runtime defaults are preserved,
including names of reused deployments; an empty deployment list does not
initialize a runtime default.
For on-disk infrastructure, set `AZURE_AI_MODEL_DEPLOYMENT_NAME` explicitly
with `azd env set` or publish it as an infrastructure output. azd does not
guess this runtime default from YAML or environment deployment lists that
the on-disk parameters override.

Existing-project reuse is rechecked after preprovision hooks so a deployment
list supplied by a hook is provisioned rather than silently skipped.

Adopting an existing project reconciles the active environment's effective
deployment list with reuse, "Choose a different model" (any existing deployment
or a catalog model), and skip choices, including an explicit `[]` when no
managed deployments remain. Ejecting infrastructure seeds the generated Bicep
or Terraform deployment parameters from that effective list. Subsequent edits
to those parameter files remain authoritative.

An explicit "Choose a different model" selection also updates
`AZURE_AI_MODEL_DEPLOYMENT_NAME`, even when the previous default still exists.
With multiple alternative selections, the first becomes the runtime default.
Other adoption choices retain the existing default-selection behavior.

`AI_PROJECT_DEPLOYMENT_BINDINGS` stores environment-local adoption bindings as a
JSON object, for example `{"template-chat":"existing-chat"}`. Each declaration
retains its own selected deployment independently of the global runtime default;
an empty target records Skip. Declaration names are matched case-insensitively,
and updates preserve existing declaration-key spelling. Targets that differ only
by case are equivalent; genuinely conflicting targets for case-equivalent keys
are rejected. Prompt-agent deploy and invoke honor these explicit
bindings even after infrastructure ejection. Bindings are cleared when the
environment switches to a different project. Legacy environments without bindings
retain the single-missing-declaration fallback and reject ambiguous replacements.
Adding a managed deployment with `azd ai project deployment add` resets that
declaration's binding to its managed name. For hand-edited agent bindings,
update the JSON object with `azd env set AI_PROJECT_DEPLOYMENT_BINDINGS '<json>'`,
or set it to `'{}'` to clear explicit bindings.

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

### Existing AKS hosting

The Agents init/adoption switch for this preview is **disabled by default**. To enable BYO AKS project initialization and adoption in your shell or CI:

```bash
export AZD_ALPHA_ENABLE_AGENTS_BYO_AKS=true
```

In PowerShell:

```powershell
$env:AZD_ALPHA_ENABLE_AGENTS_BYO_AKS = 'true'
```

The independent `agents.byo_aks` switch uses the process environment. If the variable is absent, BYO AKS project initialization and adoption are disabled. When explicitly set, only `1`, `t`, `T`, `true`, `TRUE`, or `True` enable them; empty, false, and invalid values disable them. `alpha.all`, other preview switches, and service `env:` entries do not override this switch. Values already injected into the process by the azd host count. Foundry projects without `agentHosting` do not require this switch.

The generated account, project and model deployment resources use API `2025-06-01` without AKS hosting. Only the BYO AKS path uses `2026-07-15-preview`; both paths return the same standard project, endpoint and registry outputs.

Accounts use `cog-<resourceToken>` without AKS hosting and `aks-cog-<resourceToken>` with AKS hosting. The selected account name is also its custom subdomain and is used in Project IDs, endpoints, registry connections, and private endpoints. Storage, managed identity, and registry names are derived from the shared resource token.

Agents init/adoption checks inline and referenced project configuration. A disabled preview returns `feature_not_enabled` with the variable to set. Foundry provider initialization, provision, preview, lifecycle hooks, and Bicep eject do not check this switch.

Provision and preview inspect the template's effective `agentHosting` value after parameter merging, including template defaults when no value is supplied. On-disk deployment parameters can override the hosting selections from `azure.yaml`, enable hosting without a YAML declaration, or disable hosting. Local binding lookups require a literal hosting object with a readable `enabled` flag and cluster resource ID; managed identity IDs are read only when needed for an automatic binding. Additional object properties are passed to ARM unchanged. YAML configuration retains its own field and resource-ID validation.

Set `agentHosting.clusterResourceId` on the `azure.ai.project` service to use an existing AKS cluster when creating a **new** Foundry account and project:

```yaml
infra:
  provider: microsoft.foundry
services:
  ai-project:
    host: azure.ai.project
    agentHosting:
      clusterResourceId: ${AKS_CLUSTER_ID}
```

`agentHosting` does not require a `network` block or additional subnets. Omit `network` for public networking, or supply it to use the standard account network injection and private-endpoint resources. Existing AKS hosting supports an automatically created ACR with private account networking. Without `agentHosting`, private networking requires an Agent configuration that does not require building a container image.

Use an existing AKS cluster configured for Foundry agent hosting. Before deployment, the provider reads the cluster, its AgentPool children and regional VM-size capabilities to check the Foundry hosting prerequisites. When effective hosting is enabled, it always reads and validates the selected cluster's actual kubelet principal object ID, regardless of `includeAcr` or an explicit kubelet binding. It resolves the built-in federation role when that deployment binding is needed. These checks do not create or reconfigure AKS, inspect Kubernetes node Ready conditions, or test runtime connectivity.

The default resource graph creates:

- One StorageV2 account (`st<resourceToken>`), with shared keys, anonymous blobs, and insecure transport disabled.
- Separate hosting-management and workload user-assigned managed identities.
- When an Agent requires building a container image, one native azd Container Registry (`cr<resourceToken>`), its Project connection, and `AcrPull` for the Project and AKS kubelet identities. A project without such an Agent does not automatically create a registry; pre-built images and code/ZIP configurations follow the standard Foundry ACR selection.
- Hosting-management Reader and Kubernetes Extension Contributor roles on the existing cluster, federation-management permission on the workload identity, and workload Storage Blob Data Contributor permission on the storage account.

The account receives a `ManagedCluster` / `default` hosting configuration. Its data-plane access follows the optional `network` configuration. **Storage and any automatically created ACR use public authenticated endpoints**, so the cluster must be able to reach them. Registry creation uses API `2023-07-01` and relies on the default registry RBAC mode. The deploying principal needs read access to AKS, the built-in role definitions and target resource group, creation rights for the new resources, and role-assignment write rights at the cluster, workload identity and Storage scopes. Creating an ACR additionally requires registry role-assignment write rights. Configuring private networking additionally requires the corresponding network permissions.

Storage and the two identities can each be supplied independently using complete ARM resource IDs:

```yaml
agentHosting:
  clusterResourceId: ${AKS_CLUSTER_ID}
  storageAccountResourceId: ${STORAGE_ACCOUNT_ID}
  hostingManagementIdentityResourceId: ${HOSTING_IDENTITY_ID}
  workloadIdentityResourceId: ${WORKLOAD_IDENTITY_ID}
```

Omit any supporting-resource field to retain its default creation/update path. Empty or null Storage and identity selections also retain their defaults, including when an environment variable resolves to an empty value. Undefined variables without a default are rejected, and `clusterResourceId` must resolve to a nonempty cluster resource ID. A supplied resource is referenced without a Storage or identity PUT, reconfiguration, or owner tag. Registry selection follows the standard Foundry rules; `agentHosting` does not accept a registry resource ID. An existing `endpoint:` cannot receive hosting. `${VAR}` inputs resolve from the active azd environment, including in `$ref` service definitions; a cluster ID is not a Kubernetes context name.

Agents using pre-built images retain their standard registry-connection and authentication requirements; azd does not automatically grant the AKS cluster access to an external registry.

When the hosting-management identity is omitted, provision and preview check the deployer's `Microsoft.Resources/deployments/write` permission at the AKS resource-group scope, where the cluster role-assignment modules are deployed. Cluster-scope role-assignment permissions alone are insufficient, including when AKS and Foundry use different resource groups. This check also applies when all supporting resources use defaults; a supplied hosting-management identity skips these cluster-grant deployments.

The user or automation identity running the deployment also needs the following permissions:

| Resource selection | Required deployer permission |
|---|---|
| Supplied workload identity with an omitted hosting-management identity | `Microsoft.Resources/deployments/write` on the supplied workload identity's resource group, where the nested federation-grant deployment runs. |
| Supplied Storage with an omitted workload identity | `Microsoft.Resources/deployments/write` on the supplied Storage account's resource group, where the nested Storage-grant deployment runs. |
| Supplied hosting-management identity | `Microsoft.ManagedIdentity/userAssignedIdentities/assign/action` on that identity to attach it to the Foundry account, for example through Managed Identity Operator or an equivalent role. |

These permissions are additional to the applicable resource and role-assignment permissions above. Resource-scoped role-assignment write permission alone does not authorize the resource-group-scoped nested deployments, including in another subscription.

For missing principal bindings declared by the template, the provider reads the effectively selected supplied identities to resolve their principal IDs. A missing federation-role binding is resolved in the actual workload identity's subscription when the compiled plan identifies that scope, including when its name is unresolved. Otherwise, it uses the effective supplied workload identity's subscription, or the deployment subscription when that identity is omitted. Explicit bindings do not require these automatic binding lookups; applicable preflight evidence reads still run. Supplied resources and their existing grants must already be configured for Foundry agent hosting.

`azd provision --preview` resolves the host bindings and returns the standard ARM what-if changes without creating resources. `azd provision` uses the same ARM deployment path as a project without AKS hosting. The Bicep template derives names for omitted supporting resources and creates them with the required grants. Successful provisioning returns the standard Project ID, endpoint and registry outputs used by `azd deploy`. Registry outputs are empty when no registry is selected.

`azd down` uses the standard Foundry teardown path, including its confirmation and `--force` behavior. It deletes model deployments and the target resource group with all resources inside it, including pre-existing resources; cleanup is not limited to resources created by the ARM deployment. An existing AKS cluster, network, supplied Storage, or supplied identity in the target resource group is deleted along with the group. Resources in other resource groups are outside this group-deletion scope. Infrastructure layers retain the standard Foundry resource-group ownership checks.

Embedded provisioning and Bicep eject are supported through `microsoft.foundry`; Terraform eject is rejected before changing files. On-disk Bicep templates and explicit parameters control resource naming, creation and authorization for both default and supplied supporting resources. Bicep compilation, ARM JSON parsing and Foundry Connection compatibility checks apply to on-disk infrastructure. ARM validates the resulting deployment; custom templates are responsible for the resources and grants they declare.

BYO AKS uses the standard Foundry resource-group creation/update behavior. The deployment applies the configured location and tags; an existing target resource group's location must match the deployment location.

The provider fills `federatedIdentityRoleDefinitionId` and supplied identities' `hostingPrincipalId` / `workloadPrincipalId` only when the template declares the parameter and the user has not provided it. The same rule applies to `kubeletObjectId` when `includeAcr=true`. Explicit parameter values and envelopes take precedence, with parameter names matched ignoring case, and are forwarded unchanged. User parameters absent from the template are also forwarded so ARM reports the error. Location, resource-name salt and resource-group parameters follow the normal on-disk parameter precedence and ARM validation.

Provision and preview run read-only preflight for every BYO AKS plan, including plans that supply only AKS and create all supporting resources. Checks cover resource existence and identity metadata, subscription owning tenants, supplied-identity grants, AKS hosting prerequisites, the selected existing Agent subnet, and deploying-principal permissions at the actual operation scopes. Ordinary Foundry projects without `agentHosting` do not enter this pipeline.

| Checks | Required evidence |
|---|---|
| A01-A04 | AKS Base rather than Automatic; actual running Kubernetes version at least 1.31; Azure CNI Overlay or a qualifying Agent pool's Pod Subnet; supported managed cluster identity and kubelet object ID; enabled OIDC with issuer and Workload Identity. |
| A05 | Required System pools' applicable VM sizes provide at least 4 available vCPU and 16 GiB. |
| A06-A10 | One complete User Agent pool has Azure Linux, KataVmIsolation, at least one node, at least 8 available vCPU and 32 GiB, an `AKSAzureLinux-V3katagen2-` image at least `202608.14.0`, `agents.ai.azure.com/sandbox-node=true`, disabled SSH, and successful cluster/required pool ARM provisioning states. Requirements cannot be combined across different pools. |
| A11 | Azure or Calico network policy, or the supported Cilium dataplane. |
| N02 | The selected existing `network.agentSubnet` is private IPv4, at least /27, contained in its VNet, non-overlapping with the selected AKS Node subnet, and delegated to `Microsoft.App/environments`. Its provisioning state is not required. New or unselected Agent subnets are not applicable; unrelated Node subnets are not compared. |
| P02, P04, P06 | Identity lifecycle, Storage writes, and unconditional grant authority follow actual creation/reference decisions and current existence. Existing targets use their resource scopes; future targets use their existing parent scope. |
| P03 | Actual selected VNet reads, subnet writes/joins, private endpoints, private-DNS groups, zones and links use the selected resource or deployment-parent scopes. Existing DNS zones include an explicitly selected external DNS subscription's owning tenant check. |
| P05, P07 | Native registry writes and native pull-grant authority apply only to the registry and grants selected by the actual plan. A plan without a native registry does not query a future registry or force ACR creation. |
| P09 | Actual cluster-grant modules require deployment-write at the AKS resource group even when it is also the Foundry resource group. Cross-resource-group/subscription grant deployments retain their scoped deployment read/write and operation-read checks. |

Determinate mandatory failures, including nonexistent references, incompatible configuration and missing required permissions, block deployment and ARM what-if. Unreadable or incomplete Azure evidence retains the corresponding mandatory check result. An unresolved local template-plan dependency instead produces a visible `WARNING` naming the resource ID, or its type and template/module position, the check ID and the reason; only that dependent check is recorded as `SKIPPED`, not `PASS`. Later independent checks still run, and other mandatory failures still block deployment.

Supplied Storage must have a successful provisioning state, StorageV2 kind, Hot tier, HNS explicitly disabled, HTTPS-only transport, TLS 1.2 or later, Shared Key disabled, and anonymous Blob access disabled. Anonymous Blob access is verified as disabled only when Azure returns `allowBlobPublicAccess: false`; `true` fails the security check, while a missing or `null` value is unverified and blocks provisioning. Other missing security properties use the Storage API's documented defaults. Preflight does not change supplied Storage settings. Zone redundancy is recommended but does not block provisioning. The network check requires a valid HTTPS Blob endpoint and explicit network settings: public access with an allow-default firewall passes the static baseline, not a connectivity test. Restricted/private network connectivity is skipped with a warning and does not block provisioning.

A supplied hosting-management identity needs Reader and Kubernetes Extension Contributor on the cluster, plus federation-management access when the workload identity already exists. A supplied workload identity needs Storage Blob Data Contributor when Storage already exists. Applicable inherited permissions, including management-group assignments returned by the target scope's Azure query, or provably equivalent unconditional permissions are accepted; missing or unsupported evidence blocks provisioning. If the dependency does not yet exist, preflight checks the deployer's creation and unconditional role-assignment permissions and ARM creates the dependency and grant. ARM creates the federation or Storage grant when either corresponding resource selection is omitted; two explicit endpoints require existing grants.

Preflight uses current Azure state, not a persisted deployment journal. An existing target missing a required supplied-identity grant can block a retry, including after an interrupted deployment. Omitted Storage retains the deterministic `st<resourceToken>` name and ordinary ARM creation/update path even if it already exists; current existence does not convert it into a supplied reference or establish ownership.

For both default and supplied supporting resources, preflight identifies resources from the actual compiled ARM template and merged parameters, including local nested modules, resolvable conditions, names, resource scopes, selected network resources and native ACR connections. It does not compare the template against a standard-template digest or substitute standard names for rewritten resources.

The local plan adapter is bounded, not a general ARM interpreter. Inner-scoped nested templates use their own `deployment().name`. Ordinary `reference()` returns resource properties; `reference(..., 'Full')` returns the full resource shape, preserving symbolic identity principal IDs for grant checks. Runtime-only reference properties, unavailable linked templates and unsupported expressions remain unresolved; dependent checks warn and skip individually. Supported constant `uniqueString` and `guid` expressions use the existing azd-managed Bicep tool. Explicit deployment bindings retain their normal precedence.

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
