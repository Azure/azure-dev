# Content Understanding CLI usage guide

This guide covers `azd ai cu` commands, configuration, automation, and local
development. Start with the [README](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/README.md) for concepts and first use.
For resource creation, permissions, model deployment, and service defaults, use
the separate [Microsoft Foundry provisioning guide](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/provisioning.md).

## Connect to Microsoft Foundry and check setup

You need a Microsoft Foundry resource endpoint and an identity with access to
Content Understanding. LLM-based prebuilt analyzers and custom analyzers also
require supported language and embeddings model deployments plus Content
Understanding defaults.

Sign in, set the resource endpoint for use without a project, and check
readiness:

```bash
azd auth login
export CU_ENDPOINT="https://<resource-name>.services.ai.azure.com/"
azd ai cu doctor
```

These examples use Bash. In PowerShell, set the endpoint with
`$env:CU_ENDPOINT = "https://<resource-name>.services.ai.azure.com/"`.
Endpoint-only use delegates tenant selection to azd authentication unless you
configure a tenant or subscription. To select a tenant explicitly, set
`AZURE_TENANT_ID` (`export AZURE_TENANT_ID="<tenant-id>"` in Bash or
`$env:AZURE_TENANT_ID = "<tenant-id>"` in PowerShell). An endpoint and tenant ID
do not replace `azd auth login` or supported noninteractive azd authentication.

To persist a global CU user default without creating an azd project using
`azd init`:

```bash
azd ai cu config set CU_ENDPOINT "https://<resource-name>.services.ai.azure.com/"
azd ai cu config get CU_ENDPOINT
azd ai cu config list
```

These commands use azd user configuration, not another store. For example,
`azd config set extensions.ai-content-understanding.CU_ENDPOINT <endpoint>`
writes the same setting as the CU shortcut. Both work from any directory without
`azd init`; managing defaults does not require sign-in.

Analysis uses `prebuilt-layout` when no analyzer is specified or configured.
For a resource with the required model deployments and defaults, you can
optionally save a different analyzer and remove that preference later:

```bash
# Use the invoice analyzer by default instead of prebuilt-layout.
azd ai cu config set CU_DEFAULT_ANALYZER prebuilt-invoice
# Remove only the global preference; other configuration sources still apply.
azd ai cu config unset CU_DEFAULT_ANALYZER
```

In an existing azd project, you can instead save project-specific settings in an environment:

```bash
azd env select dev
azd env set CU_ENDPOINT "https://<resource-name>.services.ai.azure.com/"
azd env set AZURE_SUBSCRIPTION_ID "<subscription-id>"
azd env set CU_DEFAULT_ANALYZER prebuilt-layout
azd ai cu doctor --environment dev
```

The selected environment can supply `AZURE_TENANT_ID` directly; otherwise its
resolved subscription is used to look up the user-access tenant.

Use `azd env new dev` if the project does not yet have that environment.
Explicit options and process inputs take precedence over saved settings; clear
process overrides when switching resources.

The extension only supports Microsoft Entra ID authentication through azd.
Use `azd auth login` or supported noninteractive azd authentication.
API-key authentication and CU profile
commands are not supported.

`CU_API_KEY` and saved API-key entries are rejected rather than used, even when
empty. `CU_AUTH_MODE`, if set, must be `login`.

`doctor` shows human-readable checks for configuration, authentication, service
access, and model defaults, with effective `CU_*` settings and their sources.
Use `azd ai cu doctor --output json` for the structured report in scripts and CI;
its `sources` object identifies each setting's source:
`flag: --<name>`, `process`, `azd environment: <name>`, `user config`, or `built-in`.
A readiness warning (`ready: false` in JSON) with missing model requirements can still exit
successfully: non-generative analyzers may remain usable. Authentication,
configuration, and service failures return nonzero. Inspect the report rather
than using the exit code alone as a generative-readiness gate.
Scripts that previously parsed default `doctor` output should explicitly pass
`--output json`.

