# Foundry Projects

<!-- cspell:ignore strconv Kata kubelet -->

Manage Microsoft Foundry Project resources from your terminal.

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

The example is illustrative. Project/deployment add commands and the Foundry
Provision provider report [operation and internal step timings](../azure.ai.agents/docs/phase-telemetry.md)
through the existing reporter, including authoring delegated by agent Init.
Existing provisioning span attributes remain unchanged. Add an event only after its product question, bounded
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

### Application Insights

New Foundry projects automatically receive a workspace-backed Application
Insights component and a project-scoped `AppInsights` connection during normal
provisioning. This includes projects created directly with `azd ai project add`
and projects created for agents. No additional command, flag, or connection
service is required. Existing projects are unchanged unless monitoring is
explicitly configured; upgrading an environment does not opt an existing project
into monitoring.

To bring your own component, add the optional project setting:

```yaml
infra:
  provider: microsoft.foundry
services:
  my-project:
    host: azure.ai.project
    monitoring:
      applicationInsightsResourceId: /subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.Insights/components/<component>
```

The component must already reference an accessible Log Analytics workspace.
It can belong to another resource group or subscription that the deploying
identity can read. Provisioning references it without changing its tags,
authentication, network settings, retention, or workspace, and creates only a
missing project connection. Credentials are resolved during deployment, not
stored in generated parameter files or azd environment outputs. A compatible
existing project or shared account connection is preserved, including
Managed Identity or Microsoft Entra authentication.

Creating a new Application Insights component for an existing project is not
supported; an existing project must set `applicationInsightsResourceId` to
enable monitoring.
To skip new monitoring setup, use:

```yaml
services:
  my-project:
    host: azure.ai.project
    monitoring:
      enabled: false
```

For embedded infrastructure, this opt-out does not delete or disable an existing
component, workspace, or connection. It also does not relinquish an existing
azd-owned supporting resource group's cleanup binding.

New connections use ApiKey authentication, are scoped to one project, and are
named `azd-appinsights-<project-name-hash>` to stay within resource name limits.
No monitoring RBAC roles are added. Each user who queries traces needs the
Log Analytics Reader role on the Application Insights resource; azd does not
grant it.
Discovery rejects conflicting destinations, protected names, ambiguous sharing,
unreadable resources, and authentication or ingestion policies that would need
to be weakened. ApiKey setup does not enable local authentication or configure
private-network ingestion. A project with a `network:` block always skips
automatic monitoring, whether `monitoring:` is omitted, empty, or names a
component, so provisioning is never blocked; azd prints a warning when it
skips monitoring. Set up monitoring for such
projects outside azd. Other monitoring failures can fail normal provisioning; there
is no second best-effort deployment or agent deployment repair hook.

Monitoring creates billable Azure resources and can store agent telemetry,
including application-supplied content. Review your application's telemetry,
data handling, and retention requirements before deployment. Newly created
workspaces start with 30-day retention; existing workspaces are not changed.
The normal `AZURE_APPLICATION_INSIGHTS_RESOURCE_ID`,
`AZURE_APPLICATION_INSIGHTS_APP_ID`,
`AZURE_LOG_ANALYTICS_WORKSPACE_RESOURCE_ID`,
`AZURE_AI_PROJECT_APPINSIGHTS_CONNECTION_NAME`, and
`AZD_FOUNDRY_MONITORING_MODE` outputs describe provisioning configuration, not
proof that an agent has emitted or ingested traces.

#### Ejected infrastructure

Monitoring is part of the main infrastructure graph, not an orphan template or
a separate deployment. Before ejection, `azure.yaml` controls monitoring.
After ejection, the Bicep files and their parameters, or Terraform files and
their variables, are authoritative. Changing stale YAML monitoring values does
not override those files. General configuration loading still requires valid
YAML and resolvable local references, even for values no longer used by IaC.

