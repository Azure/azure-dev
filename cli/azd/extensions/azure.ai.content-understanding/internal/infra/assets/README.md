# Azure Content Understanding starter (azd template)

Provision a Microsoft Foundry resource and project, configure the selected azd
environment for Content Understanding, and optionally deploy supported large language models (LLMs)
and embeddings models
supported by the selected Content Understanding API version. The model
deployments enable prebuilt analyzers such as `prebuilt-invoice` and custom
analyzers. The template also configures Content Understanding defaults that map
model names to deployment names. It is purpose-built as a launchpad for
**Azure Content Understanding in Foundry Tools** workflows and the
`azd ai cu` extension.

```sh
azd auth login
azd ai cu infra generate --output-dir provision --environment dev --models none
cd provision
azd up
```

The generated project contains infrastructure and postprovision hooks, not an
application. `--models none` skips optional model deployments; use
`--models recommended` or an interactive selection for generative AI workflows.

## Permissions required for `azd up`

This template deploys at subscription scope. For the complete new-resource
path, the identity running `azd up` needs **Contributor** or **Owner** on the
selected subscription, or a custom role with equivalent permissions. This
allows the deployment to create the resource group, Foundry resource and
project, and selected model deployments.

Contributor cannot create Azure role assignments. If the deployment should
also assign the generated data-plane role, the identity additionally needs
**Role Based Access Control Administrator**, **User Access Administrator**, or
**Owner** on the subscription. If you have Contributor only, omit
`--assign-roles` when generating the project; the post-provision hook requires an existing
data-plane role assignment. It never retrieves or persists a resource key.
Provisioning and postprovision setup use azd / Entra ID authentication.
`CU_API_KEY` applies only to separate CU data-plane commands, not to `azd up`.
Role assignment defaults to `false`; pass `--assign-roles` when generating the
project.

With Entra authentication, `azd ai cu` also requires **Cognitive Services User** on
the Microsoft Foundry resource to configure defaults and create, manage, and run
analyzers. This is data-plane access and is not included in Owner or
Contributor.

## What gets provisioned

| Resource | Purpose |
| --- | --- |
| Resource group `rg-<env>` | Container for everything |
| `Microsoft.CognitiveServices/accounts` (kind `AIServices`) | Microsoft Foundry resource that exposes Content Understanding and other Foundry Tools through one endpoint |
| `Microsoft.CognitiveServices/accounts/projects` | Foundry project (defaults to `proj-<env>`) |
| `Microsoft.CognitiveServices/accounts/deployments` | Models selected from the live Content Understanding and Microsoft Foundry resource catalogs, or none |
| Optional role assignment on the calling identity | `Cognitive Services User`, only when role assignment is enabled for a new resource |

The resource endpoint is `https://<resource-name>.services.ai.azure.com/` — the same
host that serves `/contentunderstanding/...`, `/openai/...`, and the Foundry
project API.

Generate with `--foundry-endpoint` to skip resource and project creation and
perform the same live discovery against that existing resource.

## Microsoft Foundry resource naming

The Microsoft Foundry resource name must be globally unique because it becomes part of a
public DNS hostname:

`https://<resource-name>.services.ai.azure.com/`

By default, this template uses `aif-<unique-suffix>`. To make the name more
meaningful, pass `--foundry-prefix` during generation and azd will construct:

`<prefix>-<unique-suffix>`

Example:

```sh
azd ai cu infra generate --foundry-prefix my-cu --output-dir provision
cd provision
azd up
```

This produces resource names following the pattern `my-cu-<unique-suffix>`.
Generation saves resource choices in the selected azd environment.
`infra/main.parameters.json` uses environment substitutions, so dev and prod
can target different resources in this directory. Regenerate for the selected
environment or use `azd env set <name> <value> -e <environment>` to change inputs:

| Provisioning input | Purpose |
| --- | --- |
| `CU_FOUNDRY_RESOURCE_PREFIX` | Optional name prefix for a new resource |
| `CU_FOUNDRY_EXISTING_ENDPOINT` | Existing resource endpoint; empty for a new resource |
| `CU_FOUNDRY_EXISTING_RESOURCE_GROUP` | Existing resource's group; empty for a new resource |
| `CU_ASSIGN_ROLES` | `true` or `false` for role assignments on a new resource |

