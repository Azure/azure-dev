# Azure Resources extension (Preview)

Add Azure resources and application hosting to your azd project interactively,
or declare them in `azure.yaml`.

## Get started

You need azd 1.39.0 or later, an Azure subscription, and Bicep. Install Bicep
and make `bicep` available on your `PATH`, or set `AZD_BICEP_TOOL_PATH` to its
executable. The extension does not install Bicep for you.

Install the preview extension package, replacing the example path with your
package's location:

```sh
azd extension install ./azure-resources.zip
```

From a project containing `azure.yaml`, sign in, create an environment if you
don't already have one, and add your first resource:

```sh
azd auth login
azd env new dev
azd add --environment dev
```

Select a resource or application host, such as **(Resources) Cosmos DB for NoSQL**,
then follow the configuration prompts.

## Supported resources

| Resource | Configuration type |
| --- | --- |
| Redis | `db.redis` |
| Azure Cosmos DB for MongoDB | `db.mongo` |
| Azure Cosmos DB for NoSQL | `db.cosmos` |
| Storage account | `storage` |
| Event Hubs | `messaging.eventhubs` |
| Service Bus | `messaging.servicebus` |
| Key Vault | `keyvault` |
| Microsoft Foundry project | `ai.project` |
| Azure OpenAI model | `ai.openai.model` |
| Azure AI Search | `ai.search` |
| Azure Container Apps host | `host.containerapp` |
| App Service host | `host.appservice` |