Generated Bicep calls `modules/monitoring.bicep`; Terraform declares monitoring
in `monitoring.tf`. Monitoring modes are `none`, `create`, `reuse-connect`, and
`already-connected`. The last mode references compatible external monitoring
without managing its connection. Creating monitoring for a new project shares
the normal supporting resource group used by ACR rather than creating a separate
monitoring resource group. Native/Bicep preview uses normal ARM What-If.

Monitoring-enabled Terraform exports require Terraform 1.11 or later and AzAPI
2.7 or later (existing-project exports retain their higher provider baseline).
They use an ephemeral credential read and a write-only connection field.
Previously native-created resources are not implicitly imported into Terraform;
reference the component explicitly for reuse or perform a deliberate state
handoff. Terraform resources have normal removal and destroy semantics:
changing managed monitoring to `none` or deleting its declarations can schedule
resource deletion, unlike the embedded-provider YAML opt-out. Review the plan
before applying changes. A reuse-only existing-project export cannot switch to
creation until its owned supporting resource-group graph is declared.

Export-time discovery selects a stable graph, and Terraform rechecks effective
connections and policy during planning/apply. Managed resource counts do not
depend on whether a connection happens to exist. Neither ARM nor Terraform
provides a transaction across discovery and concurrent connection changes:
avoid creating different monitoring connections concurrently with provisioning.

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

The BYO AKS preview is **disabled by default**. To enable project initialization, adoption, provisioning, and provisioning preview in your shell or CI:

```bash
export AZD_ALPHA_ENABLE_AGENTS_BYO_AKS=true
```

In PowerShell:

```powershell
$env:AZD_ALPHA_ENABLE_AGENTS_BYO_AKS = 'true'
```

The independent `agents.byo_aks` switch uses the process environment. If the variable is absent, BYO AKS use is disabled. When explicitly set, only `1`, `t`, `T`, `true`, `TRUE`, or `True` enable it; empty, false, and invalid values disable it. `alpha.all`, other preview switches, and service `env:` entries do not override this switch. Ordinary Agents extension commands may receive selected azd environment values injected by the host. The Projects provisioning listener does not receive that injection: `azd provision` and `azd provision --preview` require the switch in the current shell or CI process environment. `azd env set` alone does not enable it. Foundry provisioning without effective AKS hosting does not require this switch.

The generated account, project and model deployment resources use API `2025-06-01` without AKS hosting. Only the BYO AKS path uses `2026-07-15-preview`; both paths return the same standard project, endpoint and registry outputs.

Accounts use `cog-<resourceToken>` without AKS hosting and `aks-cog-<resourceToken>` with AKS hosting. The selected account name is also its custom subdomain and is used in Project IDs, endpoints, registry connections, and private endpoints. Storage, managed identity, and registry names are derived from the shared resource token.

Agents init/adoption checks inline and referenced project configuration. The Projects provider also requires this switch for `azd provision` and `azd provision --preview` when effective `agentHosting.enabled=true`. A disabled preview returns the `feature_not_enabled` compatibility error with the variable to set, before credential initialization or BYO AKS, role, or identity reads. Foundry provider initialization, infrastructure state and parameter queries, synthesis, lifecycle hooks, Bicep eject, and `azd down` do not check this switch.

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

`agentHosting` does not require a `network` block or additional subnets. Omit `network` for public networking, or supply it to use the standard account network injection and private-endpoint resources. Existing AKS hosting supports an automatically created ACR with private account networking. Without `agentHosting`, private networking supports prebuilt images, code/ZIP deployments, and source builds using an external registry; it does not support an automatically created ACR.

Use an existing AKS cluster configured for Foundry agent hosting. When effective hosting is enabled, the provider always reads and validates the selected cluster's kubelet principal object ID, regardless of `includeAcr` or an explicit kubelet binding. It resolves the built-in federation role when that deployment binding is needed. It does not validate AKS or network readiness or reconfigure the cluster.

The default resource graph creates:

- One StorageV2 account (`st<resourceToken>`), with shared keys, anonymous blobs, and insecure transport disabled.
- Separate hosting-management and workload user-assigned managed identities.
- When an Agent requires building a container image, one native azd Container Registry (`cr<resourceToken>`), its Project connection, and `AcrPull` for the Project and AKS kubelet identities. A project without such an Agent does not automatically create a registry; pre-built images and code/ZIP configurations follow the standard Foundry ACR selection.
- Hosting-management Reader and Kubernetes Extension Contributor roles on the existing cluster, federation-management permission on the workload identity, and workload Storage Blob Data Contributor permission on the storage account.

The account receives a `ManagedCluster` / `default` hosting configuration. Its data-plane access follows the optional `network` configuration. **Storage and any automatically created ACR use public authenticated endpoints**, so the cluster must be able to reach them. Registry creation uses API `2023-07-01` and relies on the default registry RBAC mode. The deploying principal needs read access to AKS, the built-in role definitions and target resource group, creation rights for the new resources, and role-assignment write rights at the cluster, workload identity and Storage scopes. Creating an ACR additionally requires registry role-assignment write rights. Configuring private networking additionally requires the corresponding network permissions.

When the hosting-management identity is omitted, the deploying principal also needs `Microsoft.Resources/deployments/write` at the existing AKS resource-group scope. The cluster role grants are created through nested ARM deployments in that group, including when it differs from the Foundry target resource group.

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

A supplied hosting-management identity needs Reader and Kubernetes Extension Contributor on the cluster. A supplied hosting-management identity and a supplied workload identity require an existing federation-management grant between them. A supplied workload identity and supplied Storage require an existing Storage Blob Data Contributor grant. ARM creates the federation or Storage grant when either corresponding resource selection is omitted; two explicit endpoints require existing grants. A supplied hosting-management identity skips the cluster-grant deployments. Cross-subscription grants use the selected workload or Storage resource's subscription and resource group.

The user or automation identity running the deployment also needs the following permissions:

| Resource selection | Required deployer permission |
|---|---|
| Supplied workload identity with an omitted hosting-management identity | `Microsoft.Resources/deployments/write` on the supplied workload identity's resource group, where the nested federation-grant deployment runs. |
| Supplied Storage with an omitted workload identity | `Microsoft.Resources/deployments/write` on the supplied Storage account's resource group, where the nested Storage-grant deployment runs. |
| Supplied hosting-management identity | `Microsoft.ManagedIdentity/userAssignedIdentities/assign/action` on that identity to attach it to the Foundry account, for example through Managed Identity Operator or an equivalent role. |

These permissions are additional to the applicable resource and role-assignment permissions above. Resource-scoped role-assignment write permission alone does not authorize the resource-group-scoped nested deployments, including in another subscription.

For missing principal bindings declared by the template, the provider reads the effectively selected supplied identities to resolve their principal IDs. A missing federation-role binding is resolved in the effective workload identity's subscription, or the deployment subscription when that identity is omitted. Explicit bindings do not require these lookups. This is deployment binding resolution, not a readiness, networking, or permission preflight. Supplied resources and their existing grants must already be configured for Foundry agent hosting.

After local validation, preview opt-in, and credential initialization, independent AKS, federation-role,
and supplied-identity reads run concurrently. The first failed read cancels outstanding
reads and returns its original error; errors are selected by completion order.
All required reads must succeed before bindings are merged and ARM deployment or what-if can proceed.

`azd provision --preview` resolves the host bindings and returns the standard ARM what-if changes without creating resources. `azd provision` uses the same ARM deployment path as a project without AKS hosting. The Bicep template derives names for omitted supporting resources and creates them with the required grants. Successful provisioning returns the standard Project ID, endpoint and registry outputs used by `azd deploy`. Registry outputs are empty when no registry is selected.

`azd down` uses the standard Foundry teardown path, including its confirmation and `--force` behavior. It deletes model deployments and the target resource group with all resources inside it, including pre-existing resources; cleanup is not limited to resources created by the ARM deployment. An existing AKS cluster, network, supplied Storage, or supplied identity in the target resource group is deleted along with the group. Resources in other resource groups are outside this group-deletion scope. Infrastructure layers retain the standard Foundry resource-group ownership checks.

