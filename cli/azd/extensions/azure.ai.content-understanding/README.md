# Azure Content Understanding

Native Go first-party Azure Content Understanding extension.
This development build provides help, version, metadata, analysis, analyzer
list/show/create/copy/delete/validate/test, service defaults, doctor, and environment-variable
inspection. The remaining CU workflows are in development.

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
```

Help, version, and metadata do not require an endpoint, sign-in, or project.
Service commands require a CU endpoint, a resolved tenant, and azd sign-in.
For projectless use, set `AZURE_TENANT_ID`, run `azd auth login`, and pass
`--endpoint` or set process `CU_ENDPOINT`.
Hidden `metadata` output is generated from the runtime Cobra command tree.
The extension inherits azd global flags instead of defining a second set.

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

Result/report publication uses private, same-directory temporary files and
rooted filesystem operations. No-clobber publication is atomic, replacement is
explicit, symlink/special-file destinations are rejected, and directory identity
is checked around publication. Temporary files are removed on failure.
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

EXTENSION_ID=azure.ai.content-understanding \
EXTENSION_VERSION=0.0.1 EXTENSION_PLATFORM=linux/amd64 ./build.sh

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

The agreed initial API baseline is `2025-11-01` by default, with explicit
`2026-06-01-preview` coverage. Transport-specific models will be minimal handwritten
internal types, not a public SDK. The REST implementation and the future official
Go SDK adapter will implement the same interface. Accepting arbitrary future
preview-version strings remains a separate CLI compatibility decision.

The contract is based on the [public CU TypeSpec and versioned OpenAPI specifications](https://github.com/Azure/azure-rest-api-specs/tree/56b13af91ff8d8b5ad706a502e6b4e44e6d57e1f/specification/ai/data-plane/ContentUnderstanding).
Inline analysis is preview-only; the adapter rejects it locally with the GA
version. Wire tests cover both versions and supported input/mode combinations.
Domain input validation alone does not claim wire/API-version conformance.
Analyzer list/show/create/delete are wired; remaining CU commands are not yet exposed.

### Authenticated HTTP transport

`internal/cuclient.RESTTransport` implements the extension-owned `Client`
interface. It accepts an injected azd-backed `azcore.TokenCredential` and uses
the `https://cognitiveservices.azure.com/.default` scope without selecting a second
tenant or identity. API-key authentication is not implemented.

- Endpoints require HTTPS, with no embedded credentials, query, fragment, or
  operation path. A trailing `/contentunderstanding` is normalized once.
- Only the two agreed API versions are accepted; the default remains `2025-11-01`.
- Requests carry the extension/version user agent, JSON accept header, a unique
  client request ID, and azd trace correlation when available.
- GET/HEAD requests allow at most two retries under the Azure pipeline policy.
  Other methods get one attempt, including on 401 authentication challenges.
  Credentials are acquired once per operation rather than using a bearer policy
  that can automatically replay a write after a challenge.
  Context-level SDK retry overrides cannot change these operation safety limits.
- The default timeout is two minutes per HTTP attempt and can be overridden
  internally. Caller cancellation propagates to token acquisition, requests, and
  response reads. The default HTTP client does not follow redirects.
- Error response bodies are bounded to 64 KiB before `azcore` can buffer them.
  Errors expose HTTP classification and a syntactically validated service code,
  not raw service messages, bodies, or authorization values. Oversized/malformed
  bodies still return an explicit HTTP failure. Valid `Retry-After` values are
  retained with a maximum of 24 hours.
- SDK body logging stays disabled; token, cookie, and non-allowlisted query values
  are redacted by the standard pipeline logging policy.

Tests use fake credentials/transports and local HTTPS servers. They do not call
Azure or incur service charges. Command tests additionally use injected clients
and local azd gRPC servers for request mapping, golden output files, confirmation,
cancellation, metadata launch policy, and safe failures.

### Analyzer REST operations

The internal REST adapter implements list/get/create/delete, copy authorization,
same-resource and cross-resource copy, and service defaults for both agreed API
versions.

- Listing drains every page in service order. Filtering/sorting belongs to
  commands. A failed or malformed page is an error, not a partial list.
- Continuation links must remain on the exact configured HTTPS origin and analyzer
  collection path, with the selected API version. Relative links are supported;
  repeated links, traversal, and credential-bearing links are rejected.
- Create and copy explicitly send `allowReplace=false`; no conditional headers or
  automatic write replay are used. Copy source lookup, destination preflight, and
  cross-resource grant orchestration belong to commands.
- Create/copy follow validated analyzer operation locations until completion.
  Pending polls use a one-second interval unless the service supplies a positive
  `Retry-After`. Cancellation/deadlines interrupt both waiting and HTTP requests.
  Success without an embedded result triggers the final analyzer GET.
- Analyzer JSON is retained without reconstructing schemas or rounding numbers.
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
  configured HTTPS origin under `/contentunderstanding/analyzerResults/`. Polls
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

The analysis tests include a local HTTPS binary round trip as well as fake
transports for the complete version/input/mode matrix, partial results, failures,
polling, no-replay, stream ownership, and SDK log redaction. No live Azure calls
are made.
