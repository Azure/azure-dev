# Azure Developer CLI extension for Content Understanding

This preview extension adds Azure Content Understanding commands under
`azd ai cu`. Analyze documents and media, manage analyzers, and configure model
defaults using Azure Developer CLI authentication and environments.

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
| No Foundry resource yet | [Create a new resource](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/provisioning.md#create-a-new-microsoft-foundry-resource). |
| A resource exists but generative models or defaults are missing | [Deploy models and configure defaults](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/provisioning.md#deploy-models-and-configure-defaults). |
| The resource is ready | Sign in and connect below. |

Resource creation, Azure permissions, and model setup are covered in the
separate [Microsoft Foundry provisioning guide](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/provisioning.md).

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

`doctor` shows human-readable configuration, authentication, service access, and
model readiness checks, including effective CU settings and their sources.
Use `azd ai cu doctor --output json` for scripts and CI.
Missing generative models produce a readiness warning (`ready: false` in JSON)
while `prebuilt-layout` remains usable. Inspect the report, not just the exit code.

### 2. Save a user default

To reuse the endpoint in future shell sessions, save a user default. It works
from any directory without `azd init`:

```bash
azd ai cu config set CU_ENDPOINT "https://<resource-name>.services.ai.azure.com/"
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
# Create a minimal azd project and its initial dev environment.
azd init --minimal --environment dev
# Save the CU endpoint in the selected dev environment.
azd env set CU_ENDPOINT "https://<dev-resource>.services.ai.azure.com/"
# Choose an analyzer for this environment; omit this to keep the built-in prebuilt-layout default.
azd env set CU_DEFAULT_ANALYZER prebuilt-layout
# Select the subscription used to resolve the user-access tenant.
azd env set AZURE_SUBSCRIPTION_ID "<dev-subscription-id>"
# Check configuration and readiness using the dev environment explicitly.
azd ai cu doctor --environment dev
```

Add another environment and switch between them:

```bash
# Create and select a separate production environment.
azd env new prod
# Save the production endpoint in the selected prod environment.
azd env set CU_ENDPOINT "https://<prod-resource>.services.ai.azure.com/"
# Resolve the production resource's user-access tenant through this subscription.
azd env set AZURE_SUBSCRIPTION_ID "<prod-subscription-id>"
# Check the production resource without changing the dev settings.
azd ai cu doctor --environment prod
# Make dev the selected environment for subsequent project commands.
azd env select dev
```

Run project commands from the project directory or a subdirectory, or point
`--cwd` at the project. Environment settings override user defaults; explicit
flags and process variables override both. Clear any process overrides before
switching resources. The selected subscription is used to resolve the
user-access tenant.

See [configuration precedence](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md#configuration-and-api-versions)
and [cross-tenant connection guidance](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md#connect-to-microsoft-foundry-and-check-setup)
for details.

## Supported Content Understanding API versions

The default API version is `2025-11-01`. Use `--api-version` to select
`2026-06-01-preview` or any valid `YYYY-MM-DD-preview` version for internal
testing or future previews. Acceptance by the CLI does not guarantee that the
service supports that version. The extension's Preview release status does not
change the default service API version.

See [API versions and configuration](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md#configuration-and-api-versions)
for precedence and preview-only inline analysis.

## Use prebuilt analyzers

Start with `prebuilt-layout`, which does not require language or embeddings
model deployments. Download the public sample invoice and analyze it:

```bash
curl --fail --location --output invoice.pdf \
  https://raw.githubusercontent.com/Azure-Samples/azure-ai-content-understanding-assets/main/document/invoice.pdf

# Analyze with prebuilt-layout when no analyzer is specified or configured.
azd ai cu analyze invoice.pdf
```

Without a specified or configured analyzer, analysis uses `prebuilt-layout`.
Use `--analyzer` (or `-a`) to select a different analyzer.

Analysis displays YAML front matter and Markdown in the console by default.
`--llm-input` is enabled by default, so you can omit it. To save that text:

```bash
azd ai cu analyze invoice.pdf --output-file invoice.result.md
```

For the complete JSON response, including service warnings and usage when
present, use `--output json`. Display it in the console or save it to a file:

```bash
# Display the complete service response as JSON.
azd ai cu analyze invoice.pdf --output json
# Save the same JSON response instead of the default Markdown view.
azd ai cu analyze invoice.pdf --output json --output-file invoice.result.json
```

Content Understanding also provides
[prebuilt analyzers](https://learn.microsoft.com/azure/ai-services/content-understanding/concepts/prebuilt-analyzers)
for domain-specific extraction. For example, `prebuilt-invoice` extracts
structured invoice fields. It requires the model deployments and defaults
described in [model setup](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/provisioning.md#deploy-models-and-configure-defaults).

```bash
# Extract invoice fields into YAML front matter, followed by document Markdown.
azd ai cu analyze invoice.pdf --analyzer prebuilt-invoice

# Use -a as shorthand for --analyzer and save the complete response as JSON.
azd ai cu analyze invoice.pdf -a prebuilt-invoice --output json --output-file invoice.result.json
```

In the saved response, structured fields are in each content entry's `fields`
object under `result.contents`, for example `result.contents[0].fields`.
See [document elements](https://learn.microsoft.com/azure/ai-services/content-understanding/document/elements)
for the document content structure.

See [analysis examples](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md#analyze-content) for URLs, batches, dry runs,
and existing-result handling, and the
[prebuilt analyzer catalog](https://learn.microsoft.com/azure/ai-services/content-understanding/concepts/prebuilt-analyzers)
for other scenarios.

## Create a custom analyzer

Custom analyzers require supported model deployments and configured Content
Understanding defaults. Complete [model setup](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/provisioning.md#deploy-models-and-configure-defaults)
first, then generate a schema from a representative document:

```bash
# Inspect the resource's current model-to-deployment defaults.
azd ai cu defaults show
# --name sets the custom analyzer ID in the generated definition.
# --from-sample asks the service to suggest fields from this document.
# --output-file saves the suggested definition for review and editing.
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
# --schema selects the local definition; --spec adds service-contract checks.
# --strict makes authoring warnings fail validation.
azd ai cu analyzer validate --schema invoice-schema.json --spec --strict
# --name selects the new analyzer ID; --schema supplies its reviewed definition.
azd ai cu analyzer create --name invoice_v1 --schema invoice-schema.json
# --name selects the analyzer; --file supplies a local test document.
# --output-file saves the test report; --yes confirms billed batches larger than 50 samples.
azd ai cu analyzer test --name invoice_v1 --file invoice.pdf \
  --output-file test-report.json --yes
```

The test summarizes field population and confidence, not extraction accuracy
against labeled ground truth. See [custom analyzer guidance](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md#create-a-custom-analyzer)
for validation rules and test options.

## Command overview

| Command | Purpose |
| --- | --- |
| `azd ai cu analyze` | Analyze local files, directories, or HTTPS URLs. |
| `azd ai cu analyzer` | List, show, create, copy, delete, validate, and test analyzers. |
| `azd ai cu analyzer schema create` | Generate an offline starter or suggest fields from a sample. |
| `azd ai cu defaults` | Read or configure resource-level model-to-deployment defaults. |
| `azd ai cu config` | Set, get, list, or unset global CU user defaults in azd configuration. |
| `azd ai cu infra generate` | Generate an azd/Bicep project. Run `azd up` separately to provision it. |
| `azd ai cu doctor` | Check connectivity and readiness. |
| `azd ai cu env-var list` | Inspect supported process inputs with sensitive values redacted. |
| `azd ai cu version` | Show the installed extension version. |

Run `azd ai cu <command> --help` for options and examples with descriptions.

## CLI usage guide

The [CLI usage guide](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/usage.md) covers batch processing, analyzer management,
configuration precedence, tenant selection, environment variables, automation,
output safety, and local development.

## More information

- [Microsoft Foundry provisioning](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/provisioning.md)
- [Release history](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/CHANGELOG.md)
- [Supported file types and service limits](https://learn.microsoft.com/azure/ai-services/content-understanding/service-limits)
- [Azure Content Understanding documentation](https://learn.microsoft.com/azure/ai-services/content-understanding/)
- [Azure Developer CLI documentation](https://learn.microsoft.com/azure/developer/azure-developer-cli/)