Embedded provisioning and Bicep eject are supported through `microsoft.foundry`; Terraform eject is rejected before changing files. On-disk Bicep templates and explicit parameters control resource naming, creation and authorization for both default and supplied supporting resources. Bicep compilation, ARM JSON parsing and Foundry Connection compatibility checks apply to on-disk infrastructure. ARM validates the resulting deployment; custom templates are responsible for the resources and grants they declare.

BYO AKS uses the standard Foundry resource-group creation/update behavior. The deployment applies the configured location and tags; an existing target resource group's location must match the deployment location.

The provider fills `federatedIdentityRoleDefinitionId` and supplied identities' `hostingPrincipalId` / `workloadPrincipalId` only when the template declares the parameter and the user has not provided it. The same rule applies to `kubeletObjectId` when `includeAcr=true`. Explicit parameter values and envelopes take precedence, with parameter names matched ignoring case, and are forwarded unchanged. User parameters absent from the template are also forwarded so ARM reports the error. Location, resource-name salt and resource-group parameters follow the normal on-disk parameter precedence and ARM validation.

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

Use `--new-project` for greenfield setup instead of adopting a workspace
project endpoint or project ID:

```sh
azd ai project add --new-project
```

`--new-project` cannot be combined with `--project-id` or
`--project-endpoint`. If the environment already has a deployed registry
binding, repeating setup preserves that project identity. Use a new azd
environment to create another project.
Setup restores a missing subscription from the deployed project binding and
rejects a conflicting `AZURE_SUBSCRIPTION_ID` before changing configuration.

### Reuse an external container registry

This preview is **off by default**. Enable the shared process switch before
running either the projects or agents extension:

```sh
export AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE=true
```

In PowerShell, use `$env:AZD_ALPHA_ENABLE_FOUNDRY_ACR_REUSE = 'true'`.
Set the same variable in each CI job that operates on the reused registry.
The switch uses `strconv.ParseBool`; `on` and `yes` do not enable it.
Core `azd config` alpha settings do not enable this preview. The evaluator
reads the current process, including values already injected by the azd host;
it does not open dotenv files itself.

With the switch off, reuse setup and all reuse lifecycle operations fail with
enablement guidance before changing project files, environment values, saved
context, or Azure resources. This includes **preview, down, recovery, and
eject**, even if the environment already has a reuse binding or unfinished
access cleanup and its legacy registry variables are absent. Re-enable the
switch to clean up; turning it off does not delete or convert ownership.
Ordinary automatic registry creation and existing project connection workflows
without new reuse intent remain available.

The source-build demand consistency correction is always on: a bare `image`
still requests automatic registry provisioning when package/deploy would build
from source. Without opt-in, fresh create returns normal outputs without a
binding-only project identity lookup, canonical binding config write, or new
mode write. Saved create bindings retain immutable identity validation and
maintenance. Recovered create ownership remains available for safe operations
without newly persisting or unsetting a transient binding while off.

Legacy existing-project `reuse-connect` environments without canonical reuse
bindings or pending operations remain supported without the switch. Canonical
reuse and pending operations are checked before the existing-project exemption.
New greenfield reuse intent still requires opt-in.

Guards and lifecycle operations honor `--environment` (forwarded by the host
as `AZD_ENVIRONMENT`) before the saved default. Environment values, registry
bindings, and pending operations come from that same selected environment.
An unreadable explicit selection is an error, not a fallback to the default.

For greenfield reuse, project hooks also check `existingAcrResourceId` in the active on-disk
`main.parameters.json` or `main.bicepparam`, using the configured Foundry
path and module for a root or named layer. Removing environment aliases,
saved bindings, and pending state does not bypass this check. JSON values
use the host environment's `${VAR}` substitutions; `.bicepparam` takes
precedence and its expressions are resolved by Bicep. Disabled preflight
retains normal Bicep installation and module restoration. Dependency caches
may be populated before evaluation, but rejected reuse does not change
project files, environment/config values, saved context, or Azure resources.
Invalid parameters remain errors rather than being treated as non-reuse.

