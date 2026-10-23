# Azure Developer CLI extension for Content Understanding

This preview extension adds Azure Content Understanding commands under
`azd ai cu`. Analyze documents and media, manage analyzers, and configure model
defaults using Azure Developer CLI authentication and environments.

It is a native Go extension; Python and the standalone `cu` CLI are not required.

## Content Understanding concepts

Content Understanding turns documents, images, audio, and video into structured
output for automation, analytics, and search. It is a Foundry Tool accessed
through a Microsoft Foundry resource in Azure.

- A **file** is the input, such as a document, image, audio file, or video.
- An **analyzer** defines how Content Understanding processes a file and
  extracts content and structured fields.
- An **analyzer result** is the output. It can include Markdown, structured
  fields, and modality-specific details.
- A **prebuilt analyzer** is supplied by the service for common extraction,
  search, and domain scenarios.
- A **custom analyzer** uses a base analyzer and a field schema you define
  for your scenario.

See [What is Content Understanding?](https://learn.microsoft.com/azure/ai-services/content-understanding/overview)
and the [terminology guide](https://learn.microsoft.com/azure/ai-services/content-understanding/glossary)
for more background.

## Install

Requires [Azure Developer CLI](https://aka.ms/azd) 1.37.0 or later.

```bash
azd ext install azure.ai.content-understanding
azd ai cu --help
```

## Connect to Microsoft Foundry and check setup

You need a Microsoft Foundry resource endpoint and an identity with Content
Understanding access.

| Your resource state | Next step |
| --- | --- |
| No Foundry resource yet | [Create a new resource](docs/provisioning.md#create-a-new-microsoft-foundry-resource). |
| A resource exists but generative models or defaults are missing | [Deploy models and configure defaults](docs/provisioning.md#deploy-models-and-configure-defaults). |
| The resource is ready | Sign in and connect below. |

Resource creation, Azure permissions, and model setup are covered in the
separate [Microsoft Foundry provisioning guide](docs/provisioning.md).

For an existing ready resource, start with a session setting. Add persistence
or project environments only when you need them.

### 1. Connect for the current shell session

```bash
azd auth login
export CU_ENDPOINT="https://<resource-name>.services.ai.azure.com/"
azd ai cu doctor
```

These examples use Bash. In PowerShell, set the endpoint with
`$env:CU_ENDPOINT = "https://<resource-name>.services.ai.azure.com/"`.
Normal endpoint-only use does not require a tenant ID; the extension uses azd
authentication. API-key authentication is not supported.

`doctor` reports configuration, authentication, service access, and model
readiness, including effective CU settings and their sources.
Missing generative models can produce `ready: false` while
`prebuilt-layout` remains usable. Inspect the report, not just the exit code.

### 2. Save a user default

To reuse the endpoint in future shell sessions, save a user default. It works
from any directory without `azd init`:

```bash
azd ai cu config set CU_ENDPOINT "https://<resource-name>.services.ai.azure.com/"
azd ai cu config set CU_DEFAULT_ANALYZER prebuilt-layout
azd ai cu config list
```

The CU shortcut uses the same user configuration as `azd config`; it does not
create a separate store. For example, setting `CU_ENDPOINT` above is equivalent
to `azd config set extensions.ai-content-understanding.CU_ENDPOINT <endpoint>`.

Process variables still override saved settings. Clear the session endpoint to
use the saved default:

```bash
unset CU_ENDPOINT
azd ai cu doctor
```

In PowerShell, use `Remove-Item Env:CU_ENDPOINT`. Use
`azd ai cu config unset CU_ENDPOINT` to remove the saved user default instead.

### 3. Manage project-specific environments (optional)

When you need settings grouped by project or separate development and production
resources, initialize an azd project and save settings in its environments.
Skip initialization if you already have an azd project, including one generated
by `azd ai cu infra generate`.

```bash
mkdir cu-project
cd cu-project
azd init --minimal --environment dev
azd env set CU_ENDPOINT "https://<dev-resource>.services.ai.azure.com/"
azd env set CU_DEFAULT_ANALYZER prebuilt-layout
azd env set AZURE_SUBSCRIPTION_ID "<dev-subscription-id>"
azd ai cu doctor --environment dev
```

Add another environment and switch between them:

```bash
azd env new prod
azd env set CU_ENDPOINT "https://<prod-resource>.services.ai.azure.com/"
azd env set AZURE_SUBSCRIPTION_ID "<prod-subscription-id>"
azd ai cu doctor --environment prod
azd env select dev
```

Run project commands from the project directory or a subdirectory, or point
`--cwd` at the project. Environment settings override user defaults; explicit
flags and process variables override both. Clear any process overrides before
switching resources. The selected subscription is used to resolve the
user-access tenant.

See [configuration precedence](docs/usage.md#configuration-and-api-versions)
and [cross-tenant connection guidance](docs/usage.md#connect-to-microsoft-foundry-and-check-setup)
for details.

## Supported Content Understanding API versions

The default API version is `2025-11-01`. Use `--api-version` to select
`2026-06-01-preview` or any valid `YYYY-MM-DD-preview` version for internal
testing or future previews. Acceptance by the CLI does not guarantee that the
service supports that version. The extension's Preview release status does not
change the default service API version.

See [API versions and configuration](docs/usage.md#configuration-and-api-versions)
for precedence and preview-only inline analysis.

## Use prebuilt analyzers

Start with `prebuilt-layout`, which does not require language or embeddings
model deployments. Download the public sample invoice and analyze it:

```bash
curl --fail --location --output invoice.pdf \
  https://raw.githubusercontent.com/Azure-Samples/azure-ai-content-understanding-assets/main/document/invoice.pdf

azd ai cu analyze invoice.pdf --llm-input
```

Without a specified or configured analyzer, analysis uses `prebuilt-layout`.
Use `--analyzer` to override the default.

Analysis displays YAML front matter and Markdown in the console by default.
`--llm-input` is enabled by default, so you can omit it. To save that text:

```bash
azd ai cu analyze invoice.pdf --output-file invoice.result.md
```

For the complete JSON response, including service warnings and usage, disable
the LLM-input view. Display it in the console or save it to a file:

```bash
azd ai cu analyze invoice.pdf --llm-input=false --output json
azd ai cu analyze invoice.pdf --llm-input=false --output-file invoice.result.json
```

See [analysis examples](docs/usage.md#analyze-content) for URLs, batches, dry runs,
and existing-result handling, and the
[prebuilt analyzer catalog](https://learn.microsoft.com/azure/ai-services/content-understanding/concepts/prebuilt-analyzers)
for other scenarios.

## Create a custom analyzer

Custom analyzers require supported model deployments and configured Content
Understanding defaults. Complete [model setup](docs/provisioning.md#deploy-models-and-configure-defaults)
first, then generate a schema from a representative document:

```bash
azd ai cu defaults show
azd ai cu analyzer schema create \
  --name invoice_v1 \
  --from-sample invoice.pdf \
  --output-file invoice-schema.json
```

Schema suggestion calls the service and can incur charges. Use `--from-template`
instead of `--from-sample invoice.pdf` for an offline starter.

Review and edit the generated schema for your extraction requirements, then
validate, create, and test the analyzer:

```bash
azd ai cu analyzer validate --schema invoice-schema.json --spec --strict
azd ai cu analyzer create --name invoice_v1 --schema invoice-schema.json
azd ai cu analyzer test --name invoice_v1 --file invoice.pdf \
  --output-file test-report.json --yes
```

The test summarizes field population and confidence, not extraction accuracy
against labeled ground truth. See [custom analyzer guidance](docs/usage.md#create-a-custom-analyzer)
for validation rules and test options.

## Command overview

| Command | Purpose |
| --- | --- |
| `azd ai cu analyze` | Analyze local files, directories, or HTTPS URLs. |
| `azd ai cu analyzer` | List, show, create, copy, delete, validate, and test analyzers. |
| `azd ai cu analyzer schema create` | Generate an offline starter or suggest fields from a sample. |
| `azd ai cu defaults` | Read or configure resource-level model-to-deployment defaults. |
| `azd ai cu config` | Set, get, list, or unset projectless CU user defaults in azd configuration. |
| `azd ai cu infra generate` | Generate an azd/Bicep project. Run `azd up` separately to provision it. |
| `azd ai cu doctor` | Check connectivity and readiness. |
| `azd ai cu env-var list` | Inspect supported process inputs with sensitive values redacted. |
| `azd ai cu version` | Show the installed extension version. |

Run `azd ai cu <command> --help` for options and examples with descriptions.

## CLI usage guide

The [CLI usage guide](docs/usage.md) covers batch processing, analyzer management,
configuration precedence, tenant selection, environment variables, automation,
output safety, and local development.

## More information

- [Microsoft Foundry provisioning](docs/provisioning.md)
- [Release history](CHANGELOG.md)
- [Supported file types and service limits](https://learn.microsoft.com/azure/ai-services/content-understanding/service-limits)
- [Azure Content Understanding documentation](https://learn.microsoft.com/azure/ai-services/content-understanding/)
- [Azure Developer CLI documentation](https://learn.microsoft.com/azure/developer/azure-developer-cli/)
