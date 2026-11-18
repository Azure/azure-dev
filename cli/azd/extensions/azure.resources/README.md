# Azure Resources extension (Preview)

Add Azure resources and application hosting to your azd project interactively,
or declare them in `azure.yaml`.

## Get started

You need azd 1.38.0 or later, an Azure subscription, and Bicep. Install Bicep
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
azd resources add --layer data --environment dev
```

Follow the prompts to choose and configure a resource or application host.

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

```sh
azd resources add
azd resources add --layer application-resources
```

Without `--layer`, you are prompted for a layer name (default `azureresources`).
Updating an existing resources layer, or replacing a layer that uses another
provider, requires confirmation; both confirmations default to No. Other layers
and configuration are preserved.

After saving, the command summarizes your changes and offers to apply them,
preview them first, or leave deployment for later. Preview reviews only the
selected layer. Adding a host can then provision and deploy the whole project;
resource-only additions provision just the selected layer.

The `--environment` flag (or `AZD_ENVIRONMENT`) selects the environment used for
resource discovery, saved settings, and deployment. Canceling before saving leaves
your project and environment unchanged. Declining deployment after saving keeps
your changes for later.

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
  except application hosts and Azure OpenAI models.
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
- `azd resources add` requires interactive input and does not support
  `--no-prompt`. Use saved configuration with `azd provision` for automation.
- Use `azd resources add`, not `azd add`, for these declarations.
  `azd infra gen` does not support them.
- Before using `azd resources add`, move flat `infra` settings into named
  `infra.layers` entries. Top-level project `layers` are not supported by this
  command. Existing top-level `resources` declarations remain supported by
  azd separately; see the [migration guide](./migration.md) before moving them.
- Keep layer names stable after deployment: renaming a layer changes Azure
  resource names and creates a new deployment.
- Saving changes may reformat `azure.yaml` and remove comments. Review your
  changes in source control before deploying.
