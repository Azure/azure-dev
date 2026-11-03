# Azure Resources extension (Preview)

`azure.resources` provides the `resources` infrastructure provider. It interprets
resource declarations in `azure.yaml`, generates Bicep, and provisions the
generated subscription-scope deployment through Azure Resource Manager.

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

Install the extension and run `azd resources add`, or author its provider
configuration directly in `azure.yaml`, then use `azd provision` or `azd up`.
The extension owns configuration decoding, validation, and Bicep generation;
azd passes the provider's `config` through without interpreting resource definitions.
Core `azd add` and `azd infra gen` integration for this provider is deferred;
the extension's own `azd resources add` command manages its configuration.
The existing top-level `resources` format and its core commands remain supported
independently; they do not use this extension.

## Add resources interactively

```sh
azd resources add
azd resources add --layer application-resources
```

The wizard configures databases, storage, messaging, AI models, Key Vault,
application hosts, and existing Azure resources. It writes resource declarations
only to `infra.layers[].config`, with service configuration under `services`.
Resource links are local to the selected infrastructure entry.

Without `--layer`, enter a layer name (defaulting to the existing resources layer,
or `default-resources` for a new project). An explicit
`--layer` takes precedence. An existing resources layer is updated only after
confirmation; choosing a layer owned by another provider requires confirmation
to replace that entry. Both confirmations default to No. Unrelated layers and
configuration are preserved.
Only one resources infrastructure entry is supported. Adding a second is rejected
before Azure context selection or project changes, including entries that inherit
`infra.provider`. Initialization also rejects hand-authored projects with multiple
resources entries.

After the wizard completes, changes are saved automatically without displaying
the full YAML or asking for final confirmation. A short summary lists the layer,
resources, and services added or updated. Canceling during configuration leaves
the project and environment unchanged. After saving, choose whether to provision,
preview first, or apply the changes later.
When adding a host, preview covers all infrastructure layers because the follow-up
`azd up` provisions and deploys the whole project. Resource-only additions preview
and provision the selected layer. The selected `--environment` or `AZD_ENVIRONMENT`
is used for resource discovery, environment values, and follow-up commands.
The wizard requires interactive input and rejects `--no-prompt`.
Existing flat infrastructure must first be moved into `infra.layers`; top-level
project `layers` authoring is not supported by this command.

## Development

Build the host and extension together: the extension uses the preview provisioning
contract and the checked-in local SDK replacement.

```sh
cd cli/azd
go build
cd extensions/azure.resources
go build
go test ./...
```

The local `build.sh` and `build.ps1` scripts populate version, source commit, and
UTC build date metadata, following the Go extension scaffold. `EXTENSION_VERSION`
overrides the version (default: `version.txt`), `EXTENSION_PLATFORM` selects one
target such as `linux/arm64` (default: all six supported targets), and `OUTPUT_DIR`
sets the output directory (default: `bin` beside the script). CI populates the
same three metadata fields through the shared build script.

For local installation, build and package with the developer extension, then
install the bundle so both the commands and provider are registered:

```sh
azd x build --skip-install
azd x pack --bundle --output ./bin/azure-resources-dev.zip
azd extension install ./bin/azure-resources-dev.zip --force
```

A Bicep executable is required at runtime for compilation.
Azure credentials and environment access are requested through the azd SDK.
ARM and Key Vault clients use the selected environment's cloud configuration,
falling back to project and user configuration, then Azure Public Cloud.
Tests use local fixtures and mocks rather than deploying Azure resources.

### Project editing and the azd API gap

The add wizard owns resource choices, dependencies, provider configuration, validation,
and replacement confirmation. It does not expose generic YAML operations.

| Project operation | Implementation |
|---|---|
| Add or update a service | Existing `Project.SetConfigSection("services.<name>", ...)` RPC; unchanged services are skipped |
| Save environment values | Existing `Environment.SetValue` RPC |
| Read project configuration | Existing `Project.GetConfigValue` RPC |
| Add, replace, or update an infrastructure layer | Select the layer by name in memory, then use `Project.SetConfigValue("infra.layers", ...)` |

All project-file parsing, serialization, validation, and persistence belong to the
azd host. The extension has no custom YAML editor, file lock, or stale-file guard.
Its remaining layer logic only chooses the entry and builds the resources provider's
configuration. The raw-section RPC is used for services instead of translating the
wizard's authored service mappings into the separate typed `AddService` model.

The existing RPCs can represent all structures this command writes. Named-layer
mutation is not exposed directly, so the command reads and replaces the whole
`infra.layers` array. Remaining core API gaps are comment-preserving saves, a
transaction spanning layer and service changes, and cross-process coordination or
conditional writes covering a read–modify–write sequence. These are not reimplemented
locally. RPC saves may reformat the file and discard comments, and concurrent edits
made after the initial read may be overwritten.

The layer is saved first, followed by changed services and then environment values.
A service failure restores the original infrastructure and services; if restoration
also fails, the error requests manual cleanup. Canceling
the wizard performs no writes; all project write payloads are prepared before the
first mutation.

The manifest targets host version **1.38.0** for this extension's initial release.
The `go.mod` replacement selects this checkout's SDK during development.
Provisioning uses the existing stable stream; it requires no new protocol fields
or isolated-mode acknowledgement.

### Execution subset

This is deliberately not a replacement for the general-purpose Bicep provider:

- It executes only the generated subscription-scoped `main.bicep` and
  `main.parameters.json`, using incremental ARM deployments.