Provider initialization and `azd down` do not compile infrastructure.
Down checks recorded environment selectors, bindings, pending cleanup, and
recovered deployment facts instead of current parameter files. Recorded
reuse still requires the switch; a source-only selector that has not been
deployed does not block teardown. On-disk provisioning keeps automatic Bicep setup,
even while the preview is off.

The registry build validation rule reports the same enablement guidance
before registry lookups. The prepackage, preprovision, and predeploy hooks
enforce the gate independently of optional provision validation.

For source-built hosted agents in a new Foundry project, select an existing
Azure Container Registry by its full ARM resource ID:

```sh
azd ai project add --new-project --acr-resource-id "/subscriptions/<subscription-id>/resourceGroups/<external-resource-group>/providers/Microsoft.ContainerRegistry/registries/<registry-name>"
```

This is supported with Bicep only. The registry must be in the Foundry project's
subscription (`AZURE_SUBSCRIPTION_ID`) and outside every resource group managed
by azd, not just the Foundry group, because `azd down` deletes those groups.
`AZURE_RESOURCE_GROUP` does not identify every group an application template can
create. Reuse therefore requires Foundry-only infrastructure: a non-Foundry root
deployment or any non-Foundry infrastructure layer is rejected, even when the
registry is outside the groups named in environment values.
Cross-subscription registry reuse is not supported. You are responsible for
registry network connectivity and appropriate image push permissions.

An external source-build registry selection cannot be combined with
`agentHosting`, including an unused project-only selection. Reuse does not
manage AKS node pull access. Remove the selector and let azd create a registry
when a source build needs one, or use explicit prebuilt images with
user-managed registry authentication.

Embedded and currently generated ejected Bicep keep the
`microsoft.foundry` provider, which enforces the switch for provision, preview,
and down. **Manually changing `infra.provider` to core `bicep` is unsupported**:
its preview skips extension lifecycle hooks, so it is outside this enforcement
contract. Arbitrary direct Bicep/ARM execution is also outside the contract.
Do not change the provider to bypass the switch.

For an ABAC registry (`AbacRepositoryPermissions`), source-built hosted agents
must use local Docker builds with `docker.remoteBuild: false`. Hosted source
initialization normally enables remote builds unless network injection requires
local builds. The current ACR Quick Build request does not authenticate the
source registry, so ABAC external-registry reuse with
`docker.remoteBuild: true` is rejected before provisioning. Repository Writer
permission alone does not make that remote request supported. Use local Docker
with Repository Writer push permission, a prebuilt image, or a classic RBAC
registry instead. Code deployments, prebuilt images, and project-only selections
without source-build demand do not trigger this remote-build restriction.
The scope and `agentHosting` restrictions still apply to the selector.

An `image` value alone does not select a prebuilt deployment: source build is
the default, including with `--no-prompt`. To use an existing image, set
`docker.imagePassthrough: true` together with a nonempty image. Add
`registryConnectionId` when a private registry connection is needed; it also
requires `docker.imagePassthrough: true`. The legacy environment value
`AZD_AGENT_SKIP_ACR=true` also selects prebuilt deployment when the expanded
image is nonempty.

An authored service-level `docker` block takes precedence over the entire
`config.docker` block, including through `$ref`. In particular, an explicit
service-level `docker.remoteBuild: false` retains local builds even when
`config.docker.remoteBuild` is `true`.

The registry and its images remain externally owned and are retained by
`azd down`. Default and JSON output identify the selected registry and its
external ownership.

`project add --output json` includes an optional `containerRegistry` object
when a registry resource ID is available. Its `resourceId` is the registry ARM
ID. Its `externallyOwned` boolean is `true` for a reused registry or an
already-connected registry on an existing project, and `false` for a registry
managed by azd. Externally owned registries are retained by `azd down`.
The object is omitted when no registry is known. This is an additive change:
`schemaVersion` remains `1`, and existing fields retain their names and meanings.
JSON consumers should tolerate unknown optional fields.

