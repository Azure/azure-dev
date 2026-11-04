# Azure Resources extension (Preview)

`azure.resources` provides the `resources` infrastructure provider. It reads
resource declarations from `azure.yaml`, generates Bicep for them, and provisions
them to Azure.

```yaml
name: example
infra:
  layers:
    - name: data
      provider: resources
      config:
        cache:
          type: db.redis
```

Install the extension, then run `azd resources add` or write the provider
configuration directly in `azure.yaml`. Use `azd provision` or `azd up` to deploy.
Core `azd add` and `azd infra gen` do not support this provider's configuration.
The existing top-level `resources` format and its core commands are unaffected and
do not use this extension.

## Add resources interactively

```sh
azd resources add
azd resources add --layer application-resources
```

`azd resources add` configures Redis, MongoDB, Cosmos DB, storage, messaging, AI models,
Key Vault, application hosts, and existing Azure resources. Resource declarations
are saved to `infra.layers[].config`, and service configuration to `services`.
Resources can only link to other resources in the same infrastructure layer.

Without `--layer`, you are prompted for a layer name (default `azureresources`).
Updating an existing resources layer, or replacing a layer that uses another
provider, requires confirmation; both confirmations default to No. Other layers
and configuration are preserved.

Only one `resources` layer is supported per project. Adding a second one is
rejected, as is provisioning a project that declares more than one.

When the command finishes, changes are saved and a short summary of the added or
updated layer, resources, and services is shown. Canceling before then leaves
the project and environment unchanged. You can then provision now, preview first,
or apply the changes later. When you add a host, the follow-up runs `azd up` for
the whole project, so the preview covers all infrastructure layers; resource-only
additions preview and provision just the selected layer.

The `--environment` flag (or `AZD_ENVIRONMENT`) selects the environment used for
resource discovery, saved values, and follow-up commands.

The command requires interactive input and rejects `--no-prompt`. Existing flat
`infra` configuration must first be moved into `infra.layers`; top-level project
`layers` are not supported by this command.

Saving through azd may reformat `azure.yaml` and remove comments.

## Provisioning and teardown

- Deployments are incremental: removing a resource from configuration does not
  delete it from Azure.
- `azd provision --preview` shows the changes Azure Resource Manager would make.
- `azd down` deletes the resource groups recorded by the deployment and **all
  resources inside them**, including resources added by other layers or tools.
  `azd down <layer>` selects that layer's deployment, not a subset of a shared
  resource group. Use separate resource groups when layers need independent
  teardown. Confirmation defaults to No; `--force` skips it.
- For Foundry AI models, you're prompted once for a location with sufficient
  quota. The choice is saved in the environment and reused, including with
  `--no-prompt`.
- Layer names affect generated resource and resource group names. Keep them
  stable for existing deployments; renaming a layer creates a new deployment.
- The selected environment's `cloud` configuration is used (falling back to the
  project, then user configuration, then Azure Public Cloud).

## Environment keys

Provider outputs and existing-resource bindings are saved as
`AZURE_RESOURCES_<LAYER>__<KEY>`. The layer name is converted to uppercase with
hyphens replaced by underscores, and the key omits the `AZURE_` prefix. For
example, layer `data` produces `AZURE_RESOURCES_DATA__RESOURCE_STORAGE_ID`,
`AZURE_RESOURCES_DATA__KEY_VAULT_NAME`, and
`AZURE_RESOURCES_DATA__AI_DEPLOYMENTS_LOCATION`.

Standard `SERVICE_<SERVICE>_*` outputs are unchanged, so service names must be
unique across the project.

Environments created with earlier preview builds used unscoped keys. Copy
existing-resource ID bindings to the layer-scoped keys and update any custom
references before provisioning, then provision each layer again. See the
[migration guide](./migration.md) before moving existing declarations.

## Requirements

A Bicep executable is required. Set `AZD_BICEP_TOOL_PATH` to choose one;
otherwise the extension uses `<AZD_CONFIG_DIR>/bin/bicep` (default
`~/.azd/bin/bicep`), then `bicep` on `PATH` (`bicep.exe` on Windows). The
extension does not download Bicep.

## Development

```sh
cd cli/azd
go build
cd extensions/azure.resources
go build
go test ./...
```

To install a local build:

```sh
azd x build --skip-install
azd x pack --bundle --output ./bin/azure-resources-dev.zip
azd extension install ./bin/azure-resources-dev.zip --force
```