These are saved azd deployment inputs, not runtime settings or deployment outputs.
They are not automatically exported into the parent shell.
Regeneration preserves other parameter fields. Older projects using the saved
names `FOUNDRY_RESOURCE_PREFIX`, `FOUNDRY_EXISTING_ENDPOINT`,
`FOUNDRY_EXISTING_RESOURCE_GROUP`, and `AZD_ASSIGN_ROLES` still work without
regeneration. Regeneration updates their parameter references and copies old
values to the new keys in other existing environments only when the new key is
absent. A present new key wins, including an empty value; missing inputs use the
parameter defaults. Old keys are retained but no longer referenced.
The selected environment uses the current generation choices.
For older shared literal resource
parameters, it first saves the effective old values in other existing environments
before applying new choices to the selected environment, including with `--force`.
It cannot recover values already overwritten by an earlier generation.
Unsupported custom resource-parameter expressions or unsafe environment files
fail before any files are written; review the parameters or use a new directory.

## Live model setup

The first infrastructure deployment intentionally creates the Foundry resource
with an empty `infra/models.json`. The post-provision hook then:

1. Calls `GET prebuilt-document` with `CU_API_VERSION`.
2. Reads `supportedModels.completion` and `supportedModels.embedding`.
3. Uses the signed-in azd identity to list deployable models for the provisioned resource.
4. Shows only model versions present in both live catalogs.
5. Deploys the selection and writes it to `infra/models.json`, making subsequent
   Bicep runs repeatable.

For setup without model deployments, generate the project with `--models none`.
In that mode, `prebuilt-digitalParse`,
`prebuilt-read`, and `prebuilt-layout` are available without language or
embeddings model deployments.

The generated hook runs a versioned internal postprovision contract through
`azd ai cu`, so keep azd and the Azure Content Understanding extension installed
when running `azd up`. The default `prompt` selection requires an interactive
terminal. For unattended runs, save `recommended`, `none`, or comma-separated
`model@version` selectors with `azd env config set cu.modelSelection <selection>`.
Model setup failures
are reported without blocking azd environment configuration. Resolve the model
deployment issue and rerun `azd up`; `prebuilt-digitalParse`, `prebuilt-read`,
and `prebuilt-layout` remain available without language or embeddings models.

Successful model setup is recorded in the selected environment's
`cu.modelSetupComplete` configuration setting. Generation saves `--models` in
`cu.modelSelection` and the `--force` choice in the boolean
`cu.forceConfigurationSetup`; neither requires a dedicated environment variable.
Regeneration preserves the completion marker and other existing config.

Generated projects use the `_postprovision-v1` contract. Check extension release
notes before upgrading and regenerate the project if a later release does not
retain that contract.

## Auto-configuring the azd environment

After live model setup, the post-provision hook saves `CU_ENDPOINT`, sets
`CU_DEFAULT_ANALYZER` to `prebuilt-layout`, and saves available model mappings
under `cu.modelDeployments`. Inspect the selected environment and check readiness:

```sh
azd env get-values
azd env config get cu
azd ai cu doctor
```

Download the public sample invoice for a first check that needs no language or
embeddings model:

```sh
curl --fail --location --output invoice.pdf \
  https://raw.githubusercontent.com/Azure-Samples/azure-ai-content-understanding-assets/main/document/invoice.pdf
azd ai cu analyze --file invoice.pdf --analyzer prebuilt-layout
```

When model deployments and Content Understanding defaults are ready, generate a
schema, review and edit its fields and model mappings, then validate and create
a custom analyzer:

```sh
azd ai cu analyzer schema create --name invoice_v1 --from-sample invoice.pdf --output-file invoice-schema.json

# Review and edit invoice-schema.json before continuing.
azd ai cu analyzer validate --schema invoice-schema.json --spec --strict
azd ai cu analyzer create --name invoice_v1 --schema invoice-schema.json
azd ai cu analyze --file invoice.pdf --analyzer invoice_v1
```

Custom analyzer IDs use letters, numbers, or underscores, not hyphens.
If CU configuration already exists, the hook preserves it. Regenerate the project
with `azd ai cu infra generate --force` only when you intend to replace managed
files and configuration.