Setting `AZURE_CONTAINER_REGISTRY_RESOURCE_ID` with `azd env set` selects the
same external-registry workflow and reports the same ownership. A registry
is not considered azd-managed merely because it is in the deployment resource
group. Existing azd-created registries retain their deployed binding or matching
legacy outputs; legacy ownership is verified against the previous Azure
deployment before provisioning or deleting the group. Unreadable environment
state or unverified ownership stops the operation instead of creating a
replacement registry.
For a deployed binding, `azd env refresh` also retains its registry ID,
endpoint, and connection when the latest deployment has empty registry
outputs. Refreshing does not change the binding or registry access.
Project-only provisioning retains an azd-created registry's name and the
verified nested creation deployment in root deployment tags. Recovery reads
that creation operation again; the tags alone never establish ownership.
Missing creation history blocks recovery and teardown rather than permitting
an unverified deletion.
An unused external-registry selection also survives refresh when the latest
deployment has empty registry outputs, even if a source-built agent was added
to `azure.yaml` after that deployment.
The external-registry build checks apply to `reuse-connect` selections, not
azd-created registries retained through a `create` binding or matching legacy
outputs. Managed registries can remain inside the Foundry resource group.
Legacy full outputs and partial-deployment project connections do not establish
registry ownership on their own: recovery requires a successful creation
operation for that exact registry in the environment's owning deployment
history, including nested deployments and all operation pages. Read operations
do not establish ownership and do not invalidate a successful creation operation
elsewhere in that history. Missing or ambiguous evidence stops
recovery and teardown before ownership is saved or resources or access grants
are deleted.

With an external registry selection, prompted subscription and location choices
are saved only after the final template, parameters, deployment scope, and
remote-build compatibility pass validation. Build demand is recomputed using
those prompted values, including environment-dependent service conditions.
This applies to embedded and on-disk Bicep, local and remote builds, JSON
parameter files, and `.bicepparam` files, including custom root and
infrastructure-layer routes. An invalid final selection leaves those choices
unpersisted.

On-disk Bicep registry/network compatibility is checked after merging authored
parameters and resolving effective template defaults. Registry selectors supplied
only in JSON, `.bicepparam`, or a template default are honored. Setting
`includeAcr` or `enableNetworkIsolation` to `false` in the effective deployment
parameters remains authoritative.
Registry access parameter declarations are matched ignoring case, including
the owned-grant recovery metadata, consistent with ARM parameter matching.
When creating an ACR, a declared `enableNetworkIsolation` parameter must have a
literal boolean value for local compatibility checks; ARM expressions are not
evaluated locally.

Before provisioning, the flag overrides environment defaults and saves
`AZURE_CONTAINER_REGISTRY_RESOURCE_ID` with `AZD_FOUNDRY_ACR_MODE=reuse-connect`.
You can select a registry before adding agents; the selection remains pending
until a source-built agent requires it, including across project-only provisioning,
environment refresh, and teardown. Configured project or account names do not
discard an unused selection. Source initialization validates the selector and
rejects unknown application teardown scopes before changing files or environment
values.
Interactive setup validates the selection against the final chosen subscription
and planned teardown group before scaffolding a new project or environment.
The validated subscription is reused without a second selection prompt.
An explicit empty value,
an existing project selector, or a manifest containing only code, prebuilt-image, or
non-container agents is rejected. Without a selector, source builds continue
to create a registry automatically.
An unused `AZURE_RESOURCE_GROUP` value does not add a teardown group when only
a Foundry layer is configured.

Project-only Bicep eject retains the registry selection without writing an
`includeAcr: false` override. Adding a source-built agent later lets the provider
enable registry access from the new build demand. In an older project-only
parameters file, remove the generated `parameters.includeAcr` object if it only
reflects that earlier lack of demand. An intentional user-authored `false`
override remains authoritative.
That override controls infrastructure creation, not the service's build mode.
Remote source builds still validate the selected or bound registry's ABAC
compatibility when `includeAcr` is `false`.

