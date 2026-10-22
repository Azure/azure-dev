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
  Service-returned legacy/internal IDs are preserved, including IDs longer than
  64 characters or containing `@`; request-ID restrictions are not applied to list
  results. Validation of user-supplied request IDs is unchanged.
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
The grant's informational `source` path is optional; target-resource binding and
expiry parsing remain mandatory even when that path is absent.

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

### Native Go extension conventions

Like [Foundry agents](../azure.ai.agents/) and
[Foundry projects](../azure.ai.projects/), this extension uses:

- A Go binary with Cobra commands, `azdext.Run`, generated metadata and standard
  global flags. CU additionally validates invocation diagnostics and filters SDK
  logs to keep input/configuration values out of error output.
- The azd gRPC client, host token provider, scoped environment/config RPCs and
  structured extension errors; no independent Python runtime or profile store.
- The standard manifest, version file, changelog, developer build scripts,
  shared CI build wrapper, lint configuration and shared extension release
  pipeline. CI runs tests, lint, and Go modernization checks for both the extension
  and the separately versioned local SDK module. CODEOWNERS confirmation and release publication remain separate
  owner/release gates, not inferred from local tests.
- Native Go unit tests, typed fakes and SDK-backed command integration tests.
  Python supplies feature/edge-case references, not a second implementation
  framework or a source-test migration quota.

CU runs offline installed-host smoke from its existing `ci-test.ps1` entry point
in the shared release pipeline's `Windows` and `Mac` jobs. The
`ci-smoke.ps1` entry point downloads official azd 1.37.0, verifies its pinned
archive SHA-256, and runs `TestInstalledHostOffline` against that existing binary.
It reuses the pipeline's coverage-instrumented test binary and records coverage
in a temporary directory; it does not rebuild or claim signed-release evidence.
The test uses temporary host configuration, removes inherited Azure/CU settings,
and blocks remote HTTP(S) through an unavailable proxy. It checks local-registry
install/list, help, exact version JSON, strict offline schema validation from a
path containing spaces and Unicode, invalid-input failure, and uninstall/source
cleanup. It does not sign in, deploy, analyze documents, or change CU defaults.
No shared-template changes are needed, and Linux/ARM jobs do not repeat this
smoke. Actual platform execution remains required evidence, not a claim made by
adding the test entry point.

The generated-project suite also executes the PowerShell launcher, checking
arguments, success/failure exit codes, missing azd, and paths with spaces.
PowerShell is required on Windows; elsewhere this test runs when `pwsh` is
available. This launcher test uses a fake host and does not prove live
postprovision model setup or configuration.

CU declares only custom-command and metadata capabilities. Agents/projects
service-target, provisioning, lifecycle and MCP capabilities are not required
for this command-only extension. Generated CU project hooks invoke normal azd
commands rather than registering another provider.

### Validation

Use the Go version declared in `go.mod`.

