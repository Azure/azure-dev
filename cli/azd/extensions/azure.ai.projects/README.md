# Foundry Projects

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

For source-built hosted agents in a new Foundry project, select an existing
Azure Container Registry by its full ARM resource ID:

```sh
azd ai project add --new-project --acr-resource-id "/subscriptions/<subscription-id>/resourceGroups/<external-resource-group>/providers/Microsoft.ContainerRegistry/registries/<registry-name>"
```

This is supported with Bicep only. The registry must be in the Foundry project's
subscription (`AZURE_SUBSCRIPTION_ID`) and outside the Foundry resource group
because `azd down` deletes that group. Cross-subscription registry reuse is not
supported. You are responsible for registry network connectivity and appropriate
image push permissions.
Embedded and currently generated ejected Bicep keep the
`microsoft.foundry` provider, which enforces the switch for provision, preview,
and down. **Manually changing `infra.provider` to core `bicep` is unsupported**:
its preview skips extension lifecycle hooks, so it is outside this enforcement
contract. Arbitrary direct Bicep/ARM execution is also outside the contract.
Do not change the provider to bypass the switch.
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
For a partial deployment, a project connection alone does not establish registry
ownership: recovery requires a successful registry creation operation in that
environment's deployment history. Missing or ambiguous evidence stops teardown
before any resources or access grants are deleted.

Before provisioning, the flag overrides environment defaults and saves
`AZURE_CONTAINER_REGISTRY_RESOURCE_ID` with `AZD_FOUNDRY_ACR_MODE=reuse-connect`.
You can select a registry before adding agents; the selection remains pending
until a source-built agent requires it, including across project-only provisioning
and teardown. Configured project or account names do not discard an unused
selection. Source initialization validates environment-only selections against
the root or Foundry-layer resource group retained by setup before changing files
or environment values.
Interactive setup validates the selection against the final chosen subscription
before saving project services or environment values. An explicit empty value,
an existing project selector, or a manifest containing only code, prebuilt-image, or
non-container agents is rejected. Without a selector, source builds continue
to create a registry automatically.

Once a project has a deployed registry binding, registry migration is not
supported. Repeating setup with the same selection preserves the deployed
project identity and ownership even if legacy project and registry ID outputs
are absent. Bicep eject uses the binding's recorded registry ID when no registry
environment-variable reference is available. Use a new azd environment to choose
a different registry; do not clear internal mode or ownership state to bypass
this check.

Provisioning preview checks existing pull access using the same read-only access
plan as deployment. It does not create access grants or persist pending access
operations.

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