## Analyze content

Download the public sample invoice and start with `prebuilt-layout`, which does
not require language or embeddings model deployments:

```bash
curl --fail --location --output invoice.pdf \
  https://raw.githubusercontent.com/Azure-Samples/azure-ai-content-understanding-assets/main/document/invoice.pdf

azd ai cu analyze invoice.pdf --llm-input
```

When no analyzer is specified or configured, analysis defaults to
`prebuilt-layout`. Use `--analyzer` (or `-a`) to override a configured default.

By default, a single analysis displays YAML front matter and Markdown in the
console. This LLM-input view is enabled by default; `--llm-input` can be omitted.
Save the same text with `--output-file`:

```bash
azd ai cu analyze \
  --file invoice.pdf \
  --analyzer prebuilt-layout \
  --llm-input \
  --output-file invoice.result.md
```

For the complete JSON response envelope, including the result, service warnings,
and usage when present, use `--output json`. These examples display the JSON
in the console and save it to a file, respectively:

```bash
# Display the complete response as JSON.
azd ai cu analyze invoice.pdf --output json
# Save the complete response as JSON.
azd ai cu analyze invoice.pdf --output json --output-file invoice.result.json
```

`--output json` disables the LLM-input view for both console and saved results.
Explicitly combining it with `--llm-input` or `--llm-input=true` is an error.
`--llm-input=false` also remains available to select the full response.
The view determines the result content, not the filename extension: naming a
file `.json` alone does not change its content.
`--usage` wraps a streamed result and usage metadata in a JSON object.
Saved LLM input remains plain text.

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

| Argument | Meaning |
| --- | --- |
| `--source ./documents` | Select files from this directory. |
| `--pattern "*.pdf"` | Match source-relative paths using this pattern. Quoting keeps the shell from expanding it. |
| `--recursive` | Include nested directories, excluding hidden directories. |
| `--output-dir ./results` | Save results here, preserving source-relative paths. The default suffix is `.result.md`; use `--output json` for `.result.json`. |
| `--report-file ./report.json` | Write a new JSON report of per-input outcomes, separate from extracted results. |
| `--analyzer prebuilt-layout` | Explicitly select the analyzer instead of a configured default. |
| `--yes` | Confirm the cost of batches larger than 50 inputs without prompting. |

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
Understanding defaults. Inspect the current resource mappings first:

```bash
# Read the resource's model-to-deployment defaults without changing them.
azd ai cu defaults show
```

Generate a starter schema from the sample invoice:

```bash
# --name sets the analyzer ID; --from-sample asks the service to suggest fields.
# --output-file saves the suggested definition for review and editing.
azd ai cu analyzer schema create \
  --name invoice_v1 \
  --from-sample invoice.pdf \
  --output-file invoice-schema.json
```

Schema suggestion calls the service and can incur charges. For an offline
starter instead, replace `--from-sample invoice.pdf` with `--from-template`.
Template mode also supports `--modality document|image|audio|video` and
`--type extraction|classification`.

Review and edit the generated fields, descriptions, and model mappings, then
validate the definition locally:

```bash
# --schema selects the local definition; --spec adds bundled service-contract checks.
# --strict treats authoring warnings as validation failures.
azd ai cu analyzer validate \
  --schema invoice-schema.json \
  --spec \
  --strict
```

Validation is offline and does not require an azd project, endpoint, or sign-in.

Create a new analyzer from the reviewed definition:

```bash
# --name sets the new analyzer ID; --schema supplies its definition.
azd ai cu analyzer create \
  --name invoice_v1 \
  --schema invoice-schema.json
```

Custom analyzer IDs use 1-64 ASCII letters, numbers, or underscores, such as
`invoice_v1`. Create never replaces an existing analyzer.

Test the analyzer against a local sample:

```bash
# --name selects the analyzer; --file supplies the local document.
# --output-file saves the report; --yes confirms billed batches larger than 50 samples.
azd ai cu analyzer test \
  --name invoice_v1 \
  --file invoice.pdf \
  --output-file test-report.json \
  --yes
```

