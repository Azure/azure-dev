# Customize Foundry infrastructure with `--infra`

`azd ai agent init --infra` generates editable infrastructure-as-code for the
`host: azure.ai.project` service in `azure.yaml`.

```bash
# Generate Bicep (default)
azd ai agent init --infra

# Choose the format explicitly
azd ai agent init --infra=bicep
azd ai agent init --infra=terraform
```

Use eject when you need to customize resources beyond the service-level
configuration in `azure.yaml`. Future `azd provision` runs use the generated
files.

## Project layouts

- A new or Foundry-only project uses the root `infra/` layout.
- A project with existing infrastructure keeps that infrastructure as a named
  layer and receives a separate Foundry layer under `infra/foundry`.
- An existing layered project receives a `foundry` layer when one is not
  already declared.
- A compatible existing `foundry` layer uses its declared `path` and `module`.

For example, ejecting into a project with existing Bicep produces:

```yaml
infra:
  layers:
    - name: infra
      path: infra
      provider: bicep
    - name: foundry
      path: infra/foundry
      provider: microsoft.foundry
```

The existing `infra/main.bicep` remains unchanged.

For a service that sets `endpoint:`, Bicep eject keeps the
`microsoft.foundry` provider. The provider compiles the generated `main.bicep`,
so ejected and embedded provisioning use the same resource graph. Terraform
eject uses the built-in `terraform` provider.

## Existing files

Eject never overwrites generated-file collisions.

- When `infra/foundry` exists but no `foundry` layer is declared, unrelated
  files are preserved and generated files are merged only when their paths do
  not collide.
- When a `foundry` layer is already declared, its target must be empty. Edit
  its existing files directly instead of ejecting again.
- Generation and installation are staged so failures do not leave a partial
  generated tree or rewrite `azure.yaml`.

## Resource group ownership

A Foundry layer uses `AZURE_FOUNDRY_RESOURCE_GROUP`, which defaults to:

```text
rg-<environment>-foundry
```

Set a custom group before the first provision:

```bash
azd env set AZURE_FOUNDRY_RESOURCE_GROUP <name>
```

For safety, `azd down` deletes the layer resource group only when azd recorded
that it created the group and the live Azure group is tagged for the current
environment. Otherwise teardown intentionally refuses deletion so a user-owned
group is not removed.

For an existing-project Bicep eject, the account and project resource group are
never created by the generated template. Teardown can remove adjunct resources
and project children created by that template, but does not own or delete the
reused account or project.

## Existing container registries

Eject preserves the registry choice made during init:

- No registry required: no registry resources are managed.
- No existing registry selected: create a registry, `AcrPull` assignment, and
  project connection.
- Existing registry without a project connection: reference the registry and
  create only `AcrPull` plus the project connection.
- Existing project connection selected: reference its registry and connection
  without managing either one.

The generated files never import or take ownership of an existing registry.
When registry work is required, the generated file is consistently named
`modules/container-registry.bicep` or `container-registry.tf`, whether the
registry is created or reused.

## Layer dependencies

The Foundry layer is independent by default. Azd analyzes generated parameter
references such as `${AZURE_VNET_ID}` and orders the Foundry layer after a
Bicep layer that produces the matching output. Add `dependsOn` when a
dependency cannot be inferred from parameter references, such as a value
written by another layer's hook:

```yaml
infra:
  layers:
    - name: network
      path: infra/network
      provider: bicep
    - name: foundry
      path: infra/foundry
      provider: microsoft.foundry
      dependsOn:
        - network
```

Without an inferred or explicit edge, independent layers provision
concurrently and the Foundry layer can read an empty or stale environment
value.

## Limitations

- Terraform eject does not support a service with a private `network:` block;
  use Bicep for private networking.
- Services that set `endpoint:` to reuse an existing project can eject Bicep or Terraform. The
  generated templates reference the existing account and project without taking
  ownership and manage only declared model deployments, connections, and
  adjunct resources such as ACR.
- Eject preserves the existing root infrastructure mapping during migration;
  custom properties remain the project owner's responsibility.
