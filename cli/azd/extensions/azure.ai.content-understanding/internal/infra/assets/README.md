# Azure Content Understanding starter (azd template)

Provision a Microsoft Foundry resource and project, configure the selected azd
environment for Content Understanding, and optionally deploy supported large language models (LLMs)
and embeddings models
supported by the selected Content Understanding API version. The model
deployments enable prebuilt analyzers such as `prebuilt-invoice` and custom
analyzers. The template also configures Content Understanding defaults that map
model names to deployment names. It is purpose-built as a launchpad for
**Azure Content Understanding in Foundry Tools** workflows and the
native Go `azd ai cu` extension.

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
**Owner** on the subscription. If you have Contributor only, set
`AZD_ASSIGN_ROLES` to `false`; the post-provision hook then requires an existing
data-plane role assignment. It never retrieves or persists a resource key.
The native extension is Entra-only, including projectless commands. Role assignment defaults to
`false`; pass `--assign-roles`
when generating the project.

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

If `FOUNDRY_EXISTING_ENDPOINT` is set, this template skips resource and project
creation and performs the same live discovery against that existing resource.

## Microsoft Foundry resource naming

The Microsoft Foundry resource name must be globally unique because it becomes part of a
public DNS hostname:

`https://<resource-name>.services.ai.azure.com/`

By default, this template uses `aif-<unique-suffix>`. To make the name more
meaningful, set an optional prefix and azd will construct:

`<prefix>-<unique-suffix>`

Example:

```sh
azd env set FOUNDRY_RESOURCE_PREFIX my-cu
azd up
```

This produces resource names following the pattern `my-cu-<unique-suffix>`.

## Live model setup

The first infrastructure deployment intentionally creates the Foundry resource
with an empty `infra/models.json`. The post-provision hook then:

1. Calls `GET prebuilt-document` with `CU_API_VERSION`.
2. Reads `supportedModels.completion` and `supportedModels.embedding`.
3. Uses the signed-in azd identity to list deployable models for the provisioned resource.
4. Shows only model versions present in both live catalogs.
5. Deploys the selection and writes it to `infra/models.json`, making subsequent
   Bicep runs repeatable.

Decline every model confirmation to deploy no models. In that mode, `prebuilt-digitalParse`,
`prebuilt-read`, and `prebuilt-layout` are available without language or
embeddings model deployments.
Set `CU_MODEL_SELECTION=recommended` for noninteractive selection or
`CU_MODEL_SELECTION=none` for deterministic setup without model deployments.

The generated hook runs a versioned internal postprovision contract through
`azd ai cu`, so keep azd and the Azure Content Understanding extension installed
when running `azd up`. A saved `prompt` selection
requires an interactive terminal; set `CU_MODEL_SELECTION=recommended`, `none`,
or explicit `model@version` selectors for unattended runs. Model setup failures
are reported without blocking azd environment configuration. Resolve the model
deployment issue and rerun `azd up`; `prebuilt-digitalParse`, `prebuilt-read`,
and `prebuilt-layout` remain available without language or embeddings models.

Successful model setup is recorded in the selected environment's
`cu.modelSetupComplete` configuration setting. The legacy
`CU_MODEL_SETUP_COMPLETE` environment value is not the completion marker used
by this extension.

Generated projects use the `_postprovision-v1` contract. Check extension release
notes before upgrading and regenerate the project if a later release does not
retain that contract. The hook does not require Python or the standalone `cu` CLI.

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
model setup; use `azd env set CU_MODEL_SELECTION none` to skip model deployments.

## Cleanup

```sh
azd down --purge
```

For resources created by this project, `--purge` removes soft-deleted resources
so their names can be reused. Review the deployment and selected model resources
before cleanup, especially when the project uses an existing account.

## Outputs

After `azd up`, `azd env get-values` exposes:

| Variable | Notes |
| --- | --- |
| `FOUNDRY_ENDPOINT` | `https://<resource-name>.services.ai.azure.com/` |
| `FOUNDRY_PROJECT_ENDPOINT` | Project-scoped URL when this project creates a Foundry project |
| `FOUNDRY_RESOURCE_NAME` | Account name (also the custom subdomain) |
| `FOUNDRY_PROJECT_NAME` | Project name when created by this deployment |
| `CU_ENDPOINT` | Resource endpoint used by CU commands; those commands do not fall back to `FOUNDRY_ENDPOINT` |
| `CU_DEFAULT_ANALYZER` | Saved by the hook as `prebuilt-layout`, unless existing CU configuration is preserved |
| `MODEL_DEPLOYMENTS` | Models declared in the Bicep deployment; subsequent hook selections are saved to `infra/models.json` |

Generation also saves inputs such as `CU_API_VERSION`, `CU_MODEL_SELECTION`,
and any existing-resource selectors in the azd environment. These are not Bicep
outputs. Use `azd env get-values` to inspect `CU_DEFAULT_ANALYZER`, and
`azd env config get cu` for `modelDeployments` and the `modelSetupComplete`
marker set by successful setup. Hooks update only the selected azd environment,
never CU user defaults in global azd configuration.

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