The post-provision hook only prints the custom-analyzer workflow after it
verifies succeeded chat completion and embeddings model deployments and
configures Content Understanding defaults. Otherwise it prints the specific
repair action and retains the `prebuilt-layout` sanity check.

Configuration setup is automatic. To opt out, set
`CU_DISABLE_AUTO_CONFIG=true` before `azd up`. This does not disable live
model setup; use `azd env config set cu.modelSelection none` to skip model deployments.
The provisioned endpoint remains available as `_CU_PROVISIONED_ENDPOINT`, but
with automatic configuration disabled you must configure `CU_ENDPOINT` yourself
or pass `--endpoint` to CU commands.

## Cleanup

```sh
azd down --purge
```

For resources created by this project, `--purge` removes soft-deleted resources
so their names can be reused. Review the deployment and selected model resources
before cleanup, especially when the project uses an existing account.

## Outputs

After `azd up`, `azd env get-values` exposes these Bicep outputs. The
`_CU_PROVISIONED_*` prefix marks deployment results, not user settings:

| Output | Notes |
| --- | --- |
| `_CU_PROVISIONED_LOCATION` | Deployment location |
| `_CU_PROVISIONED_RESOURCE_GROUP` | Created or existing resource group containing the account |
| `_CU_PROVISIONED_SUBSCRIPTION_ID` | Subscription containing the resource |
| `_CU_PROVISIONED_TENANT_ID` | Resource tenant; postprovision looks up the user's access tenant separately |
| `_CU_PROVISIONED_PROJECT_ENDPOINT` | Project-scoped URL when a Foundry project is available |
| `_CU_PROVISIONED_RESOURCE_NAME` | Created or adopted account name (also the custom subdomain) |
| `_CU_PROVISIONED_PROJECT_NAME` | Project name when available |
| `_CU_PROVISIONED_ENDPOINT` | Resource endpoint used by postprovision setup, not a runtime endpoint alias |
| `_CU_PROVISIONED_MODEL_DEPLOYMENTS` | Models declared in the Bicep deployment; subsequent hook selections are saved to `infra/models.json` |

The hook separately saves runtime `CU_ENDPOINT` and
`CU_DEFAULT_ANALYZER=prebuilt-layout`, unless existing CU configuration is
preserved or automatic configuration is disabled. CU commands do not treat
`_CU_PROVISIONED_ENDPOINT` as a user-supplied endpoint.

Generation also saves `CU_API_VERSION` and standard Azure scope values in the
azd environment. These are not Bicep outputs. Use `azd env get-values` to inspect
`CU_DEFAULT_ANALYZER`, and `azd env config get cu` for postprovision choices,
`modelDeployments`, and the `modelSetupComplete`
marker set by successful setup. Hooks update only the selected azd environment,
never CU user defaults in global azd configuration.

Existing projects remain supported. When new config fields are absent, the hook
reads saved `CU_MODEL_SELECTION` and `CU_CONFIG_SETUP_FORCE` values. It reads
legacy endpoint and scope values only when the corresponding
`_CU_PROVISIONED_*` outputs are absent. For the account name, it falls back to
`CU_RESOURCE_NAME`, then `FOUNDRY_RESOURCE_NAME`, only when the newer name is
absent. Empty required new outputs fail instead of selecting stale legacy
values. Old environment entries are preserved during regeneration. Templates
are also preserved unless `--force` is supplied.

## Layout

```text
.
├── azure.yaml           # azd project manifest + postprovision hook wiring
├── infra/
│   ├── main.bicep              # subscription-scope entrypoint
│   ├── main.parameters.json    # azd → Bicep parameter glue
│   └── modules/
│       └── foundry.bicep       # account + project + persisted model deployments + RBAC
└── hooks/
    ├── postprovision.ps1       # Windows live model setup + azd environment config
    └── postprovision.sh        # POSIX live model setup + azd environment config
```

## More information

- [Content Understanding documentation](https://learn.microsoft.com/azure/ai-services/content-understanding/)
- [Prebuilt analyzers](https://learn.microsoft.com/azure/ai-services/content-understanding/concepts/prebuilt-analyzers)
- [Azure Developer CLI documentation](https://learn.microsoft.com/azure/developer/azure-developer-cli/)