An analyzer test sends local samples to the service and reports
field population and confidence, not extraction accuracy. Its concurrency
defaults to 4 and accepts 1-16. Its `--dry-run` mode is fully offline.

## Manage analyzers and defaults

List custom analyzers ordered by creation time:

```bash
# --kind custom excludes service-provided analyzers; --sort-by chooses the ordering field.
azd ai cu analyzer list --kind custom --sort-by createdAt
```

Inspect an analyzer definition:

```bash
# invoice_v1 identifies the analyzer to inspect.
azd ai cu analyzer show invoice_v1
```

Copy an existing custom analyzer to a new ID:

```bash
# Copy the definition from invoice_v1 to a new analyzer named invoice_v2.
azd ai cu analyzer copy invoice_v1 invoice_v2
```

Without resource selectors, copy stays on the configured resource.
Cross-resource copy accepts `--source-resource` and `--destination-resource`
as account names, ARM resource IDs, or HTTPS endpoints. Use the corresponding
subscription and resource-group flags to narrow discovery. Missing custom
analyzer dependencies must be copied explicitly. Copy never replaces an
existing destination.

Delete the copied analyzer when it is no longer needed:

```bash
# invoice_v2 identifies the analyzer to delete; --yes confirms deletion without prompting.
azd ai cu analyzer delete invoice_v2 --yes
```

Without `--yes`, deletion requires interactive confirmation.

Inspect the resource's current model-to-deployment defaults:

```bash
# Read service defaults without changing them.
azd ai cu defaults show
```

Apply mappings from the selected azd environment:

```bash
# --from-environment loads cu.modelDeployments from the selected azd environment.
azd ai cu defaults set --from-environment
```

Defaults are model-to-deployment settings on the CU resource, not local CLI
settings. `--from-environment` uses `cu.modelDeployments`; repeatable
`--model MODEL=DEPLOYMENT` arguments override those mappings. Updates merge
with existing service mappings unless `--replace` skips the client-side merge.
The service still uses merge-patch semantics, so omitted mappings are not
guaranteed to be deleted.

`doctor --fix-defaults` explicitly updates defaults from environment model
mappings. It does not deploy models or provision resources.

## Configuration and API versions

The default API version is `2025-11-01`. `--api-version` also accepts
`2026-06-01-preview` and any valid calendar date in `YYYY-MM-DD-preview` format
for internal testing or future previews. The selected version is sent unchanged;
the service determines whether it is available and supports the requested
operation. Other GA versions, malformed dates, and extra suffixes are rejected.

Inline analysis requires a preview version and does not fall back to
asynchronous analysis:

```bash
azd ai cu analyze \
  --file invoice.pdf \
  --analyzer prebuilt-layout \
  --inline \
  --api-version 2026-06-01-preview
```

Offline authoring checks accept the same preview versions, but `analyzer validate
--spec` requires a bundled contract. Contracts are available only for
`2025-11-01` and `2026-06-01-preview`. For other preview versions, omit `--spec`;
the CLI reports a clear error rather than silently substituting a contract.

Successful inline responses can omit detected MIME metadata. Full JSON output
preserves that response as returned; the readable LLM-input view uses
`mimeType: unknown` when the service supplies no MIME type. Inline success does
not trigger asynchronous polling or fallback.

Endpoint, default analyzer, and API version use the exact, case-sensitive names
`CU_ENDPOINT`, `CU_DEFAULT_ANALYZER`, and `CU_API_VERSION` across process
variables, azd environment values, and CU user defaults. There are no alternate
setting names or compatibility fallbacks: `FOUNDRY_ENDPOINT`,
`cu.defaultAnalyzer`, and `cu.apiVersion` are not read as substitutes.

Service commands use one shared resolver, in this order:

