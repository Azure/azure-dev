# Foundry Projects

Manage Microsoft Foundry Project resources from your terminal. (Preview)

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
