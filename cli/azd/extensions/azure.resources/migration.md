# Migrate resource declarations

Migration is optional: legacy top-level `resources` declarations remain supported
by azd core. The extension is a separate, opt-in provisioning provider.

Do not migrate PostgreSQL (`db.postgres`) or MySQL (`db.mysql`) declarations to this
preview extension. Both managed and existing declarations of those types are
unsupported; keep them in separately authored native Bicep infrastructure.

Move top-level `resources` into a named `infra.layers` entry's `config`.
Resource-to-resource `uses` references must stay within the same entry.
Core service `uses` references do not resolve extension-owned resources; review
those dependencies and use infrastructure layer dependencies where appropriate.
Core `azd add` and `azd infra gen` do not support the extension's configuration.
After migrating, use `azd resources add --layer <name>` to add resources to the
selected entry interactively.

Before:

```yaml
resources:
  cache:
    type: db.redis
```

After:

```yaml
infra:
  layers:
    - name: resources
      provider: resources
      config:
        cache:
          type: db.redis
```

Remove the old `resources` key. If `infra.layers` already exists, append the entry.
Move any existing flat `infra` settings into a separate named entry in `infra.layers`.
The [Azure Resources extension](./README.md) must be installed to provision it.
Layer names affect generated Azure resource names: preview changes before provisioning
an existing environment; this YAML edit does not migrate deployed Azure resources.

The extension's provider-owned environment keys are entry-scoped, unlike the
legacy generator's keys. For entry `resources`, for example, copy an existing
`AZURE_RESOURCE_STORAGE_ID` binding to
`AZURE_RESOURCES_RESOURCES__RESOURCE_STORAGE_ID`. Update custom output references as described in the
[environment-key contract](./README.md#environment-keys).
Standard `SERVICE_<SERVICE>_*` host-binding keys do not change.