- Preview uses ARM what-if; refresh reads the entry's deployment state. Generation
  itself needs neither Azure access nor Bicep.
- AI deployment location selection uses the model quota requirements in resource
  configuration directly, including an account slot and the combined capacity of
  models sharing a quota pool. A separate resource parameter resolver owns this
  policy; the Bicep executor accepts a generic resolver and does not interpret AI
  configuration. It uses the host's quota-aware location prompt,
  not prompt metadata in generated Bicep. The selection is persisted in the
  entry's `AI_DEPLOYMENTS_LOCATION` environment key and reused by preview,
  provision, and pipeline configuration. Only a missing selection requires
  interactive input; a saved value works with `--no-prompt`.
  Refresh does not prompt for AI deployment location.
- `down` deletes the resource groups recorded by the deployment and **all resources
  inside them**, matching standard Bicep teardown. This includes resources added
  by other layers or tools, linked existing resources inside those groups, and
  resources removed from configuration during incremental deployment. Group
  ownership tags do not restrict teardown. Confirmation defaults to No;
  `--force` skips it. `azd down <layer>` selects the layer's deployment, not an
  isolated subset of a shared group's contents. Use separate resource groups
  when layers need independent teardown. Linked existing resources in other
  groups do not cause those groups to be deleted.
- `--purge` and the `deploymentStacks` provider setting are rejected explicitly.
  Soft-deleted resources are retained. Use separately authored native Bicep
  infrastructure if you need those capabilities.
- Deployment names include the project, environment, and entry identities.
  Deployments from the former in-process provider are discovered using matching
  project/environment/entry tags. Legacy group adoption also requires the recorded
  group and its environment tag, and never overrides another explicit owner.
  Untagged or ambiguously owned infrastructure is not automatically adopted.
  Renaming entries can create a new deployment identity.
- Standard azd project/environment/entry tags are retained on deployments.

### Tool configuration

`AZD_BICEP_TOOL_PATH` selects a Bicep executable. Otherwise the extension checks
`<AZD_CONFIG_DIR>/bin/bicep` (default `~/.azd/bin/bicep`) and then `PATH`, using
`bicep.exe` on Windows. The extension does not download Bicep. Missing or failed
tool execution is reported explicitly.

## Ownership and lifecycle

- The catalog and Bicep assets live in [internal/resources](./internal/resources).
- Existing-resource host bindings are generated directly as Bicep expressions by
  the catalog. They do not use an expression language in `azure.yaml`.
  Linking hosts to existing Redis, PostgreSQL, MySQL, or MongoDB resources remains
  unsupported because their database credentials and connection bindings cannot
  be generated.
- The extension-owned execution subset lives in [internal/bicep](./internal/bicep).
- Provider-side environment setup is isolated in
  [provider_env.go](./internal/bicep/provider_env.go) so shared scope prompting
  can move to the host when it guarantees setup across the relevant flows.
- The stable SDK shares one provider object per provider name for the listen
  session. Only the first infrastructure entry initializes that object; independent
  execution of multiple `resources` entries is deferred with instance isolation,
  so projects with multiple entries are rejected.
- Provider initialization resolves and saves the shared subscription and location,
  matching built-in provider setup before parallel layer steps. It uses host
  account and environment services, but does not compile Bicep or deploy resources.
- Failed initialization releases generated files. Local `Close` releases them
  when the extension's listen session ends; there is no remote instance-close operation.
- Parameter and output strings use the stable protocol's plain-string fields.
  Other JSON-compatible values are represented as JSON text, not typed wire values.
- Generation can return sources without initialization or Azure access.
- The extension uses the azd SDK and Azure SDKs, not the core Bicep provider.

Infrastructure entry names affect generated resource and resource-group naming.
Keep entry names stable when updating existing deployments. Resource definitions
and their `uses` references are local to an entry, so the same resource name can
appear in separate entries. Core service `uses` validation and compose APIs do not
discover resources inside the extension's configuration. See the
[migration guide](./migration.md) before moving existing declarations.

Provider-owned outputs and existing-resource bindings use entry-scoped environment
keys: `AZURE_RESOURCES_<ENTRY>__<KEY>`. The entry is converted to uppercase, with each hyphen
replaced by an underscore (repeated and trailing hyphens are preserved). The key
omits the former `AZURE_` prefix. For example, entry `data` uses
`AZURE_RESOURCES_DATA__RESOURCE_STORAGE_ID` and
`AZURE_RESOURCES_DATA__KEY_VAULT_NAME`. The unnamed entry has an empty entry component.
AI location is saved as `AZURE_RESOURCES_DATA__AI_DEPLOYMENTS_LOCATION`.
Standard `SERVICE_<SERVICE>_*` host-binding outputs remain unchanged; service names
must remain unique across the project. Application environment variables generated
inside each resource group are unchanged.

For environments created with earlier preview builds, copy existing-resource ID
bindings and the Key Vault name into the appropriate entry-scoped keys before
provisioning. Copying the vault name preserves the existing database password
lookup; do not share another entry's vault key. Update custom references to the
old output keys. Provision each entry again to replace its deployment's old output
names before relying on entry-isolated refresh or `down`.

## Release handoff

Before publication, replace the local SDK replacement with a released SDK that
includes any remaining host changes required by this extension, and confirm the minimum host version.
Have the azd core team register `eng/pipelines/release-ext-azure-resources.yml`
in Azure DevOps under `azure-dev/extensions` and grant shared release access.
Create the `ext-azure-resources` issue label. Add registry entries in a follow-up
PR after release artifacts are available.
