# Azure Developer CLI extension for Content Understanding

This preview extension adds Azure Content Understanding commands under
`azd ai cu`. Use it to connect to a Microsoft Foundry resource, analyze documents
and media, manage analyzers, configure model defaults, and generate an azd/Bicep
project for provisioning.

The extension uses Azure Developer CLI authentication, environments, subscription
context, and prompts. It is a native Go extension; Python and the standalone
`cu` CLI are not required.

## Content Understanding concepts

Content Understanding processes unstructured content, including documents,
images, audio, and video, into structured output for automation, analytics, and
search workflows. It is a Foundry Tool accessed through a Microsoft Foundry
resource in Azure.

- A **file** is the input, such as a document, image, audio file, or video.
- An **analyzer** defines how Content Understanding processes a file and
  extracts content and structured fields.
- An **analyzer result** is the output from processing a file. It can include
  extracted Markdown, structured fields, and modality-specific details.
- A **prebuilt analyzer** is supplied by Content Understanding for common
  extraction, search, and domain scenarios.
- A **custom analyzer** uses a base analyzer and field schema that you define
  for your scenario.

Further reading:

- [What is Content Understanding?](https://learn.microsoft.com/azure/ai-services/content-understanding/overview)
- [Content Understanding terminology](https://learn.microsoft.com/azure/ai-services/content-understanding/glossary)
- [Supported file types and service limits](https://learn.microsoft.com/azure/ai-services/content-understanding/service-limits)

## Install

Requires [Azure Developer CLI](https://aka.ms/azd) 1.37.0 or later.

```bash
azd ext install azure.ai.content-understanding
azd ai cu --help
```

## Connect to Microsoft Foundry and check setup

You need a Microsoft Foundry resource endpoint and an identity with access to
Content Understanding. LLM-based prebuilt analyzers and custom analyzers also
require supported language and embeddings model deployments plus Content
Understanding defaults.

Sign in, set the resource endpoint for projectless use, and check
readiness:

```bash
azd auth login
export CU_ENDPOINT="https://<resource-name>.services.ai.azure.com/"
azd ai cu doctor
```

These examples use Bash. In PowerShell, set the endpoint with
`$env:CU_ENDPOINT = "https://<resource-name>.services.ai.azure.com/"`.
Projectless use delegates tenant selection to azd authentication unless you
configure a tenant or subscription. To select a tenant explicitly, set
`AZURE_TENANT_ID` (`export AZURE_TENANT_ID="<tenant-id>"` in Bash or
`$env:AZURE_TENANT_ID = "<tenant-id>"` in PowerShell). An endpoint and tenant ID
do not replace `azd auth login` or supported noninteractive azd authentication.

In an existing azd project, you can instead save settings in an environment:

```bash
azd env select dev
azd env set CU_ENDPOINT "https://<resource-name>.services.ai.azure.com/"
azd env set AZURE_SUBSCRIPTION_ID "<subscription-id>"
azd env config set cu.defaultAnalyzer '"prebuilt-layout"'
azd ai cu doctor --environment dev
```

The selected environment can supply `AZURE_TENANT_ID` directly; otherwise its
resolved subscription is used to look up the user-access tenant.

Use `azd env new dev` if the project does not yet have that environment.
Explicit options and process inputs take precedence over saved settings; clear
process overrides when switching resources.

The extension uses Microsoft Entra ID through `azd auth login`, not `az login`
or a separate credential chain. It does not expose API-key authentication or
CU profile commands.
Remove `CU_API_KEY` from the process and selected environment, and remove
`cu.apiKey` or legacy `api_key` settings; even empty persisted key entries are
rejected. `CU_AUTH_MODE`, if set, must be `login`.

`doctor` checks configuration, authentication, service access, and model defaults.
A report with `ready: false` and missing model requirements can still exit
successfully: non-generative analyzers may remain usable. Authentication,
configuration, and service failures return nonzero. Inspect the report rather
than using the exit code alone as a generative-readiness gate.

## Analyze content

Download the public sample invoice and start with `prebuilt-layout`, which does
not require language or embeddings model deployments:

```bash
curl --fail --location --output invoice.pdf \
  https://raw.githubusercontent.com/Azure-Samples/azure-ai-content-understanding-assets/main/document/invoice.pdf

azd ai cu analyzer list --output json
azd ai cu analyze invoice.pdf
```

When no analyzer is specified or configured, analysis defaults to
`prebuilt-layout`. Use `--analyzer` to override a configured default.

By default, a single analysis returns the complete terminal response envelope,
including the result, service warnings, and usage when present. Use `--llm-input`
to format the extracted content for generative AI model input:

```bash
azd ai cu analyze \
  --file invoice.pdf \
  --analyzer prebuilt-layout \
  --llm-input \
  --output-file invoice.result.md
```

Saved LLM input is plain text; streamed LLM input is a JSON string. Use
`--output-file invoice.result.json` without `--llm-input` to save the full JSON
response instead.

Analyze an HTTPS or Azure Blob SAS URL without downloading it first:

```bash
azd ai cu analyze \
  --url "https://<storage-account>.blob.core.windows.net/<container>/<blob>?<sas-token>" \
  --analyzer prebuilt-layout
```

Analyze a batch while preserving source-relative paths under an output directory:

```bash
azd ai cu analyze \
  --source ./documents \
  --pattern "*.pdf" \
  --recursive \
  --output-dir ./results \
  --report-file ./report.json \
  --analyzer prebuilt-layout \
  --yes
```

Use `--dry-run` without `--yes` to inspect the execution plan before CU calls or
writes. Analysis dry runs still require resolved endpoint and analyzer
configuration. Select inputs using one mode: positional inputs, `--file`,
`--source`, or `--url`.

- Batch concurrency defaults to 4; `--concurrency` accepts 1-32.
- More than 50 selected inputs require confirmation or explicit `--yes`.
- Existing results fail by default. Use `--on-existing skip` to keep them or
  `--on-existing reanalyze` to replace them.
- Without an output directory, local batches write beside their inputs. Batches
  containing URLs require an output directory.
- A requested report records successes, failures, and skipped inputs. Partial
  failures or cancellation return nonzero while retaining completed result
  files and the requested report. Reports never overwrite existing files.

## Create a custom analyzer

Custom analyzers require supported model deployments and configured Content
Understanding defaults. Generate a starter schema from the sample invoice:

```bash
azd ai cu defaults show
azd ai cu analyzer schema create \
  --name invoice_v1 \
  --from-sample invoice.pdf \
  --output-file invoice-schema.json
```

Schema suggestion calls the service and can incur charges. For an offline
starter instead, replace `--from-sample invoice.pdf` with `--from-template`.
Template mode also supports `--modality document|image|audio|video` and
`--type extraction|classification`.

Review and edit the generated fields, descriptions, and model mappings before
validation and creation:

```bash
azd ai cu analyzer validate \
  --schema invoice-schema.json \
  --spec \
  --strict

azd ai cu analyzer create \
  --name invoice_v1 \
  --schema invoice-schema.json

azd ai cu analyzer test \
  --name invoice_v1 \
  --file invoice.pdf \
  --output-file test-report.json \
  --yes
```

Custom analyzer IDs use 1-64 ASCII letters, numbers, or underscores, such as
`invoice_v1`. Create never replaces an existing analyzer.

Validation is offline and does not require a project, endpoint, or sign-in.
`--spec` adds bundled service-contract checks; `--strict` makes authoring warnings
fail validation. An analyzer test sends local samples to the service and reports
field population and confidence, not extraction accuracy. Its concurrency
defaults to 4 and accepts 1-16. Its `--dry-run` mode is fully offline.

## Manage analyzers and defaults

```bash
azd ai cu analyzer list --kind custom --sort-by createdAt
azd ai cu analyzer show invoice_v1
azd ai cu analyzer copy invoice_v1 invoice_v2
azd ai cu analyzer delete invoice_v2 --yes

azd ai cu defaults show
azd ai cu defaults set --from-environment
```

Without resource selectors, copy stays on the configured resource.
Cross-resource copy accepts `--source-resource` and `--destination-resource`
as account names, ARM resource IDs, or HTTPS endpoints. Use the corresponding
subscription and resource-group flags to narrow discovery. Missing custom
analyzer dependencies must be copied explicitly. Copy never replaces an
existing destination; delete requires confirmation or `--yes`.

Defaults are model-to-deployment settings on the CU resource, not local CLI
settings. `--from-environment` uses `cu.modelDeployments`; repeatable
`--model MODEL=DEPLOYMENT` arguments override those mappings. Updates merge
with existing service mappings unless `--replace` skips the client-side merge.
The service still uses merge-patch semantics, so omitted mappings are not
guaranteed to be deleted.

`doctor --fix-defaults` explicitly updates defaults from environment model
mappings. It does not deploy models or provision resources.

## Generate Microsoft Foundry infrastructure

`azd ai cu infra generate` writes a self-contained azd/Bicep project. It does
not provision resources or run `azd up`:

```bash
azd auth login
azd ai cu infra generate --output-dir provision
cd provision
azd up
```

Generation prompts for a subscription and defaults to environment `dev`, region
`eastus2`, and interactive model selection. Use `--foundry-endpoint` to adopt an
existing account or `--foundry-prefix` for a new one; these cannot be combined.
The inherited `--environment` / `-e` selects the generated azd environment.

The generated Bash and PowerShell hooks invoke
`azd ai cu infra _postprovision-v1` to set up selected model deployments and CU
defaults. Keep this extension installed when running `azd up`; neither Python
nor standalone `cu` is required. Role assignment is opt-in with `--assign-roles`
and requires appropriate Azure permissions. Existing accounts require an
existing data-plane role.

Recognized generated projects retain editable assets and environment settings
unless `--force` is supplied. Force replaces known generated assets and resets
model/configuration setup; unrelated files are not deleted.
`CU_DISABLE_AUTO_CONFIG=true` disables subsequent CU configuration, not model
setup.

## Configuration and API versions

The extension supports `2025-11-01` (default) and `2026-06-01-preview`.
Override the version for a command with `--api-version`. Inline analysis
requires the preview version and does not fall back to asynchronous analysis:

```bash
azd ai cu analyze \
  --file invoice.pdf \
  --analyzer prebuilt-layout \
  --inline \
  --api-version 2026-06-01-preview
```

For persisted settings, use azd environments and `cu.*` environment configuration
instead of CU profiles.

| Setting | Highest to lowest priority |
| --- | --- |
| Endpoint | `--endpoint`; process `CU_ENDPOINT`; selected environment `CU_ENDPOINT`; selected environment `FOUNDRY_ENDPOINT`. |
| API version | `--api-version`; process `CU_API_VERSION`; environment config `cu.apiVersion`; environment `CU_API_VERSION`; GA. |
| Default analyzer | `--analyzer`; process `CU_DEFAULT_ANALYZER`; environment config `cu.defaultAnalyzer`; environment `CU_DEFAULT_ANALYZER`; `prebuilt-layout`. |
| Model mappings | Explicit mappings; environment config `cu.modelDeployments`; empty mapping. |
| Tenant | Process `AZURE_TENANT_ID`; selected environment `AZURE_TENANT_ID`; default deployment context for the default environment, or resolved subscription lookup for explicitly selected environments and projectless use. Projectless use without a tenant or subscription delegates to azd authentication. |
| Subscription/resource group | Explicit resource selectors; process `AZURE_SUBSCRIPTION_ID` / `AZURE_RESOURCE_GROUP`; selected environment values; default deployment scope when applicable. |

With no explicit `--environment` or process `AZD_ENVIRONMENT`, a process or command-line endpoint selects projectless
mode; otherwise the default azd environment is used. Explicitly selected
environments do not borrow tenant or scope from an unrelated environment.
When tenant selection is delegated to azd, `azd auth token` uses its normal
resolution: the current project's subscription, process `AZURE_SUBSCRIPTION_ID`,
then the signed-in identity's home tenant. Being inside a project can therefore
affect authentication even though the extension does not load project settings
for endpoint-only operation.

| Scenario | Tenant behavior |
|---|---|
| Default project environment with a tenant or deployment context | Uses the configured tenant, or the default deployment context. |
| Explicitly selected environment with a configured tenant | Uses that tenant; process `AZURE_TENANT_ID` takes precedence over saved values. |
| Explicitly selected environment without a tenant | Looks up the user-access tenant for the resolved subscription. If neither can be resolved, reports an error instead of using another environment. |
| Endpoint without an environment selection, with a configured tenant | Uses the configured tenant. |
| Endpoint without an environment selection, no configured tenant | Looks up the resolved subscription if present; otherwise delegates tenant selection to azd authentication. |
| No valid azd authentication, even with endpoint and tenant | Authentication is still required. Azure CLI-only login is not used. |
| Help or version | No authentication or tenant configuration is required. |

For guest or cross-tenant access, the signed-in identity must have access through
the appropriate tenant. A tenant ID does not grant resource permissions, and the
extension does not infer a tenant from the endpoint.

Use `azd ai cu env-var list` to inspect supported process inputs with sensitive
values redacted. It does not dump saved azd environment settings or unrelated
variables. `CU_ON_EXISTS` supplies the analysis existing-result policy when
`--on-existing` is absent. `FOUNDRY_ENDPOINT` is a selected-environment fallback,
not an independent process override.

## Command overview

| Command | Purpose |
| --- | --- |
| `azd ai cu analyze` | Analyze local files, directories, or HTTPS URLs. |
| `azd ai cu analyzer` | List, show, create, copy, delete, validate, and test analyzers. |
| `azd ai cu analyzer schema create` | Generate an offline starter or suggest fields from a sample. |
| `azd ai cu defaults` | Read or configure model-to-deployment defaults. |
| `azd ai cu infra generate` | Generate an azd/Bicep project and its postprovision hooks. |
| `azd ai cu doctor` | Return structured connectivity and readiness checks. |
| `azd ai cu env-var list` | Inspect supported process inputs with secrets redacted. |
| `azd ai cu version` | Show the installed extension version. |

Run `azd ai cu <command> --help` for command-specific options and common-use
examples with descriptions. Help uses azd-style headings and colors when enabled;
set `NO_COLOR=1` for plain text.

## Usage guidance

- Supported result formats are the default JSON presentation, `--output json`,
  and `--output none`. Do not assume Azure CLI table or query behavior.
- `--output none` suppresses successful stdout, not service operations, requested
  file writes, confirmations, or errors. Always check the exit code before parsing
  output: the azd host can render extension errors on stdout.
- `--no-prompt` disables interactive confirmation; it does not imply `--yes`.
  For unattended infrastructure generation, also provide an unambiguous azd
  default subscription and `--models none`, `recommended`, or explicit
  comma-separated `model@version` selectors.
- Write extracted content only to trusted private directories. Output safety
  checks reject symlinks, unsafe shared directories, and unsupported filesystems
  without changing existing permissions. Network output volumes are not supported.
- Diagnostics redact sensitive details, but intentional result stdout and saved
  files can contain document content. Do not share credentials, SAS URLs, or
  extracted customer data in logs, issues, or source control.

## Local development

To build from source, use the Go version declared in [go.mod](go.mod) and
install the azd extension developer kit. From the azure-dev repository root:

```bash
azd ext install microsoft.azd.extensions
cd cli/azd/extensions/azure.ai.content-understanding
azd x build
azd x pack
azd x publish
azd ext install azure.ai.content-understanding --source local
azd ai cu --help
```

`azd x publish` without a GitHub repository publishes to the local extension
registry, not the public registry. Subsequent `azd x build` or `azd x watch`
runs rebuild and reinstall the extension without repeating the initial
packaging and registration steps.

Tests live
beside their Go packages under [internal/](internal/), including command,
SDK-adapter, generated-project, and recorded-playback tests:

```bash
go test -race ./... -count=1
go fix ./...
golangci-lint run ./...

cd sdk/azcontentunderstanding
go test -race ./... -count=1
go fix ./...
golangci-lint run ./...
cd ../..

cspell lint '**/*.go' '**/*.md' --config ./cspell.yaml --no-progress
```

The extension uses a separately versioned [local Go SDK](sdk/azcontentunderstanding/README.md).
CI tests and lints both modules. Ordinary tests use fakes, local
servers, and strict recordings; they do not call Azure or require Python.
Reused assets retain their [fixture provenance](testdata/parity/fixtures.yaml).
Extension-local LF rules keep byte-level fixture and embedded-asset hashes stable
across platforms.

[ci-test.ps1](ci-test.ps1) is the shared release pipeline's test entry point.
Its Windows and Mac jobs also run [ci-smoke.ps1](ci-smoke.ps1), which verifies
an official azd 1.37.0 archive and exercises local-registry installation, help,
version, offline validation, and cleanup against the built extension binary.
Generated-project tests execute the Bash and PowerShell launchers; PowerShell
is required on Windows and is tested elsewhere when `pwsh` is available.
Offline tests do not establish live authentication/RBAC, deployment success,
or release signing and publication.

## More information

- [Release history](CHANGELOG.md)
- [Azure Content Understanding documentation](https://learn.microsoft.com/azure/ai-services/content-understanding/)
- [Prebuilt analyzers](https://learn.microsoft.com/azure/ai-services/content-understanding/concepts/prebuilt-analyzers)
- [Azure Developer CLI documentation](https://learn.microsoft.com/azure/developer/azure-developer-cli/)
- [Extension development guidelines](../../docs/extensions/extensions-style-guide.md)