```bash
go test ./... -count=1
go test -race ./... -count=1
go fix ./...
golangci-lint run ./...

cd sdk/azcontentunderstanding
go test -race ./... -count=1
go fix ./...
golangci-lint run ./...
cd ../..

cspell lint '**/*.go' '**/*.md' --config ./cspell.yaml --no-progress

EXTENSION_ID=azure.ai.content-understanding \
EXTENSION_VERSION=0.0.1 EXTENSION_PLATFORM=linux/amd64 bash ./build.sh

# Offline installed-host smoke on Windows/macOS, using an existing release binary:
pwsh ./ci-smoke.ps1 -BinaryPath ./azure-ai-content-understanding-windows-amd64.exe -Version 0.0.1

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

The extension has an entry in [CODEOWNERS](../../../../.github/CODEOWNERS).
Confirm CU/azd ownership and extension identity before merging.
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
  Write submissions disable automatic retries. An extension-owned SDK policy
  enforces the configured attempt timeout and retry bounds even if the caller
  supplies context-level Azure retry overrides.
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
`SDKTransport`, including copy, schema suggestion, analyzer-test and generated
postprovision integration tests. The former parallel REST transport and its
private request/polling helpers have been removed. Retry, authentication,
response-body bounds and diagnostics are tested through the production SDK
adapter; only genuine shared-helper behavior is tested directly.
The local SDK unit suite and adapter tests cover the supported
version/input/mode matrix, exact raw JSON, failures, polling, no-replay, stream
ownership, and SDK log redaction. The test suite makes no live Azure calls.
Prior prototype live evidence covers GA/preview URL, binary and base64 analysis,
preview inline behavior, analyzer lifecycle and same-resource copy.

Separate authorized Linux/amd64 installed-host validation with official azd
1.37.0 and GA API `2025-11-01` confirms Entra-backed list/show/create/delete and
cross-subscription, cross-region authorization/copy, including ready state and
schema equality at the destination. Live list results include legacy/internal
identifiers. The response-fix verification used owned temporary analyzers,
verified deletion on both resources, and performed no analysis or defaults writes.
An earlier installed-host run also confirmed a synthetic single-page custom
analysis, a single-sample prebuilt analyzer test, and defaults mutation/read-back
with complete snapshot restoration. These bounded runs do not establish broader
RBAC coverage, preview behavior, Windows/macOS runtime, or release readiness.

### Native testing and recorded playback

Tests follow the repository's native Go conventions: `testing`, table-driven
`t.Run`, `testify`, narrow typed fakes, and existing test helpers. Python tests
are behavior and edge-case references, not a required test shape or count.
Ordinary Go tests need neither Python nor a sibling source checkout.

`testdata/parity/fixtures.yaml` retains provenance for reused reference assets,
capability mappings, scenarios, and recordings. It is not a source-test ledger
or release-approval gate. The exhaustive source-node inventory, AST/subtest
registry, five input/planning/schema/analysis captures and count-based release
gate have been removed. Useful boundary assertions now live beside the owning
Go code. Command fixtures retain native payloads and source/license provenance,
not Python types, pytest node IDs or frontend observations. Direct Go tables
cover command bindings, outputs and errors; generated-project tests exercise
the native generator once, without duplicate Python-frontend labels.
Fixture inventories contain asset metadata, not review state or approvals;
human review remains separate from test execution.

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
in code and the inventory, and LF checkout policy. Compare the inventory's ordered
five-field projection (ID, name, kind, mode, recording list) exactly against
that source. Review state and approval are kept outside the test inventory.
Preserve shared cassettes across scenarios, but reject renamed,
reordered, duplicate or incorrectly associated scenario/recording entries.

The native tests retain all 27 exact per-name contracts, 16 command/11 hygiene
counts, 14 playback/two live-only/11 offline counts, and the exact union of 19
inventory/on-disk cassette identities. Twenty-one negative controls cover
source-reference and manifest drift, including missing cassette references
without using review state. These are manifest facts only: structural checks
for the two live-only rows do not execute copy, and hygiene-row shape checks
do not prove a native scrubber. Passing them does not approve a scenario.

### Native command and generated-project contracts

`internal/cmd/analyzer_*contract_test.go` uses ordinary Go tables and the
production `SDKTransport`. Reused JSON fixtures retain complete native payloads
and source commit/file-digest/MIT provenance. They do not define Python types,
pytest nodes, frontend observations or review states as Go runtime contracts.
No Python execution or sibling checkout is needed.

- List tests compare complete sorted output for default/explicit kind/sort
  flags. Delete tests cover positional/name aliases, preflight GET then empty
  204 DELETE, full acknowledgement, no prompting and quiet output. Conflicting
  operands fail before opening a session. Missing-status models fail with
  `malformed_response`, without output or a subsequent DELETE.
- Show tests cover positional/name aliases, trimmed names and full results;
  conflicting/missing names expose safe category/code/message/hint with no
  session, credential, HTTP, prompt or output.
- Create tests retain schema aliases and private absolute paths containing
  spaces, complete SDK definitions with unknown metadata/exact integers,
  no-replacement PUT and complete results. Invalid IDs fail before reading a
  schema or opening a session.
- Analyzer-test contracts retain default concurrency, SDK mode/content type,
  full binary bytes, complete envelopes/results and saved/stdout reports.
  Readers close, input bytes remain unchanged and missing samples fail locally.
- Validate tests cover positional/schema aliases, clean/warning/strict/
  malformed/missing-file cases and complete outputs/errors. Invalid auth/service
  environment markers are ignored; no session opens or extra files appear.
- Same-resource copy tests cover both operand forms, success/existing
  destination/missing source, full ordered reads, exact no-replacement requests
  and complete results or safe errors. They forbid account discovery, client
  switching, authorization grants, prompts and copy POSTs after failed preflight.

The fixtures with `core_*_binding` names are reduced native contract vectors,
not executable source-binding oracles. Typed SDK spies remain only where they
check actual Go requests, results or reader ownership. Duplicate delete
acknowledgement cases are covered by the shared list/delete contract suite.

`internal/cmd/generated_project_test.go` checks the full written/on-disk file
set, CRLF-to-LF golden hashes, parsed config JSON and the Bash executable bit.
Its choices use the native `infra.Choices` type. The golden tree retains the
documented native README/config adaptations and their source provenance.
The generated Bash hook executes against a local fake azd and must pass the
complete ordered hidden-command arguments. Neither test repeats execution
under labels for Python frontends that do not exist in the native extension.

Capability/scenario snapshots still check source facts, command mappings and
recording ownership; they contain no review-state/approval machinery. Strict
SDK playback, hygiene, exact-number comparisons and network denial are
unchanged. Synthetic command fixtures are not additional service recordings.
PowerShell, installed-host, platform and authorized live evidence remain
separate; these offline tests do not establish authentication/RBAC, live
copy/defaults mutation, accuracy, release approval or signing/registry evidence.

### Behavior and risk coverage

| Behavior or risk | Native evidence | Remaining validation |
| --- | --- | --- |
| Input selection, URL privacy and filesystem identity | `internal/analysis`: discovery, aliases, recursion, patterns, URL limits/redaction and reader identity tests | Actual Windows/macOS filesystem execution |
| Safe deterministic outputs | `internal/analysis`: policies, aliases, parent/report collisions, every real-SHA fallback length and exhaustion, reordered plans, UTF-8 byte budgets and complete previews | Platform-specific path execution |
| Analysis orchestration | `internal/analysis`: async/inline local/URL requests, complete envelopes/usage, saved JSON/LLM/report golden outputs, partial failures, bounded workers, cancellation and reader closure | Authorized live service and installed-host evidence |
| Structural authoring validation | `internal/schema`: compact validation vectors, complete ordered safe findings, diagnostic-path privacy, aliases, PrepareDefinition, bundled versioned contracts | Review material native/Python diagnostic and policy differences |
| Service requests and results | `internal/cmd`, `internal/cuclient` and the local SDK: strict production-SDK playback, no network fallback, full ordered interactions/results, binary contracts and redaction | Additional preview/error/cancellation service evidence and broader auth/RBAC coverage |
| Host and release integration | Existing command/generated-project and host-contract tests; separate Linux installed-host validation | Windows/macOS installed-host and PowerShell runtime, signing/registry/release ownership (Tasks 6.1/6.2) |

```bash
# Native behavior tests, command integration and strict SDK-backed playback.
go test ./internal/analysis ./internal/schema ./internal/cmd -count=1

# Extension-wide race checks (offline).
go test -race ./... -count=1
```

Completion is assessed against released behaviors and material risks, not a
Python-node total or a zero-pending ledger. Human review of material differences
and authorized runtime evidence remain separate requirements. Passing offline
tests does not approve release, prove authentication/RBAC, or replace actual
host/platform/live execution. Fixtures retain provenance/license and strict
hygiene checks; scrubbed uploads do not prove original binary request bytes.
