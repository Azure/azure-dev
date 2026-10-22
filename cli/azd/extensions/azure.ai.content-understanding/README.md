# Azure Content Understanding

Native Go first-party Azure Content Understanding extension.
This development build provides help, version, metadata, analysis, analyzer
list/show/create/copy/delete/validate/test, schema authoring, service defaults, doctor,
environment-variable inspection, infrastructure generation, and generated-project hooks.
All 16 required command paths are implemented. Production service calls use a
source-pinned local CU Go SDK module; release readiness and the remaining live
validation gaps are separate gates.

## Installation

This extension is not published in the public registry yet. Build and install it
from this directory with `azd x build`.

## Usage

`azd ai cu <command> [options]`

## Commands

```bash
azd ai cu --help
azd ai cu version
azd ai cu version --output json
azd ai cu analyzer list --kind custom --sort-by createdAt -o json
azd ai cu analyzer validate --schema schema.json --strict --spec -o json
azd ai cu analyzer schema create -n invoice_v1 --output-file schema.json
azd ai cu analyzer schema create --type classification -o json
azd ai cu analyzer schema create --from-sample invoice.pdf --output-file suggested.json
azd ai cu analyzer show prebuilt-document
azd ai cu analyzer create invoice_v1 --schema schema.json
azd ai cu analyzer copy invoice_v1 invoice_v2
azd ai cu analyzer test invoice_v1 --source ./samples --pattern '*.pdf' --dry-run
azd ai cu analyzer test -n invoice_v1 --file invoice.pdf --output-file test-report.json
azd ai cu analyzer delete invoice_v1 --yes --no-prompt
azd ai cu analyze invoice.pdf --analyzer prebuilt-invoice
azd ai cu analyze --source ./documents --pattern '*.pdf' -r -d ./results --report-file ./report.json
azd ai cu defaults show
azd ai cu defaults set --from-environment
azd ai cu doctor
azd ai cu env-var list
azd ai cu infra generate -d provision -e dev --models none
```

Help, version, and metadata do not require an endpoint, sign-in, or project.
Service commands require a CU endpoint, a resolved tenant, and azd sign-in.
For projectless use, set `AZURE_TENANT_ID`, run `azd auth login`, and pass
`--endpoint` or set process `CU_ENDPOINT`.
Hidden `metadata` output is generated from the runtime Cobra command tree.
The extension inherits azd global flags instead of defining a second set.

### Infrastructure and generated-project hooks

`infra generate` writes the canonical infra-only azd project: Bicep, parameters,
model input, `azure.yaml`, Bash/PowerShell hooks, and selected azd environment
state. It needs the azd host and subscription context, but no existing project
or CU endpoint. Generation itself never deploys Azure resources or models.
Defaults are `provision`, environment `dev`, region `eastus2`, GA API version,
model selection `prompt`, and no role assignments. Inherited `-e` selects the
generated environment; no conflicting extension-local environment flag is added.
With `--no-prompt`, the host must expose an unambiguous default subscription
(or a single available subscription). It never arbitrarily selects one.

```bash
azd ai cu infra generate -d provision -e dev --models recommended --no-prompt
azd ai cu infra generate -d provision --foundry-endpoint <account-name-or-ARM-ID-or-HTTPS-endpoint>
azd ai cu infra generate -d provision --foundry-prefix my-cu --assign-roles
cd provision
azd auth login
azd up
```

An existing resource is resolved with ARM under the selected subscription; its
region is used unless `--location` overrides it. A prefix and existing resource
cannot be combined. `--assign-roles` opts into the template's new-account
Cognitive Services User assignment and requires role-assignment permissions.
`--no-assign-roles` explicitly retains the default. Existing accounts require
an existing data-plane role. The extension is Entra-only; it never lists,
stores, or falls back to account keys.

A recognized generated project is reused without replacing editable assets,
models, unknown azd config, or custom environment lines. The legacy environment
completion marker is preserved during merge, but hook completion is tracked by
the selected environment's `cu.modelSetupComplete` boolean. `--force` replaces
known generated assets/state, clears the model file, and sets
`CU_CONFIG_SETUP_FORCE=true`; unrelated files are not deleted. Invalid reusable
state and unsafe output paths fail preflight before writes. Files are published
individually and atomically, not as a directory transaction; an I/O failure
retains completed files. Output includes resolved choices, file paths, reuse
status, and shell-quoted next steps.

The generated hooks invoke only `azd ai cu infra _postprovision-v1`; neither
Python nor standalone `cu` is required. The hidden `_models` compatibility hook
accepts `--resource-group`, `--account`, `--selection`, and `--out`. Its
`none` selection writes `[]` entirely offline, with no host/auth/catalog calls.
Other selections require the account to match the selected generated environment,
preventing cross-account CU-default writes.

Model policy ports the shared core: intersect `prebuilt-document` supported
families with the account's live ARM catalog, prefer GlobalStandard /
DataZoneStandard / Standard SKUs, retain explicit versions and capacities, and
reject ambiguous versions or duplicate families. `recommended` uses the existing
CU family preferences; `prompt` uses azd confirmations defaulting to No.
Unattended provisioning must use `none`, `recommended`, or explicit comma-separated
`model@version` selectors. Model discovery and deployment use the official ARM Go
SDK, API `2025-06-01`; Bicep assets retain their pinned resource API versions.
Deployment writes are not automatically replayed. Exact **succeeded** matches
are reused; conflicting or unfinished deployments are not overwritten/deleted.
All selected deployments are checked for conflicts before any create request.
Create requests include `If-None-Match: *`; concurrent ARM changes can still
require inspection and retry rather than being treated as successful.

Postprovision performs optional model setup before basic configuration.
Successful model setup records `cu.modelSetupComplete=true`; completed setup is
not repeated. `CU_DISABLE_AUTO_CONFIG=true` disables subsequent configuration,
not model setup. Existing analyzer/model configuration is preserved unless
forced. Required writes set `CU_ENDPOINT` and `cu.defaultAnalyzer=prebuilt-layout`;
model mappings are written as a whole JSON map so dots in model names remain
literal keys. Existing CU defaults are merged using the shared defaults helper.
Optional model/catalog/default failures are explicit safe warnings; required
configuration failures return nonzero without success-shaped stdout.
Cancellation always stops the workflow. Generative readiness requires succeeded
completion and embedding deployments **and** successfully configured defaults.
No live Azure deployment is performed by the local test suite.