1. Explicit command flags.
2. Process environment variables.
3. Applicable azd project-environment values.
4. CU user defaults under `extensions.ai-content-understanding` in azd user configuration.
5. Built-in defaults, or an actionable missing-setting error.

| Setting | Highest to lowest priority |
| --- | --- |
| Endpoint | `--endpoint`; process `CU_ENDPOINT`; applicable environment `CU_ENDPOINT`; user default `CU_ENDPOINT`; missing-setting error. |
| API version | `--api-version`; process `CU_API_VERSION`; applicable environment `CU_API_VERSION`; user default `CU_API_VERSION`; `2025-11-01`. |
| Default analyzer | `--analyzer`; process `CU_DEFAULT_ANALYZER`; applicable environment `CU_DEFAULT_ANALYZER`; user default `CU_DEFAULT_ANALYZER`; `prebuilt-layout`. |
| Model mappings | Explicit mappings; environment config `cu.modelDeployments`; empty mapping. |
| Tenant | Process `AZURE_TENANT_ID`; selected environment `AZURE_TENANT_ID`; default deployment context for the default environment, or resolved subscription lookup for explicitly selected environments and endpoint-only use. Endpoint-only use without a tenant or subscription delegates to azd authentication. |
| Subscription/resource group | Explicit resource selectors; process `AZURE_SUBSCRIPTION_ID` / `AZURE_RESOURCE_GROUP`; selected environment values; default deployment scope when applicable. |

With no explicit `--environment` or process `AZD_ENVIRONMENT`, a process or command-line
endpoint selects endpoint-only mode: project-environment settings are not loaded,
even inside a project. User defaults can still supply the analyzer and API version.
Otherwise the current project environment is used when available. If there is
no project or no current environment, user defaults allow the command to run without one.
An explicitly selected missing environment always fails; it never silently
falls back to user defaults. Other environment-loading failures also fail.
Explicitly selected environments do not borrow tenant or scope from an unrelated environment.

Invalid effective values fail instead of falling back to a lower-priority source.
Public-cloud resource endpoints must use HTTPS with a resource hostname under
`services.ai.azure.com` or `cognitiveservices.azure.com`, on the default port or
explicit port `443`. Arbitrary hosts, IP addresses, and other ports are rejected
before service authentication to prevent sending access tokens to an untrusted server.
This applies to endpoints supplied through flags, process variables, azd environments,
user defaults, and Azure resource discovery.
Explicitly empty command flags are rejected; empty process/environment values
are treated as unset. `config set` rejects empty values; use `config unset` to
remove a user default.

`config get` and `config list` show only stored CU user defaults, not merged
effective settings. Unsupported setting names are rejected by the shortcut.
`config unset` removes only the user default; it does not change shell variables
or an azd environment. These commands do not accept `--environment`.
Direct `azd config` writes are validated when their values are used.
Use `doctor` for effective values and sources. These CLI preferences are separate
from resource-level model defaults managed by `azd ai cu defaults`.

Offline schema templates and validation remain independent of resource settings
and user defaults; they use their existing explicit/API-version process inputs.

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
| No valid azd authentication, even with endpoint and tenant | Authentication is still required; sign in through azd. |
| Help or version | No authentication or tenant configuration is required. |

For guest or cross-tenant access, the signed-in identity must have access through
the appropriate tenant. A tenant ID does not grant resource permissions, and the
extension does not infer a tenant from the endpoint.

Use `azd ai cu env-var list` to inspect supported process inputs with sensitive
values redacted. It does not dump saved azd environment settings or unrelated
variables. `CU_ON_EXISTS` supplies the analysis existing-result policy when
`--on-existing` is absent. `FOUNDRY_ENDPOINT` remains an infrastructure output,
not an alias for `CU_ENDPOINT` in service-command configuration.

The azd extension SDK also reads these inherited process inputs. Corresponding
explicit flags take precedence over the SDK environment defaults:

| Variable | Purpose | Default when unset |
| --- | --- | --- |
| `AZD_DEBUG` | Enables debug and diagnostics logging; use `true` or `false`. Corresponds to `--debug`. | `false` |
| `AZD_NO_PROMPT` | Disables interactive prompts; use `true` or `false`. Corresponds to `--no-prompt` and does not imply `--yes`. | `false` |
| `AZD_CWD` | Sets the working directory for command execution. Corresponds to `--cwd` / `-C`. | Inherits the process working directory. |

## Command overview

| Command | Purpose |
| --- | --- |
| `azd ai cu analyze` | Analyze local files, directories, or HTTPS URLs. |
| `azd ai cu analyzer` | List, show, create, copy, delete, validate, and test analyzers. |
| `azd ai cu analyzer schema create` | Generate an offline starter or suggest fields from a sample. |
| `azd ai cu defaults` | Read or configure model-to-deployment defaults. |
| `azd ai cu config` | Set, get, list, or unset CU user defaults in azd configuration. |
| `azd ai cu infra generate` | Generate an azd/Bicep project and its postprovision hooks. |
| `azd ai cu doctor` | Show readable connectivity and readiness checks; use `--output json` for automation. |
| `azd ai cu env-var list` | Inspect supported process inputs with secrets redacted. |
| `azd ai cu version` | Show the installed extension version. |

Run `azd ai cu <command> --help` for command-specific options and common-use
examples with descriptions. Help uses azd-style headings and colors when enabled;
set `NO_COLOR=1` for plain text.

## Usage guidance

- Supported output formats are `default`, `json`, and `none`. Analysis defaults
  to readable LLM-input text, and `doctor` defaults to readable diagnostics.
  Other commands, analysis summaries, dry runs, and
  `--usage` wrappers use JSON. Use `--output json` for complete analysis JSON.
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

To build from source, use the Go version declared in [go.mod](../go.mod) and
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
beside their Go packages under [internal/](../internal/), including command,
REST-client adapter, generated-project, and recorded-playback tests:

```bash
go test -race ./... -count=1
go fix ./...
golangci-lint run ./...

cd cu-rest-client
go test -race ./... -count=1
go fix ./...
golangci-lint run ./...
cd ..

cspell lint '**/*.go' '**/*.md' --config ./cspell.yaml --no-progress
```

The extension uses a [Content Understanding client](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/cu-rest-client/README.md)
maintained for this extension. It is an extension-internal dependency, not a
separately published package.
CI tests and lints both modules. Ordinary tests use fakes, local
servers, and strict recordings; they do not call Azure or require Python.
Reused assets retain their [fixture provenance](../testdata/parity/fixtures.yaml).
Extension-local LF rules keep byte-level fixture and embedded-asset hashes stable
across platforms.

[ci-test.ps1](../ci-test.ps1) is the shared release pipeline's test entry point.
Its Windows and Mac jobs also run [ci-smoke.ps1](../ci-smoke.ps1), which verifies
an official azd 1.37.0 archive and exercises local-registry installation, help,
version, offline validation, and cleanup against the built extension binary.
Generated-project tests execute the Bash and PowerShell launchers; PowerShell
is required on Windows and is tested elsewhere when `pwsh` is available.
Offline tests do not establish live authentication/RBAC, deployment success,
or release signing and publication.

## More information

- [Getting started](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/README.md)
- [Microsoft Foundry provisioning](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/docs/provisioning.md)
- [Release history](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/azure.ai.content-understanding/CHANGELOG.md)
- [Azure Content Understanding documentation](https://learn.microsoft.com/azure/ai-services/content-understanding/)
- [Prebuilt analyzers](https://learn.microsoft.com/azure/ai-services/content-understanding/concepts/prebuilt-analyzers)
- [Azure Developer CLI documentation](https://learn.microsoft.com/azure/developer/azure-developer-cli/)
- [Extension development guidelines](https://github.com/Azure/azure-dev/blob/main/cli/azd/docs/extensions/extensions-style-guide.md)