You can also select existing Azure resources instead of creating new ones;
see [current limitations](#current-limitations) for restrictions.

## Add resources interactively

In `azd add`, select a type in the **Resources** category. To choose
from the Azure Resources wizard's categories instead, launch it directly:

```sh
azd resources add
```

Items that the resources layer already manages and can only contain once stay
visible in `azd add`, but cannot be selected.

Without `--layer`, the command reuses the existing resources layer or defaults
to `azureresources` if there isn't one. Adding to a resources layer doesn't
require confirmation. Replacing a layer that uses another provider still
requires confirmation and defaults to No. Layers do not inherit `infra.provider`.
Other layers are preserved.

After saving, the command summarizes your changes and prints the command to
apply them. Run `azd provision <layer>` for resource-only additions or
`azd up` to provision and deploy the project after adding a host. To preview
changes to the selected layer first, run `azd provision <layer> --preview`.
Adding a resource or host does not launch provisioning or deployment.

The `--environment` flag (or `AZD_ENVIRONMENT`) selects the environment used for
resource discovery and saved settings, and is included in the suggested command.
Canceling before saving leaves your project and environment unchanged.

## Configure resources in azure.yaml

You can edit declarations directly under the selected layer's `config`. For
example, this `data` layer creates a storage account with a blob container and a
Service Bus namespace with a queue:

```yaml
name: example
infra:
  layers:
    - name: data
      provider: resources
      config:
        files:
          type: storage
          containers:
            - uploads
        jobs:
          type: messaging.servicebus
          queues:
            - pending
```

Preview and provision this layer when you are ready:

```sh
azd provision data --preview --environment dev
azd provision data --environment dev
```

Use `azd up --environment dev` to provision infrastructure and deploy your
project's application services.

For Foundry models, you are prompted for a location with sufficient quota. Your
choice is saved in the environment and reused on later deployments. Deployments
use your selected environment's cloud configuration, falling back to project
settings, user settings, then Azure Public Cloud.

## Export infrastructure to Bicep

Run the core generation command to take ownership of the infrastructure:

```sh
azd infra generate
```

The extension generates the same Bicep sources used for provisioning, without
contacting Azure or initializing its provisioning provider. By default, sources
are written to `infra/<layer-name>/`; an explicitly configured layer `path`
is used instead and must stay within the project.

The extension writes the files and returns a replacement provider entry.
After generation succeeds, core replaces the original named
infrastructure entry in place with `provider: bicep`, the output
`path`, and `module: main`. Its name, position, hooks, and dependencies are
preserved, as are other layers, services, and unrelated YAML comments.
The resource `config` is removed. Future provisioning uses the exported files
instead of regenerating them from resource declarations.

For a managed Foundry project, the exported parameter file references the
entry-scoped AI deployment location saved by the resources provider. If no
location has been saved, Bicep provisioning prompts for the parameter rather
than using the resources provider's quota-aware location selection.

If generated files already exist, this extension refuses to overwrite them.
Use `azd infra generate --force` to allow overwriting. A file
write failure prevents configuration conversion. If configuration conversion fails after
writing, the command reports the failure and retains the generated files.
Changes to the selected entry during generation are rejected rather than overwritten.

Infrastructure generation and the `azd add` menu use the beta
`infra-generation-provider` and `add-provider` capabilities. For local
development, the extension uses the SDK from this checkout. Before publishing
the extension, remove the local replacement and pin a published SDK with both APIs.

## Delete resources safely

Removing a resource from `azure.yaml` does **not** delete it from Azure.

`azd down` deletes deployed resource groups and **all resources inside them**,
including resources added by other layers or tools. `azd down <layer>` selects
that layer's deployment, not a subset of a shared resource group. Use separate
resource groups when layers need independent teardown. Deletion confirmation
defaults to No; `--force` skips it.

## Environment keys

Resource settings, including existing-resource IDs, are saved in the selected
azd environment as `AZURE_RESOURCES_<LAYER>__<KEY>`. The layer name is uppercase with
hyphens replaced by underscores, and the key omits the `AZURE_` prefix. For
example, layer `data` produces `AZURE_RESOURCES_DATA__RESOURCE_STORAGE_ID`,
`AZURE_RESOURCES_DATA__KEY_VAULT_NAME`, and
`AZURE_RESOURCES_DATA__AI_DEPLOYMENTS_LOCATION`.

Application hosting settings use `SERVICE_<SERVICE>_*` keys. Service names must
be unique across the project.

Environments created with earlier preview builds used unscoped keys. Copy
existing-resource ID bindings to the layer-scoped keys and update any custom
references before provisioning, then provision each layer again. See the
[migration guide](./migration.md) before moving existing declarations.

## Current limitations

- Only one `resources` infrastructure entry is supported per project. Other
  infrastructure layers can use Bicep, Terraform, or other providers.
- For newly created resources, only one of each resource type is supported,
  except application hosts and Azure OpenAI models. To add hubs, queues, or
  blob containers to a resource you already manage in the layer, edit its
  `hubs`, `queues`, or `containers` list in `azure.yaml`.
  If you select a duplicate in `azd add`, press Enter after the error to
  return to the menu and choose something else.
- PostgreSQL (`db.postgres`) and MySQL (`db.mysql`) are not supported, including
  existing databases. Manage these through separately authored infrastructure.
- App Service hosts support Python and Node.js. Both host types require a port
  from 1 to 65535; App Service also requires a runtime stack and version.
- Only hosts can declare resource links with `uses`, and linked resources must
  be in the same infrastructure layer. Service-level `uses` does not link to
  resources declared by this extension.
- Existing resources cannot be application hosts or include deployment options
  such as containers, queues, or model settings. Linking a host to an existing
  Redis or MongoDB resource is not supported. Selecting an existing resource
  references it rather than creating it; the teardown warning above still
  applies to anything in a resource group being deleted.
- Resource options are type-specific; unsupported or unknown properties are
  rejected. Use the interactive command to configure supported options.
- The wizard requires interactive input and does not support `--no-prompt`,
  whether launched from `azd add` or `azd resources add`. Use saved configuration
  with `azd provision` for automation.
- Infrastructure generation supports flat `infra` declarations as well as
  named `infra.layers` and `layers[].infra` entries.
- Before using the wizard, move flat `infra` settings into named
  `infra.layers` entries. Top-level project `layers` are not supported by this
  command. Existing top-level `resources` declarations remain supported by
  azd separately; see the [migration guide](./migration.md) before moving them.
- Keep layer names stable after deployment: renaming a layer changes Azure
  resource names and creates a new deployment.
- Saving changes may reformat `azure.yaml` and remove comments. Review your
  changes in source control before deploying.