Infrastructure assets are vendored from `cu-cli-core` revision
`25d39b30116a840491b7b6f393d6db792b38d4a4`; seven executable/configuration assets
are hash-checked against its golden fixtures. Only the bundled README is adapted
for native installation, Entra-only authentication, confirmation UX, and truthful
versioned-hook support. Intentional safety improvements over the Python prototype
include full conflict preflight, rejecting unfinished matching deployments,
scoped hook arguments, literal model-map keys, and cancellation propagation.
The CU data-plane adapter uses the pinned local prototype SDK described under
[CU client contract](#cu-client-contract). CLI/core workflows remain outside it.

### Analyzer commands

- `list` accepts `--kind all|prebuilt|custom` and
  `--sort-by analyzerId|createdAt|lastModifiedAt`. Filtering/sorting is local after
  all service pages have completed. Date sorting is chronological, missing dates
  sort first, and equal keys retain service order.
- `show`, `create`, and `delete` require exactly one analyzer name: positional,
  `--name`, `-n`, or `-a`. Positional and named forms cannot be combined.
- `create` requires `--schema` / `-s`. Custom IDs use 1-64 ASCII letters,
  numbers, or underscores, unlike prebuilt IDs, which can contain hyphens.
  Files are read relative to the inherited `--cwd`. Local UTF-8/JSON and blocking
  structural rules run before opening a service session. A schema's `apiVersion`
  must match the resolved API version. Unknown JSON properties and exact numbers
  are retained. Empty invalid containers are rejected, rather than being treated
  as absent; canonical properties take precedence over legacy snake-case aliases.
  Authoring warnings and the offline `analyzer validate` workflow are separate
  from create. Create never replaces an existing analyzer or retries a write.
- `delete` prompts through azd with a default of **No**, unless `--yes` / `-y`
  is supplied. `--no-prompt` (or `AZD_NO_PROMPT=true`) without `--yes` fails before
  configuration/service access. Declining confirmation cancels the operation.
  After confirmation, GET verifies existence before DELETE; a failed lookup never
  deletes, and a missing analyzer is an error.

### Analyzer schema authoring

`analyzer schema create` creates an offline starter by default; `--from-template`
makes that choice explicit. No project, endpoint, sign-in, host RPC, or service
request is needed. The API version is explicit `--api-version`, process
`CU_API_VERSION`, then GA. Template mode does not resolve an azd environment,
read credentials/model mappings, or modify service defaults.

`--name` / `-n` / `-a` defaults to `my_analyzer_v1` and uses the same custom-ID
rules as analyzer create. There is no positional name. `--modality` accepts
`document` (default), `image`, `audio`, and `video`, selecting the corresponding
prebuilt base unless `--base` overrides it. `--type extraction|classification`
defaults to extraction. Extraction includes the existing scalar, summary,
classification-field, and table examples; classification uses description-only
categories and enables segmentation, without creating dependent analyzers.
Templates match the shared Python core, including the editable model names
`gpt-5.2` and `text-embedding-3-large`; these are authoring defaults, not
deployment discovery or a claim that those models are deployed.

`--from-sample` sends exactly one regular local document to
`prebuilt-documentFieldSchema` using asynchronous analysis. This can incur CU
cost and requires the normal endpoint/tenant/azd-sign-in configuration. It
cannot be combined with `--from-template`, classification type, a non-document
modality, or a base override. Supported suffixes match `cu` / `az cu`: PDF,
TIFF (`.tiff`), Office/OpenDocument formats, EPUB, TXT, HTML, Markdown, RTF, XML,
JSON, CSV/TSV, KML, EML, and MSG. URLs, directories, images, audio, video,
archives, and the unsupported `.tif` alias are rejected before session access.

The first nonempty `schema.valueJson` object across content segments replaces
the example fields. Missing/null/blank descriptions receive recursive TODO
placeholders, including object properties and array items. Existing descriptions,
unknown properties, and exact large numbers survive unchanged. Empty suggestions
retain the default extraction template with an explicit stderr warning, matching
the standalone core behavior without adding a Go-only schema property. Service
warning presence is summarized on stderr; raw warning details are not echoed
into diagnostics or inserted into the analyzer document.

Default/JSON output is the complete schema. `--output-file` optionally saves
the same UTF-8 JSON through the shared atomic private-file writer; existing files
require `--force`. Sample aliases (including hard links), symlinks, special
files, and unsafe parents are rejected. Output checks run before service access
and again before saving. Service/preflight failures and cancellation observed
before publication do not save a schema or emit success-shaped stdout. Input
readers close before the service session. If stdout fails after a successful
file publication, the command returns an error but retains that completed file.
Inherited `-o none` suppresses successful stdout, not explicit file writes or
warnings. Review the result with `analyzer validate` before `analyzer create`;
schema authoring itself never creates or replaces an analyzer.

### Analyzer test

`analyzer test NAME [INPUTS...]` (or `--name` / `-n` / `-a`) evaluates local
sample files/directories using asynchronous analysis. Input modes are mutually
exclusive: positional inputs, repeated `--file`, or repeated `--source`.
Directory discovery, `--pattern`, `--recursive` / `-r`, ordering, and physical
file deduplication use the same implementation as `analyze`. URLs and
analyze-only options such as `--inline`, `--usage`, and result directories are
not accepted. An explicit analyzer name is required; no configured analyzer
fallback is used.

The complete structured report matches `cu` / `az cu`: `analyzerId`, `summary`,
and ordered `samples` with `ok`/`error` status, extracted field values/confidence,
and safe sample errors. Object children use `Parent.Child`; array-of-object
children use `Parent[].Child`. The first nonempty field across content segments
is retained. Zero/false values are populated; recursively empty containers are
not. Population percentages include failed samples in the denominator.
Confidence means round to three decimals, percentages to one, and low-confidence
counts use **strictly less than 0.6**. Missing confidence remains `null`.
Exact large integers are retained. **This is not an accuracy benchmark**;
the report includes the same explanatory disclaimer as Python.

For exact existing report parity, service warnings and usage are not included.
Adding warning output is deferred to a separate cross-frontend contract change;
this does not change warning preservation in `analyze` or analyzer definitions.
Default/JSON output is the full report, rather than standalone CU's summary
table. Use inherited `-o default|json|none`, not standalone `--json`.

`--dry-run` returns `{dryRun,analyzerId,inputs}` completely offline: no project,
endpoint, sign-in, host/configuration RPC, prompt, service request, directory
creation, or report write. It still validates inputs/report destinations and
cannot be combined with `--yes`. `--concurrency` / `-j` defaults to 4 and accepts
1-16. More than **50 samples** require confirmation, or explicit `--yes` / `-y`;
`--no-prompt` without `--yes` fails before session/service access.

`--output-file` saves only the structured report, not individual analysis
results. Existing reports require explicit `--force`; sample aliases,
symlinks/special files, and unsafe parent paths are rejected even with force.
Publication uses the shared atomic private-file writer. Partial failures return
nonzero without success-shaped stdout, while a requested report retains all
sample outcomes. Cancellation stops pending jobs, drains started requests and
closes readers, and retains a requested partial report. `-o none` suppresses
successful stdout, not analysis or report writes.

### Analyzer copy

`analyzer copy SOURCE DESTINATION` also accepts `--source` and `--destination`.
Specify each ID once; positional arguments retain source/destination slot order.
Without resource selectors, copy uses the configured CU endpoint and never calls
ARM. Identical analyzer IDs are rejected on the same resource, but allowed
between different resources.

For each side, `--source-resource` / `--destination-resource` accepts an account
name, HTTPS endpoint, or full Cognitive Services account ARM ID. Optional
`--source-subscription`, `--destination-subscription`, and corresponding
`--*-resource-group` flags narrow discovery. Full IDs override ambient scope;
conflicting explicit scope flags are errors. Subscription/group fallback comes
only from resolved process/selected-environment settings, never an unrelated
environment or an Azure CLI subprocess. Projectless name/endpoint discovery
requires a subscription setting or explicit subscription flag.

Resource groups require their corresponding resource selector. A destination
subscription also requires `--destination-resource`, rather than silently
ignoring the requested scope. Omit unused flags instead of passing empty values.
When discovery is requested, the source defaults to the configured endpoint;
an omitted destination resource means the **resolved source account**, not a
different configured endpoint. Exact account IDs or name/group pairs use
read-only ARM GET; other selectors drain bounded account-list pages and reject
missing/ambiguous matches. ARM uses the same tenant-bound azd identity with the
management audience, not the CU audience. Discovery never registers providers,
follows redirects, or forwards credentials to continuation URLs outside the
original account-list operation.

Cross-resource copy reads the source once, verifies custom content-category
analyzer dependencies at the destination, and checks that the destination ID is
absent. Missing dependencies must be copied explicitly; there is no recursive
copy. Only typed not-found responses count as absence: authorization,
throttling, and service failures stop the operation. The command then grants
authorization on the source and submits copy on the destination, including
source account/region coordinates. Grants are never printed or saved.

Copy never deletes or replaces an analyzer, including a destination created
between preflight and submission. A conflict recommends a new versioned ID or
explicit inspection/deletion **at the destination**. Failed results remain
available for inspection and are not silently cleaned up. Source/destination
authorization failures identify the relevant side; cross-resource copy
authorization guidance also covers expired grants. The existing CU transport
waits for completion, preserves warnings/unknown JSON, honors cancellation,
and never replays writes.

As in PR2, default human-readable output is pretty-printed, complete JSON rather
than a summary table. `-o default` and `-o json` preserve the same result shape:
a list array, a complete show/create/copy analyzer object (including service warnings),
or `{"deleted":true,"analyzerId":"..."}`. `-o none` suppresses successful result
output, not the operation, confirmation, or errors. An empty list is `[]`, not
`null`. Unknown properties and large integers survive serialization. No progress
messages are mixed into result stdout; debug lifecycle messages go to stderr.

All CU command metadata sets `launch.noAzdEnvironment`. The host therefore
does not inject persisted environment values or resolve unrelated Key Vault
references before launch; the extension reads its selected environment through
scoped RPCs. Inherited process inputs remain available. This preserves source
precedence and projectless execution, including offline help and metadata.

### Analyze

Input modes are mutually exclusive: positional files/directories/HTTPS URLs,
repeated `--file`, repeated `--source`, or repeated `--url`. `--pattern` requires
`--source` and matches the source-relative POSIX path using fnmatch-style
wildcards (`*` can span directories). `--recursive` / `-r` requires a directory.
Discovery sorts relative paths within each source, excludes hidden and generated
result files, skips hidden directories, and does not follow directory symlinks.
Explicitly selected files are not subject to those discovery exclusions.
Repeated direct arguments are errors; physical file overlaps, including hard
links, are deduplicated. Input modes, empty selections, output conflicts, and
existing-file policy are validated before opening the service session.

`--analyzer` / `-a` overrides the resolved default analyzer. There is no implicit
fallback analyzer. `--inline` / `-i` requires `2026-06-01-preview` and never falls
back to asynchronous analysis. Authentication remains Entra-only.

- A single input streams its complete terminal response envelope as JSON,
  including service warnings, usage when present, and unknown properties.
  It does not substitute the nested `result` object for the full response.
- `--output-file` saves exactly one result. `--output-dir` / `-d` preserves
  source-relative subdirectories. Without either flag, local batches write
  beside their inputs; batches containing URLs require an output directory.
  Full results use `.result.json`; LLM views use `.result.md`.
- Local result-name collisions receive deterministic SHA-256 suffixes.
  Remote name collisions are errors, not silently renamed outputs. Generated
  remote names exclude query/fragment data and fit within 240 UTF-8 bytes.
  HTTPS URLs are passed unchanged to CU, while references and reports redact
  queries and remove fragments. The extension never downloads remote inputs.
- `--on-existing error|skip|reanalyze` overrides process `CU_ON_EXISTS`, then
  defaults to `error`. `skip` makes no analysis request for existing results;
  `reanalyze` explicitly permits replacement. Invalid or empty policies fail.
- `--llm-input` implements the Python SDK's default LLM view: ordered YAML front
  matter, resolved fields and service metadata, warnings excluding internal
  `LLMStats:` telemetry, document page markers/classification routing, and
  multi-segment audio/video time ranges. YAML whitespace can differ from Python,
  but types, content order, and boundaries are retained. Unsupported field
  types fail explicitly rather than silently dropping extracted data.
  Files contain plain text; streamed LLM output is a JSON string.
- `--usage` wraps streamed output as `{"result":...,"usage":...}` and adds usage
  to successful per-input records. Absent usage is `null`; unknown counters,
  explicit zeroes, and exact large numbers are retained. Without the flag,
  successful records have `usage:null`; raw envelope usage remains untouched.
- `--concurrency` / `-j` defaults to 4 and accepts 1-32. Cancellation stops
  pending work and cancels in-flight requests. Binary readers close after each
  request. Attempt timeouts remain distinguishable from malformed responses,
  including partial bodies at a deadline. Result-publication failures also
  stop pending work.
- More than **50 selected inputs** require an azd confirmation (default No),
  unless `--yes` / `-y` is supplied. `--no-prompt` requires explicit `--yes`
  above that threshold. As in PR2, selected inputs include skipped results.
- `--dry-run` returns the analyzer and per-input destination/existing/skip plan,
  without CU calls, prompts, directory creation, or file writes. It still
  resolves environment configuration through azd; normal endpoint/tenant and
  analyzer requirements apply. It cannot be combined with `--yes`.
- `--report-file` saves a new `cu-cli/analyze-report/v1` report with counts and
  succeeded/failed/skipped records, including partial failure or cancellation.
  Reports never overwrite existing files. Input/result/report aliases and
  incompatible parent/child output paths are rejected before execution.
  Concurrent completions do not reorder summaries or reports: both retain
  input-selection order. Any failure returns nonzero, without success-shaped
  stdout; successful result files and a requested report remain available.

All local output surfaces use the same guarded writer: analysis results/reports,
schema files, analyzer-test reports, generated infrastructure/environment files,
and model files. It verifies directory ownership and permissions before creating
customer data, stages in a private operation-owned directory on the destination
filesystem, and publishes through rooted operations. No-clobber publication is
atomic; replacement requires the command's explicit overwrite policy.
Cleanup checks staging identities and never recursively removes a staging path
or deletes an unrelated replacement.

The current OS account and privileged administrators are trusted. Other accounts
must not be able to modify the output directory chain. Symlinks, Windows reparse
points, unsafe shared output directories, and unverifiable filesystems fail
closed without repairing existing permissions. A private child under a trusted
sticky temporary directory is supported. Newly created POSIX directories are
owner-only; published files are owner-only, with executable bits added to
generated shell hooks. Windows staging and published files grant access only
to the current account, SYSTEM, and Administrators; validated ancestor handles
are retained without delete sharing.

Linux permission checks include the effective POSIX ACL mask and require ext,
XFS, Btrfs, tmpfs, or overlay filesystems. macOS requires APFS/HFS and rejects
ACL mutation grants conservatively; private staging also rejects ACL read grants.
Windows requires a local fixed ACL-capable volume. Network/unsupported output
filesystems are not silently treated as private. POSIX directory handles follow
renames: these protections do not isolate a malicious same-account/admin process
or trusted-owner permission changes, and a post-write identity check is not a
guarantee against such relocation.

Diagnostics use bounded, redacted text, not raw library errors. Invalid arguments
do not echo their values; schema errors retain fixed constraint locations but
omit customer field paths. SDK debug logging emits only fixed event categories,
never raw SDK messages, URLs, headers, or bodies. Intentional stdout/file result
payloads, including extracted content and schema output, are not redacted.
`-o none` suppresses successful stdout, not result/report file writes.

### Offline analyzer validation

`analyzer validate SCHEMA` (or `--schema` / `-s`) runs without a project, CU
endpoint, credential, or host RPC. It does not create, modify, or analyze
anything. Explicit `--api-version` selects GA or preview; otherwise GA is used,
matching the shared Python local validator rather than loading service settings.

The default curated checks collect field-level structural errors and authoring
warnings, including missing/short descriptions, missing completion mappings, and
nesting deeper than four levels. `--strict` makes warnings fail validation.
Successful output retains the shared `ok`, `errors`, `warnings`, `strict`, and
`apiVersion` fields. Failures use the normal structured error channel, without
success-shaped stdout or rejected schema values in error messages.

`--spec` additionally validates against the embedded CU OpenAPI analyzer
**create-view** contract. It is truly offline: external references cannot be
loaded. A missing or invalid bundled contract is an explicit error, not a skipped
check. As in Python, curated checks alone are not full service-contract
validation; empty primary aliases follow the shared authoring fallback rules.
Spec validation always checks the original body.

The JSON files under `internal/schema/specs/` are generated from the shared
Python snapshot `25d39b30116a840491b7b6f393d6db792b38d4a4`. Each records its
source URL, original SHA-256, and MIT copyright/license. To regenerate, select
the versioned `ContentUnderstanding.json`, recursively retain definitions
reachable from `ContentAnalyzer`, remove read-only properties from each
definition's required list, and emit a Draft 4 schema referencing that
definition. Retain all other constraints and reject external references.
The normalized corpus in `internal/schema/testdata/validation.json` contains
40 cases generated from that snapshot's curated validator, compared by finding
paths rather than Python-specific prose. Neither regeneration nor validation
introduces a Python build or runtime dependency into the extension.

### Service defaults and diagnostics

`defaults show/set` retain the `cu`, `az cu`, and Python PR2 meaning: they read or
update **CU resource service defaults**, not local CLI settings.
No CU profile or local config writer is introduced. Manage local
configuration through `azd env select/set` and `azd env config get/set/unset`.

`defaults show` returns the complete service object, including unknown
properties and exact large numbers. Defaults that are not configured are an explicit error,
not an empty successful response.

`defaults set` accepts repeatable `--model MODEL=DEPLOYMENT`,
`--from-environment`, and `--replace`. Explicit mappings override
`cu.modelDeployments` when `--from-environment` is supplied; otherwise local
model mappings are not included. Repeated mappings for the same model use the
last value. Without `--replace`, existing service mappings are read and merged,
with desired mappings taking precedence. Only a specific `DefaultsNotSet`
response permits initialization; unrelated 404/auth/transport errors never
become an empty mapping. Writes are never retried.

As in PR2, `--replace` skips that **client-side merge**. CU's API is still a
merge patch: omitted service keys are not guaranteed to be deleted. The returned
service object is authoritative. This command does not delete deployments or
replace an entire resource. See the official
[update-defaults API](https://learn.microsoft.com/rest/api/contentunderstanding/content-analyzers/update-defaults?view=rest-contentunderstanding-2025-11-01).

Updates derive PR2's prebuilt completion, mini-completion, and embedding aliases.
The full-model preference is `gpt-5.2`; mini preferences are `gpt-5.2-mini`,
`gpt-4.1-mini`, then `gpt-4o-mini`; embeddings prefer `text-embedding-3-large`.
Other candidates use sorted model names, rather than map/property order.
Existing aliases are retained when no corresponding real model is supplied.
These are mapping-selection rules, not a promise of regional model availability
or automatic model deployment.

`doctor` resolves configuration, acquires Entra authentication through azd,
and checks CU access by reading defaults. It is read-only unless
`--fix-defaults` is explicitly supplied. That flag merges resolved
`cu.modelDeployments` over existing service mappings and derives aliases;
it never deploys models, provisions resources, or writes local configuration.
An empty mapping cannot be "fixed" into success.

Doctor preserves PR2's structured result and readiness rules:

- Missing generative mappings produce `ready:false` and explicit
  `missingRequirements`, with a successful diagnostic exit. This is not a
  connectivity/authentication failure; non-generative analyzers may still work.
- Configuration, authentication, service, and update failures use a nonzero
  exit and the standard host error channel, without success-shaped stdout.
- Projectless missing-tenant/context errors are distinct from invalid endpoint
  or API-version errors. Remediation points to process inputs, `azd auth login`,
  and `azd env`, never to CU profiles.
- The legacy JSON field `profile` remains for PR2 compatibility. It labels the
  **resolved azd environment** or `projectless`; it does not refer to a CU profile.
  `defaultAnalyzer` is `null` when unset, and empty collections remain arrays or
  objects rather than `null`.

### Environment-variable inspection

`env-var list` works without a project, endpoint, or host RPCs. It lists only
currently set, supported process inputs, as `{name,value,scope,sensitive}`
records. It does not dump selected-environment values or unrelated variables.
`CU_API_KEY` is always `********`, including when empty, and remains unsupported.
An invalid `CU_ENDPOINT` is shown as `[invalid endpoint]`, not echoed with URL
credentials, SAS queries, or fragments. Host access tokens, client secrets,
standalone telemetry/update variables, and unrelated variables are not listed.

| Process variable | Accepted values / default | Scope and precedence |
| --- | --- | --- |
| `CU_ENDPOINT` | HTTPS resource endpoint / unset | Explicit `--endpoint` wins; then process, selected `CU_ENDPOINT`, selected `FOUNDRY_ENDPOINT`. |
| `CU_API_VERSION` | `2025-11-01` or `2026-06-01-preview` / GA | Explicit `--api-version` wins; process precedes selected config/environment values. |
| `CU_DEFAULT_ANALYZER` | Analyzer ID / unset | Explicit `--analyzer` wins; process precedes selected config/environment values. |
| `CU_AUTH_MODE` | `login` or unset | Entra-only; non-login modes fail before service access. |
| `CU_API_KEY` | Unsupported | Presence, even empty, blocks service execution; always redacted by inspection. |
| `CU_ON_EXISTS` | `error`, `skip`, `reanalyze` / `error` | Used by analyze only when `--on-existing` is absent. |
| `AZD_ENVIRONMENT` | Existing environment name / unset | Inherited `--environment` wins; otherwise select the default environment or projectless mode. |
| `AZURE_TENANT_ID` | Azure tenant ID / unset | Process precedes selected environment; required for projectless service execution. |
| `AZURE_SUBSCRIPTION_ID` | Azure subscription ID / unset | Process precedes selected environment; embedded ARM scope is authoritative. |
| `AZURE_RESOURCE_GROUP` | Resource group name / unset | Process precedes selected environment; embedded ARM scope is authoritative. |
| `AZD_DEBUG` | azd boolean / false | Inherited `--debug` overrides the SDK's process fallback. |
| `AZD_NO_PROMPT` | azd boolean / false | Inherited `--no-prompt` overrides the SDK's process fallback. |
| `AZD_CWD` | Working directory / unset | Inherited `--cwd` overrides the SDK's process fallback. |

`FOUNDRY_ENDPOINT` is a selected-environment fallback, not an independent process
override. Structured settings remain `cu.apiVersion`, `cu.defaultAnalyzer`, and
`cu.modelDeployments`; credentials must never be stored in that section.

## Development

| Command | Description |
| ------- | ----------- |
| `azd x build` | Build the extension and install it locally. |
| `azd x watch` | Watch for changes and automatically rebuild and reinstall. |
| `azd x pack` | Package the extension into a distributable artifact. |
| `azd x publish` | Publish the extension to an extension source. |
| `azd x release` | Create a GitHub release for the extension. |

Learn more about building extensions in the [azd extension framework docs](https://github.com/Azure/azure-dev/blob/main/cli/azd/docs/extensions/extension-framework.md).

### Validation

Use the Go version declared in `go.mod`.

```bash
go test ./... -count=1
go test -race ./... -count=1
go fix ./...
golangci-lint run ./...

cd sdk/azcontentunderstanding
go test -race ./... -count=1
cd ../..

EXTENSION_ID=azure.ai.content-understanding \
EXTENSION_VERSION=0.0.1 EXTENSION_PLATFORM=linux/amd64 bash ./build.sh

# CI unit-test entry point, where PowerShell is available:
pwsh ./ci-test.ps1
```

Metadata and version use Cobra's output writer rather than the SDK convenience
commands' process-wide stdout, allowing output failures and stream separation to
be tested without replacing process streams. The metadata generator and global
flag configuration still come from `azdext`.

Runtime context is initialized lazily by commands that need the azd host. It
requires the host server and access token, preserves cancellation, and constructs
only an azd-backed credential; it never falls back to another Azure identity.
Debug diagnostics contain static lifecycle events, not token, configuration,
request, or raw error values, and go to stderr.

The extension reports failures through the standard `azdext.Run` host channel.
When run directly, failures use stderr and a nonzero exit status. The current azd
host renders reported extension errors to stdout; consumers must check the exit
status before parsing output. Strict host-level error stream separation would
require a shared azd change, not a CU-specific error-reporting bypass.

The scaffold currently lists `@chienyuanchang` and `@JeffreyCA` as provisional
CODEOWNERS. Confirm CU/azd ownership and extension identity before merging.
API-key requirements, performance budgets, and Foundry pack timing remain
implementation/release gates.

### Configuration resolution

The runtime resolves configuration before constructing the azd-backed credential.
Analyzer commands expose `--endpoint` and `--api-version`; remaining settings
come from the supported environment/config sources.
No global CU profile or user-config store is introduced.

| Setting | Highest to lowest priority |
| ------- | -------------------------- |
| Endpoint | Explicit option; process `CU_ENDPOINT`; selected environment `CU_ENDPOINT`; selected environment `FOUNDRY_ENDPOINT`. |
| API version | Explicit option; process `CU_API_VERSION`; environment config `cu.apiVersion`; environment `CU_API_VERSION`; `2025-11-01`. |
| Default analyzer | Explicit option; process `CU_DEFAULT_ANALYZER`; environment config `cu.defaultAnalyzer`; environment `CU_DEFAULT_ANALYZER`. No implicit analyzer is selected. |
| Model deployments | Explicit mapping; environment config `cu.modelDeployments`; empty mapping. |
| Tenant | Explicit option; process `AZURE_TENANT_ID`; selected environment `AZURE_TENANT_ID`; default deployment context only when no environment was explicitly selected. |
| Subscription/resource group | Explicit options; process `AZURE_SUBSCRIPTION_ID` / `AZURE_RESOURCE_GROUP`; selected environment values; default deployment scope when needed to obtain the default environment's tenant. |

Environment selection is the inherited `--environment` option, then process
`AZD_ENVIRONMENT`. Without either selection, a runtime endpoint (explicit or
process `CU_ENDPOINT`) uses projectless mode; otherwise the runtime loads the
default environment. Named environments use scoped values and the scoped `cu`
config section, never the default environment's deployment context. Environment
values are read as persisted values, not host-side process/environment mixtures.
An environment/deployment read failure is an error, not a projectless fallback.

Projectless service execution requires both a runtime endpoint and tenant
(explicit inputs or the compatibility process variables above), plus any
command-specific required inputs. It does not require a project or prior
deployment, but still uses the azd host and azd authentication. A missing tenant
does not trigger a lookup in another environment or a different credential chain.

Full Cognitive Services account ARM IDs use their embedded subscription and
resource group instead of ambient scope. Conflicting explicit scope options fail;
matching scope options are compared case-insensitively. Account endpoint discovery
and ARM network operations are separate command-layer work.

`cu.apiVersion` and `cu.defaultAnalyzer` must be nonempty strings;
`cu.modelDeployments` must map nonempty model names to nonempty deployment names.
Invalid selected values fail instead of silently falling through to a lower
priority source. Higher-priority runtime options override lower-priority settings.
Configuration is read-only; the resolver writes neither process variables nor
azd configuration.

Only Entra authentication is enabled. `CU_AUTH_MODE=login` is accepted, while
other modes and process `CU_API_KEY` fail explicitly. Plaintext `CU_API_KEY`
in the selected environment and `cu.apiKey` (including legacy `api_key`) in its
config are rejected even when runtime options override other settings. Empty
persisted key entries must also be removed. Secrets are never included in resolved
configuration, diagnostic logs, or errors.
The PR2 `--auth-mode` and `--api-key` options are intentionally not exposed while
the API-key flow remains undecided.

### CU client contract

`internal/cuclient` defines an extension-owned service interface for analyzer
list/get/create/delete, same-resource and cross-resource copy, service defaults,
and binary/URL analysis with asynchronous or inline completion. Every operation
accepts context. The adapter owns paging and polling; command code owns prompts,
filesystem I/O, resource selection, and copy preflight/orchestration.

The contract includes typed diagnostics, optional usage counters, and stable
validation, not-found, conflict, authentication, throttling, limit, cancellation,
timeout, and service error categories. Domain types have no azd or Azure SDK
dependency. Full analyzer/result JSON is retained alongside typed projections to
preserve dynamic schemas, multimodal fields, and full-result output.

The agreed API baseline is `2025-11-01` by default, with explicit
`2026-06-01-preview` coverage. `SDKTransport` adapts the local
`github.com/Azure/azure-sdk-for-go/sdk/contentunderstanding/azcontentunderstanding`
module without exposing SDK types to commands. The module is copied from
`Azure/azure-sdk-for-go#27810` commit
`8ff358e8d3410d9bc96b9ed79487a0cad0252fac` and consumed through an intentional
relative `replace`. See `sdk/azcontentunderstanding/AZD_PROVENANCE.md` for its
REST-spec provenance and local changes to back-port after azd ships.

The contract is based on the [public CU TypeSpec and versioned OpenAPI specifications](https://github.com/Azure/azure-rest-api-specs/tree/56b13af91ff8d8b5ad706a502e6b4e44e6d57e1f/specification/ai/data-plane/ContentUnderstanding).
Inline analysis is preview-only; the adapter rejects it locally with the GA
version. Contract tests cover all 12 SDK operations, both versions, and supported
input/mode combinations. Domain input validation alone does not claim wire or
live-service conformance.

### Authenticated SDK runtime

`internal/cuclient.SDKTransport` implements the extension-owned `Client`
interface over the local SDK. It accepts an injected azd-backed
`azcore.TokenCredential` and uses
the `https://cognitiveservices.azure.com/.default` scope without selecting a second
tenant or identity. The SDK's key constructor is not exposed by the extension.

- Endpoints require HTTPS, with no embedded credentials, query, fragment, or
  operation path. A trailing `/contentunderstanding` is normalized once.
- Only the two agreed API versions are accepted; the default remains `2025-11-01`.
- Requests carry the extension/version user agent, JSON accept header, a unique
  client request ID, and azd trace correlation when available.
- Reads and polling allow at most two retries under the Azure pipeline policy.
  Write submissions override the policy to disable automatic retries.
- The default timeout is two minutes per HTTP attempt and can be overridden
  internally. Caller cancellation propagates to token acquisition, requests, and
  response reads. The default HTTP client does not follow redirects.
- Error response bodies are bounded to 64 KiB before `azcore` can buffer them.
  Errors expose HTTP classification and a syntactically validated service code,
  not raw service messages, bodies, or authorization values. Oversized/malformed
  bodies still return an explicit HTTP failure. Valid `Retry-After` values are
  retained with a maximum of 24 hours.
- Failed service and polling envelopes are converted to sanitized
  errors without logging service bodies, request URLs, schemas, paths, or customer
  content. Terminal `OperationError` values preserve status and safe nested codes
  and wrap `azcore.ResponseError`; the adapter retains CLI cancellation categories
  and operation-specific failure messages while keeping actual HTTP status.

Tests use fake credentials/transports and local HTTPS servers. They do not call
Azure or incur service charges. Command tests additionally use injected clients
and local azd gRPC servers for request mapping, golden output files, confirmation,
cancellation, metadata launch policy, and safe failures.

### Analyzer SDK operations

The SDK adapter implements list/get/create/delete, copy authorization,
same-resource and cross-resource copy, and service defaults for both agreed API
versions.

- Listing drains every page in service order. Filtering/sorting belongs to
  commands. A failed or malformed page is an error, not a partial list.
- Continuation links must remain on the exact configured HTTPS origin.
  Relative links are supported; repeated and credential-bearing links are
  rejected, and the selected API version is applied consistently.
- Create and copy explicitly send `allowReplace=false`; no conditional headers or
  automatic write replay are used. Copy source lookup, destination preflight, and
  cross-resource grant orchestration belong to commands.
- Create/copy follow validated analyzer operation locations until completion.
  Pending polls use a one-second interval unless the service supplies a positive
  `Retry-After`. Cancellation/deadlines interrupt both waiting and HTTP requests.
  Success without an embedded result triggers the final analyzer GET.
- Analyzer and defaults JSON are retained without reconstructing schemas or
  rounding numbers.
  Warnings retain nested codes, messages, and targets. Operation failures expose
  safe codes and redacted nested diagnostics, not raw service error text.
- Optional usage distinguishes absent counters from zero. Usage is taken from
  the completed operation response.
- Delete uses the service's idempotent semantics. The command first checks
  existence so a missing analyzer is reported as an error.
- Defaults update uses `application/merge-patch+json`. The service returns the
  resulting mapping; an empty mapping is not a request to clear existing entries.

### Analysis submission and completion

- Async URL analysis sends one `inputs` entry to `:analyze`. Binary analysis
  streams the caller-owned reader to `:analyzeBinary`, without base64 encoding
  or downloading URL inputs in the extension.
- Binary content defaults to `application/octet-stream`. An explicit MIME type is
  validated and sent as the binary content-type header or the URL input's
  `mimeType`, respectively.
- Preview inline analysis uses `:analyzeInline` or `:analyzeBinaryInline` and
  requires HTTP 200 with a terminal result. GA inline requests fail before input
  I/O or authentication; there is no automatic fallback to async.
- Async submission requires HTTP 202 and a validated operation location on the
  configured HTTPS origin. Polls
  share the analyzer polling implementation, safe GET retries, `Retry-After`, and
  cancellation/deadline behavior. The POST is never automatically replayed.
- `Succeeded` with warnings is a completed partial result, not a failure.
  Warnings and preview infos retain nested diagnostic data. Unknown statuses,
  missing results, malformed content, and mismatched operation/result identities
  fail explicitly.
- The adapter does not enable input truncation or override analyzer settings.
  Range, truncation, and other optional REST parameters outside the current
  request contract are not added implicitly.
- `AnalyzeResponse.JSON` retains the exact terminal response envelope, including
  status, result, usage, and service-only fields, matching the Python CLI's
  full-result output. `Result.JSON` retains the nested analysis result; each
  `Content.JSON` and dynamic field also retains its original JSON. Typed
  projections do not round large numbers or discard multimodal details.
- Optional usage comes from the terminal envelope. Missing usage/counters remain
  distinct from explicit zero. Input readers remain caller-owned on success,
  cancellation, and failure; the Azure pipeline may rewind and consume them.

Analyzer/analysis wire and polling conformance tests construct the production
`SDKTransport`; low-level REST pipeline/helper tests remain reference coverage.
The local SDK unit suite and adapter tests cover the supported
version/input/mode matrix, exact raw JSON, failures, polling, no-replay, stream
ownership, and SDK log redaction. The test suite makes no live Azure calls.
Prior prototype live evidence covers GA/preview URL, binary and base64 analysis,
preview inline behavior, analyzer lifecycle and same-resource copy. Successful
cross-resource authorization/copy and defaults mutation remain live-validation
gaps with offline contract coverage.

### Initial source-pinned parity and recorded playback

<!-- cspell:ignore parityrelease -->

`testdata/parity/pr2_test_inventory.yaml` contains all 797 collected source cases
from 65 Python/shared-core/Azure-adapter modules at commit
`25d39b30116a840491b7b6f393d6db792b38d4a4`, all 29 capability dispositions, and
27 playback/live/hygiene scenarios. Normal CI validates the immutable source-case
snapshot, unique rows, command presence, declared mapping states, existing Go
test references, and imported fixture digests without a sibling checkout or
Python runtime. A changed source revision requires deliberate re-collection,
review, and updating the pinned snapshot/counts, not automatic enrollment.

States are `pending`, `evidenced`, and `approved`. An evidenced mapping records
tests plus an equivalent/different/not-applicable rationale; it is not release
approval. Approved rows require an explicit review reference. Currently 344
source cases have recorded-command, hygiene-replacement, manifest-structure or
normalized-adapter/generated-project/core-binding/input/output-planning/schema/analysis evidence,
453 remain pending, and none is approved. Passing the structural checks does
not mean parity is complete.

All nineteen source recordings are imported under `testdata/playback/`:
`defaults_get`, `analyzer_list`, `analyze_single`, `analyze_batch`, and
`analyze_prebuilt_invoice_json`, `analyzer_create`, `analyzer_show`, and
`analyzer_delete`, and `analyzer_test`, plus the eight template/classifier/route
recordings and `doctor`/`schema_suggest` described below. Their source commits, raw/imported digests,
ordered source interaction indices, omitted duplicate interactions, MIT license, and
normalization records are retained. Defaults deployment names are synthetic.
Playback JSON is checked out with LF endings so its provenance digest is stable
on Windows as well as Linux/macOS.
The list retains all 88 public prebuilt definitions. The analysis and analyzer-test fixtures
retain only results from the repository-provided public Contoso invoice sample;
its path/digest and review disposition are recorded. No customer extraction
output or uploaded binary data is included.

`TestRecordedServiceCommands` exercises defaults show and analyzer list
(default/custom/created-time scenarios), all three output modes, and full JSON
results through actual production `SDKTransport` and the command boundary.
Only the session/credential are injected: this is not installed-host or live
Entra evidence. Request matching checks method, origin, escaped path, complete
query, relevant headers and semantic JSON bodies with exact numbers. Unexpected,
repeated or unconsumed interactions fail explicitly; the injected transport has
no real-network fallback. Python user-agent/request IDs are not contract matches.
Hygiene checks supplement, not replace, manual fixture review.

`TestRecordedAnalysisCommands` adds three analysis scenarios in all three output
modes, including full terminal envelopes and saved directory results. Fixture
schema v2 carries ordered interactions and body modes. All 7/6/42 interactions
are preserved, including every repeated Running poll; Go virtual time executes
the real SDK polling loop without wall-clock sleeps or production timing changes.
Scrubbed binary requests must match explicit synthetic test bytes, never an
unconstrained wildcard, and do not prove original PDF uploads. Synthetic polling
mutations reject wrong operation IDs/origins/API versions/query values, altered
terminal IDs, extra/unconsumed polls, and mismatched/missing binary expectations.

All three source analysis recordings are binary, not URL input. The separate
`TestRecordedAnalysisSyntheticURLContract` checks URL JSON submission with recorded
responses; it is explicitly synthetic and does not claim URL service evidence.
The source dual-host case labelled `analyze-prebuilt-invoice-json` actually calls
the layout/single-file runner; its ledger mapping follows those real assertions.
The separate Azure-adapter invoice case maps to the actual invoice recording.
Scenario names alone are not evidence of LLM rendering or invoice analyzer use.

`TestRecordedAnalyzerLifecycle` replays both distinct public basic/template
definitions in all three output modes. All 11 create interactions and two show
interactions are retained; the delete fixture retains GET preflight/empty-204
DELETE and explicitly dispositions one duplicate frontend DELETE. The SDK uses
the analyzer embedded in Succeeded instead of redundantly fetching it: source
final GETs are exercised as explicit show commands, while a separate synthetic
missing-result test verifies the SDK final-GET fallback. Full definitions,
including effective models and nested fields, are compared without filtering.

Source PUT JSON was itself scrubbed. JSON matching therefore requires explicit
synthetic expectations, not a wildcard or a claim of original request-byte/model
submission proof. Basic input follows the source test schema; alternate template
input is reconstructed from the public definition. An explicit
`allowReplace=false` query is a declared native SDK difference from the omitted
source query. Synthetic failures cover JSON mismatches, replacement queries,
operation origin/identity, missing final GET, invalid final analyzer/status and
non-retried write errors; no real analyzer is created or deleted.

The source `schema-template-validate-lifecycle` scenario actually repeats the
basic lifecycle, without template generation or validation. Its mapping follows
those actual assertions; the separate template/classifier scenarios and template
generation/validation coverage are supplemented below. All fourteen recorded
source scenario dispositions are evidenced, not fourteen distinct or
release-approved workflows.

`TestRecordedAnalyzerFieldReport/recorded` replays all 45 analyzer-test
interactions, including 43 Running polls, in all three output modes. The entire
saved/stdout report is compared with an independent oracle generated offline by
both pinned Python frontends and asserted identical. It covers all 56 flattened
fields, values, nullable confidence, population/mean/low-confidence statistics,
threshold and disclaimer, not only a selected field or nonempty confidence.
Source-code hashes and the Python/SDK/core runtime versions are retained.
Only the asserted input path and integral JSON floating-point representation
are normalized; Go CI needs neither Python nor a sibling checkout.
Separate synthetic warning/usage and terminal-failure variants verify report
omission rules, safe failure text and saving the failed report before returning
an error. They do not claim recorded warning/error evidence. The scrubbed upload
still matches synthetic bytes, not the original PDF.

`TestRecordedTemplateClassifierLifecycle` adds eight recordings and retains all
22 source interactions for extraction-template and classifier/route lifecycle workflows.
Offline schema creation compares complete stdout/saved documents with the
existing source-pinned Python template oracle; strict/spec validation requires
zero findings and neither command opens a service/host session. Classifier
configuration preserves all four categories and explicitly adds prebuilt-invoice
and custom-route references. The route is created first and deleted last.
Full service definitions, effective models/configuration and empty-204 deletes
are checked in all three output modes. Fresh local schema names and the recorded
extraction response's older schema name are preserved independently, not
normalized to hide their difference.

The recorded classifier PUT already returns a complete ready resource. Native
create correctly consumes that response without the source's extra operation
GET. `TestRecordedClassifierSyntheticPendingStart` retains/exercises that poll
under a labelled synthetic creating start; source final-resource GETs remain
explicit show commands. Source PUT bodies are scrubbed, so exact synthetic
template/route expectations and the declared no-replacement query are not proof
of original submitted bytes. The route input follows the Azure helper; the
source dual-host helper uses a shorter vendor description. Configuration
lifecycle playback does not establish actual document classification/routing
or live dependency enforcement.

`TestRecordedDoctorCommands/healthy` compares the full read-only report with
independent Python frontend results in all three output modes, for projectless
and selected-environment contexts. Preserve every model/alias key with synthetic
deployment values. Source Azure/Python azd auth and profile labels differ;
native explicitly reports Entra and azd context, with the resolved endpoint
without a trailing slash. No fields are dropped to obtain equality. Separate
synthetic cases distinguish empty/unset generative defaults (ready:false) from
an unrelated resource-not-found error, and verify safe diagnostics.

`TestRecordedSchemaSuggestion/recorded` retains all 51 interactions and 49
Running polls, comparing complete saved/stdout schemas against independent
Python frontend results with all 20 fields, nested descriptions, models and
configuration intact. Source Azure's profile selects gpt-4.1 while Python azd
and native use gpt-5.2; both full source results are retained and cross-checked
for that sole schema difference, not filtered. The original JSON `:analyze`
request is explicitly adapted to native PDF `:analyzeBinary`, with strict MIME
and synthetic-byte matching. Responses remain unchanged; this does not prove
the original JSON upload or recorded native binary-service behavior. Synthetic
warning/usage and empty-suggestion variants verify safe warning text, complete
output and the independent Python starter fallback.

`TestRecordedHygieneScenarios` maps all 11 exact pinned source hygiene cases,
including apostrophes in URL paths/query values and four UTF-8 byte-body cases.
The source-executed full raw/scrubbed controls and source-code hashes live
in `testdata/parity/hygiene_scenarios.json`, pinned by the ledger's SHA256.
These synthetic unsafe controls are not service recordings or a twentieth
cassette. UTF-8 bytes are canonicalized to artifact text only after an exact
round-trip check; this does not prove in-memory byte-return API equivalence.

Python scrubs during recording; native has no recorder and instead rejects
unsafe artifacts during import review/CI. Native deliberately also rejects
REDACTED credential markers; reviewed imports omit that metadata. Each case
rejects the exact source raw and scrubbed artifacts and accepts a separate
reviewed native control. Independent single-marker tests cover all seven
sensitive headers, all 18 sensitive query keys (including blank/REDACTED
values), customer CU hosts, encoded/nested URLs and malformed queries without
depending on a second leak to fail. This is an evidenced but unapproved
stricter replacement, not an automatic scrubber or a production logging change.

The 19 per-cassette Python artifact-hygiene cases map separately to exact
`TestRecordedFixtureHygiene/<cassette-name>` references and
`TestRecordedSourceArtifactMarkers`. Besides all source private markers, these
checks reject quote/whitespace-separated secret values at the source's
eight-character boundary, including Python Unicode whitespace and special
case-folding behavior. Native intentionally also rejects REDACTED values.
Independent nested-value controls exercise each marker without another leak;
ordinary public controls remain accepted. The ledger rejects missing fixture
subtests or invented output-format suffixes. Go checks the reviewed normalized
JSON imports and their pinned original-YAML hashes, not original YAML contents
in CI; source pytest checks the originals separately. All 19 imports remain
unchanged, and these stricter artifact dispositions still require approval.

Four additional source helper cases have explicit replacement evidence:
three exact raw/scrubbed controls under `TestRecordedScrubberHelpers` and the
complete/sanitized recording-set check under `TestRecordedImportCompleteness`.
The schema-v2 hygiene control asset retains all 11 original scenarios, the
three helpers, three source-code hashes and all 19 expected source YAML names.
Verify every helper oracle assertion before rejecting raw and REDACTED
artifacts and accepting a separate reviewed native control. The bytes helper
adds one UTF-8 artifact-text adaptation, not an in-memory bytes-return API.

Compare the expected source set with both the inventory and actual imported
JSON files; reject missing/duplicate/extra entries. Every imported request
must use the exact placeholder HTTPS origin, without user information or a
port. Negative controls also reject other hosts, lookalikes and malformed URLs.
These checks supplement full provenance/digest/header/response hygiene; they
do not turn native import rejection into Python's recording-time scrubber or
make normalized JSON checks equivalent to checking original YAML in Go CI.

All 29 source scenario-manifest cases have separate structural evidence.
`testdata/parity/source_scenarios.yaml` is a byte-for-byte copy of the pinned
source manifest, with source path/MIT provenance, an immutable digest checked
in code and the ledger, and LF checkout policy. Compare the ledger's ordered
five-field projection (ID, name, kind, mode, recording list) exactly against
that source. Review state and approval are native metadata outside this
contract. Preserve shared cassettes across scenarios, but reject renamed,
reordered, duplicate or incorrectly associated scenario/recording entries.

The native tests retain all 27 exact per-name contracts, 16 command/11 hygiene
counts, 14 playback/two live-only/11 offline counts, and the exact union of 19
inventory/on-disk cassette identities. Twenty-one negative controls cover
source-reference and manifest drift, including missing cassette references
even when a scenario is pending. Checked subtest references reject unknown
names and invented formats. These are manifest facts only: structural checks
for the two live-only rows do not execute copy, and hygiene-row shape checks
do not prove a native scrubber. Existing scenario evidence and approval states
are unchanged.

The three source normalized-adapter tests also have exact checked mappings.
`testdata/parity/normalized_adapter.json` retains complete equal Azure/Python
azd observations and fake-client calls from executing the original tests,
nine source-code hashes, runtime/MIT provenance and declared host differences.
`TestNormalizedAdapterParity` delegates to production `SDKTransport`; it captures
actual SDK results/name operands and projects parsed local kind/sort/yes flags.
This is a test-only typed request projection, not Python request-builder
implementation equivalence. Strict synthetic HTTP responses derive from the
source fake-client calls; this asset is not a twentieth service recording.

Source list/get fake models omit `status`, which existing native wire validation
requires. Preserve the original source objects and inject only `status: ready`
into the separately declared native wire models and complete expected native
list/backend results. Negative tests verify the unadapted source models fail
with `malformed_response`, without a subsequent DELETE or output. Do not weaken
production validation to accommodate source mocks.

All three mappings are unapproved differences. For conflicting delete operands,
retain the source `usage` category/message/null hint versus native `validation`,
safe message/actionable hint and `conflicting_analyzer_name` code. Compare full
requests/results/warnings/errors and backend order across 24 default/explicit
flag, positional/alias and output-mode invocations. Default/JSON output is
compared in full; quiet output has a separate no-output contract. Conflict
validation must occur before session/credential/HTTP/prompt activity. These
offline tests do not establish live authentication, RBAC or installed hosts.

Four generated-project source cases also have checked evidence for the core
and Azure Python origins: complete golden trees and actual Bash delegation.
`testdata/parity/generated_projects.json` retains all ten source path/hash
expectations, canonical choices, observed Bash arguments, four source-file
hashes and MIT/runtime provenance. Both labels exercise the same native
`infra.Materialize`, not separate native frontends. Compare the complete
written/on-disk file sets, source CRLF-to-LF hashes and the Bash owner-execute bit.

Keep two explicit tree differences: native Go/Entra-only README content and
Go's sorted JSON config keys versus source insertion order. Pin their native
hashes alongside the original source hashes; additionally compare the entire
parsed config JSON, without selecting keys. Both tree mappings remain
unapproved differences. Execute the generated Bash hook against a local fake
azd and compare full ordered arguments with both source observations and the
canonical hidden command; those two mappings are equivalent but unapproved.
PowerShell source cases remain pending because `pwsh` is unavailable. POSIX
fake-host execution is not installed-host/live or Windows runtime evidence.

All 31 source capability-disposition manifest tests also have structural
evidence. `testdata/parity/source_capabilities.yaml` is the byte-identical
source YAML, pinned by an immutable code/ledger digest with source path/MIT
metadata and LF checkout. Compare all four source fields in order: ID,
source-path list (including aliases), target and classification. Check IDs
1-29, the 16/11/1/1 classification counts and unique source paths/extension
targets. Keep native review state/approval outside this source projection.

Twenty-four negative controls reject source-reference, row/order, source-path,
alias, target and classification drift; checked row references reject unknown
IDs, padded IDs and invented output formats. Four controls reproduced prior
validator acceptance of incorrect paths/targets/alias order before the new
checks were wired into normal CI. Metadata-independence tests allow legitimate
review updates without changing any source facts. This proves manifest
structure only, not runtime dispositions or host-helper/update workflows.
All 29 capability approvals and the separate runtime/core source cases remain
pending. Existing cassette/scenario evidence states are unchanged.

Five exact core analyzer-show binding tests now have command-path evidence.
`testdata/parity/core_show_bindings.json` retains seven complete original
observations: parsed inputs, bound/typed results, Python result types and
exception type/messages, plus four source hashes and positional-classification
provenance. Execute the original assertions when producing the oracle.

`TestCoreShowBindingParity` runs 45 positional/name/-n/-a/output variants through
actual Cobra commands and production `SDKTransport`. A delegating observer
projects the resolved SDK name into the complete source name request, including
trimmed named input. Strict synthetic GETs compare full SDK results and
default/JSON output; quiet output is separately empty. Source core tests make
no HTTP calls and produce no analyzer definition: native response data is
explicitly synthetic, not another service recording.

Retain source dictionary/typed-request class identity and standalone-shortcut
classification as source facts, not Go implementation identity. Preserve raw source binding
exceptions separately from native safe validation category/message/hint/code.
Conflict/missing-name errors must precede any session, credential, HTTP or
prompt activity and produce no output. All five mappings remain unapproved
differences. No live/installed-host/authentication equivalence is established.
Also, the 29 source tests named runtime-disposition evidence only check registry
membership; their names are not evidence of service execution.

The shared core delete-binding parameter adds one source mapping, not twelve.
`testdata/parity/core_delete_binding.json` retains both original complete
bindings, result types, four source hashes and classification/MIT provenance.
`TestCoreDeleteBindingParity` runs 12 native name/positional/alias/output
variants, reusing the analyzer-form generator and delegating name observer.
Verify actual SDK names and full GET model followed by empty-204 DELETE,
complete bound-name projection and full deletion acknowledgement. Explicit
`--yes --no-prompt` must avoid prompting; quiet output is separately empty.
Source core binding performs no HTTP, confirmation or CLI result generation:
the native analyzer/acknowledgement are synthetic. Type/surface/confirmation/
host seams remain an unapproved difference, not live deletion evidence.

The shared core create-binding parameter adds one source mapping, not 24.
`testdata/parity/core_create_binding.json` retains both complete original
parsed/bound observations, dictionary/path types, four hashes and source
classification/MIT provenance. Source binding accepts `invoice-v1` but never
reads `schema.json`, validates a custom ID or calls HTTP. Native custom-create
validation rejects hyphens: `TestCoreCreateSourceNameRejected` verifies all 12
original-name variants fail before session creation, even without a schema
file. Production validation is unchanged.

`TestCoreCreateBindingParity` uses only the explicit synthetic `invoice_v1`
adaptation for 12 successful positional/name/-n/-a/output variants and both
schema flag spellings. Read a complete synthetic schema from a private absolute
path containing spaces; check the exact parsed path before projecting only
that fixture path to source `schema.json`. Compare actual SDK name/definition,
strict no-replacement PUT JSON including unknown metadata/exact integer,
one immediate-ready response and full native output; quiet output is empty.
Schemas/service results are synthetic, not source results or a new cassette.
ID/path/type/host differences remain unapproved; no live-create claim.

The shared analyzer-test binding parameter adds one source mapping, not 24.
`testdata/parity/core_test_binding.json` retains both complete parsed/bound
observations, dictionary types, four hashes and classification/MIT provenance.
`TestCoreAnalyzerTestBindingParity` verifies unchanged `invoice-v1` and parsed
default concurrency 4 without overriding the flag. Its 12 successful native
variants add one explicitly synthetic sample under a private spaced path.
Compare actual SDK name/mode/content type/full bytes, strict binary POST with
202/immediate-success body and valid operation location, complete SDK envelope
and nested result, and complete saved/stdout reports including exact integer,
confidence/nulls/statistics/disclaimer. The reader closes and input stays
unchanged; quiet output is empty. Verify the actual sample path before reusing
the fixture-only report-path projection. Both positional and --file samples run.

Source binding only resolves name/default concurrency: it has no sample bytes,
service calls or report. `TestCoreAnalyzerTestBindingRequiresSamples` verifies
12 source-only argument variants fail local discovery before session creation.
Synthetic input/service/report data is not source output, another recording,
valid-PDF/accuracy evidence or four-worker performance proof. Python type,
sample/path/report and host seams remain unapproved; no live billing occurs.

The shared validate-binding parameter adds one source mapping, not 45.
`testdata/parity/core_validate_binding.json` retains both complete named/
positional Path observations and types, four hashes and classification/MIT
provenance. `TestCoreValidateBindingParity` runs 45 offline variants:
--schema/-s/positional, three output modes and five synthetic schema cases.
Observe actual Cobra arguments/flags while delegating unchanged validation;
verify the exact private spaced absolute path before projecting only that
fixture path to source schema.json. Compare complete clean/warning output,
strict-warning/malformed/read errors including category/message/hint/code,
and separately empty quiet/error output. Invalid service/auth environment
markers must be ignored, no service session may open, input bytes stay
unchanged and no extra files appear.

Source binding only binds schema.json; it reads no schema or emits validation
results/errors. Native synthetic schema/output/error cases do not prove
Python validator or service equivalence. Path/type/syntax/diagnostic/report/
host seams remain unapproved differences; no cassette or live call is added.

The shared analyzer-copy binding parameter adds one source mapping, not 18.
`testdata/parity/core_copy_binding.json` retains both complete parsed/bound/type
observations, four binding-file hashes and a fifth copy-operation reference.
The spec and operation establish that `dev`/`prod` are analyzer IDs, not
profiles; separate profile-copy and cross-resource cases remain pending.
`TestCoreCopyBindingParity` preserves both IDs across named/positional/output
variants and three synthetic same-resource cases: success, existing destination
and missing source. Verify actual binding, ordered full GET models/404s,
complete SDK copy request with empty cross-resource coordinates, strict
no-replacement POST/body and full immediate-ready result/output including
exact integer. Quiet output is empty; preflight failures expose full typed
service error/code/status/hint and must prevent any copy POST.

Reject account discovery, client switching, grants and prompts. Source binding
performs no HTTP or analyzer/error output. Native HTTP/models/results/errors
are synthetic, not source service outputs or a new recording. Python type,
surface/service-error/host seams remain unapproved. Both live-only copy
scenarios stay pending; no real copy, cross-resource authorization, auth/RBAC
or installed-host equivalence is established.

The complete discovery/redaction portion of the source input-planning suite
adds 58 exact case mappings from 29 original test functions.
`testdata/parity/input_discovery.json` retains complete call arguments,
structured/scalar results and types, typed errors/category/message/hint/status/
details/context, five code hashes, and exact pre-call file contents/directories/
symbolic and hard links. Every original Python assertion runs during capture;
source discovery must leave its fixture tree unchanged. Only the verified
pytest temporary root is normalized.

`TestInputDiscoveryParity` reconstructs the complete tree and runs real native
`analysis.Discover`/`RedactURL` in an isolated working directory without any
network or service client. Compare full selected-input order, canonical paths,
unchanged raw URLs, redacted display references, relative names and complete
native errors. Read and close actual native inputs to probe sizes, byte totals
and extension counts, then compare the entire unchanged fixture tree including
link identity. These statistics are explicit reader-derived probes, not native
planner fields. Source mode/origin/source-root/skipped records and Python
result identity remain visible in source observations but are not native API
fields; safe error/malformed-URL redaction differences remain unapproved.
Each case has a unique checked Go subtest reference. Digest/path drift,
unknown case references and nonexistent output-format variants are rejected;
review approval remains separate from source/control integrity.

This asset contains public source synthetic SAS/user-info/error controls, not
service recordings or deployable configuration. Never send its URLs anywhere.
`TestInputDiscoveryControlsAreNotRecordings` verifies the unchanged recording
hygiene scanner rejects the asset as a cassette and it is absent from imported
recordings. No hygiene matcher/import is relaxed. Output planning is covered
separately below; Windows-specific behavior/symlink privileges, installed host
and live execution remain separate evidence gates.

`testdata/parity/output_planning.json` adds 83 exact output-planning case
mappings across 16 complete source functions and 97 original calls. Capture
every original assertion, full typed input plans/arguments, execution plans/
types/errors and five source hashes. Retain each pre-call tree, including
test-authored existing result contents between repeated calls; normalize only
the verified temporary root and assert source planning creates no files.

`TestOutputPlanningParity` converts captured source input plans at the typed
output-planner boundary. Local files use real discovery/readers with checked
source-root-relative paths and sizes; remote inputs use real discovery and
retain their complete URLs. This does not replay original selection flags or
claim native mode/origin/skipped/statistics identity. Run real native
`PlanOutputs` against every exact pre-call tree and compare complete output/
input/path/exists/skipped/policy/format/report projections and full errors.
Check actual UTF-8 byte lengths in over-budget cases. Compare all 43 successful
native-only full `Preview` probes separately, without claiming Python dry-run
or CLI-output equivalence. All trees remain unchanged.

An explicit behavior difference is preserved: Python plans existing outputs
under error policy using flags; native immediately raises `result_exists`.
Keep all three such native failures and complete original source success
plans, without relaxing production safety. Diagnostic/reference/type/preview
differences remain unapproved. The unchanged cassette hygiene scanner rejects
this public synthetic URL/file-content control asset as a recording.

The initial output batch left six hash/injection/platform cases explicitly
deferred. Its immutable asset retains that historical record; five now have
separate evidence below, not inferred equivalence or an N/A disposition.

`testdata/parity/local_output_hashes.json` retains five original local-hash
cases/seven calls with every assertion, full typed observations/errors,
original hashed names/lengths, true reference hashes versus observed forced
digests, exact trees and five source hashes. Scoped deterministic capture
roots keep original root-dependent values reproducible without persisting
private absolute roots. Capture/file hashing stays bound to the original
SHA function even while Python globally overrides it.

`TestLocalOutputHashParity` uses real native SHA with explicit reference/root
bindings and named expected-path templates. Adapt only eight declared
exhaustion-neighbor paths/relative names, never arbitrary file contents or
source observations. Verify both fully reserved cases fail, complete success
plans/errors/native-only previews match, reordered reference/path maps remain
identical in the same native root, and every tree/source observation remains
unchanged. Sixteen additional synthetic native-only probes check reservations
0 through 7 and exact fallback lengths 8, 16, 24, 32, 40, 48, 56 and 64 hex
digits before exhaustion.

Python's injected-digest names use 8/16 digits; the native replacement uses
real SHA-256 with 8/8 digits. This is an explicit unapproved replacement, not
proof of a forced full native SHA collision. No production hook/algorithm,
source assertion or previous fixture/review state is changed.
The input-planning module now has **146/147 cases mapped**; only its
Windows-only case-insensitive collision case remains pending. All planning
batches, previous cassettes, live/installed-host/platform evidence and reviewer
approvals remain distinct.

`testdata/parity/schema_validation.json` captures all 31 original structural
validator cases and 32 complete call/constant observations, including exact
arguments, ordered findings, result/tuple types, allowed values and six pinned
source/spec hashes. Original assertions run unchanged; input guards detect
mutation. `TestSchemaValidationParity` replays original JSON/text through real
`schema.Validate` with service-spec validation disabled, honors explicit source
API versions, and separately probes preview behavior for unspecified versions.
Compare complete native safe diagnostics/order and actual diagnostic paths.
Both validators reject empty content categories; no production fix is needed.

Safe messages, sorted field traversal and tuple/helper identity differences
remain unapproved. The pinned-version helper uses real `PrepareDefinition`,
checking the full normalized body and explicitly projecting empty native
version to source null. Ten native-only type probes exercise nine accepted
types and invalid `datetime`; they do not invent an exported enum or add source
cases. The source `$ref` rejection is a pinned CLI structural policy, not a
claim about current service support. These public controls are not recordings,
live validation, command/host equivalence or reviewer approval.

`testdata/parity/core_analysis.json` captures all 29 original shared-core
analysis cases and 42 calls, including full pre/post arguments and job
mutations, results/types/typed errors, fake-client requests, custom-run and
callback outcomes, unchanged file/link trees and seven source hashes.
Only verified temporary roots and concurrent event ordering outside the
source contract are normalized; complete values and multiplicity remain
visible, not a claim about source completion order.

`TestCoreAnalysisParity` uses real discovery, output planning and execution.
Compare complete plans, all records, full raw envelopes, usage fallback,
safe errors, exact native JSON files and unchanged inputs. Thirty-nine
source-derived contract-client calls exercise async/inline, binary/URL,
usage, bounded concurrency and per-job failure isolation. First-wave
barriers measure the native worker bound, and actual file readers must close.
These are contract fakes, not SDK transport or service recording evidence.

Native combines planning/collision handling and canonicalizes first-kept
references; it rejects missing files instead of Python's realpath fallback.
Source helper objects become full native envelopes; custom source runners
use explicit native-only files and a `sourceRunnerResult` wrapper, while
callback observations remain source-only rather than an invented native API.
Native-only batch result files retain every payload, with declared native
JSON escaping/formatting. Preserve all these replacements/differences as
unapproved. Public synthetic SAS controls remain rejected as recordings.
No production behavior, prior assets/reviews, live authorization or
host/platform/performance gate is changed.

All 19 cassette identities and 25 source scenario dispositions now have
evidence: 14 recorded command scenarios and 11 proposed hygiene replacements.
Two live-only dispositions, all approvals and the other
semantic/preview/error/cancellation/host/release gates remain open.

```bash
# Inventory, recorded/normalized/project commands, network-denial and hygiene tests.
go test ./internal/cmd -run 'Test(Parity|Recorded|Normalized|Generated|SourceCapability|CoreShow|CoreDelete|CoreCreate|CoreAnalyzerTest|CoreValidate|CoreCopy|InputDiscovery|OutputPlanning|LocalOutputHash|SchemaValidationParity|CoreAnalysis)' -count=1

# Required parity approval gate before promotion; intentionally fails while rows remain pending.
go test -tags parityrelease ./internal/cmd -run '^TestParityReleaseReadiness$' -count=1
```

The approval command is an explicit gate, not yet wired into the shared release
pipeline. Remaining source cases, live scenarios, review dispositions,
SDK-backed integration seams, platform/install validation and authorized live
copy/defaults mutation are still open. GA success recordings do not prove
preview/errors/cancellation; scrubbed uploads do not prove binary bytes, and
dummy credentials do not prove authentication/RBAC.