Removing source-build demand does not release an existing registry binding.
Recovery can use the deployed project connection even when no azd-owned grants
exist. After a failed connection deployment, verified access-operation parameters
allow `azd down` to clean up owned grants before removing deployment evidence.
An unused selector with no connection or owned access operation remains pending.

Once a project has a deployed registry binding, registry migration is not
supported. Repeating setup with the same selection preserves the deployed
project identity and ownership whether ordinary project and registry ID outputs
are present or absent. Explicit `--project-id` or `--project-endpoint` adoption
cannot convert this environment's owned project to an existing-project target.
Bicep eject uses the binding's recorded registry ID when no registry
environment-variable reference is available. Use a new azd environment to choose
a different registry; do not clear internal mode or ownership state to bypass
this check.

Provisioning preview checks existing pull access using the same read-only access
plan as deployment. It does not create access grants or persist pending access
operations.

Registry access operations persist their role together with the ownership token.
If the permission mode changes before a retry, azd retires the previous operation
only after its deployments have completed and only removes grants with exact
ownership proof. It then plans a fresh operation for the current role.
If the mode changes during deployment, azd preserves the deployed grant's exact
ownership and verifies effective pull access before reporting success. Missing
access returns retry or cleanup guidance instead of a successful deployment
result; administrator-managed grants are not changed.
Re-eject older Foundry Bicep templates before provisioning an external registry
so their access-operation contract accepts the persisted role and owned-grant
recovery metadata.

If local registry lifecycle state is lost, azd can recover owned grants from
retained deployment access-operation metadata. Repeated provisioning retains
all owned grants, including earlier pull roles after a permission-mode change
and grants from before a project-only provision. Recovery verifies the registry,
project principal, role, and ownership marker before recording a grant.
An already-deleted grant still allows cleanup of its verified deployment records.
Recovered bindings are saved before cleanup even for azd-created registries or
when there are no owned grants. If the resource group is deleted but retirement
of the root deployment fails, the saved binding allows a retry without reading
the deleted project. Failure to save recovery state prevents destructive cleanup.
Mismatched or unreadable grant ownership stops teardown. Legacy deployments
without access-operation metadata do not authorize deleting registry grants.

If deployment fails or is canceled, resource-group and registry-access ownership
recovery are attempted independently. Recovery errors are reported without
preventing the other ownership record from being recovered.

Successful `azd down` also removes this environment's subscription-level Foundry
deployment record before clearing registry lifecycle state, so provisioning again
does not try to recover the deleted project. Failed cleanup preserves recovery
state for a retry. Removing the deployment record does not delete external
resources. Custom deployment roles must allow
`Microsoft.Resources/deployments/delete` at the project subscription scope.

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
The system ACR connection and generated system AppInsights connection remain
Project-owned.

### Breaking migration

Upgrade the related Foundry extensions together. On-disk Bicep whose compiled
template declares generic Foundry Connection resources (including inline nested
deployments) is rejected with migration guidance. Remove those resources, their
associated parameters and aggregate readiness outputs, or regenerate the IaC
after saving custom changes. Unrelated user-owned parameters named `connections`
or `connectionCredentials` are allowed and retain normal parameter substitution;
parameter names alone do not identify the removed Foundry contract. Linked
templates are not fetched or inspected by this validation.

Only generated system ACR and AppInsights connections with their expected name,
target, project identity, and resource ID wiring are exempt. Other `ContainerRegistry` connections,
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
declared model deployments, system ACR and explicitly enabled monitoring
connections, and any required registry or monitoring resources. Endpoint-only setup cannot eject infrastructure; rerun
`project add` with the full project resource ID.

When an agent needs a registry, ejection preserves the registry state selected
during initialization: it creates a registry when none exists, connects an
existing registry when needed, or references an existing project connection
without managing it. Terraform registry output is always named
`container-registry.tf`; Bicep uses `modules/container-registry.bicep`.
Greenfield ejection persists `AZD_FOUNDRY_ACR_MODE` in the azd environment.
On-disk Foundry initialization also records the mode for previously ejected
projects, preserving external registry reuse during provisioning and `azd down`.
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
