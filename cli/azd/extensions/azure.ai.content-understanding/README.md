# Azure Content Understanding

Native Go foundation for the Azure Content Understanding first-party extension.
This development build provides help, version, metadata, and runtime foundations.
The complete internal REST client is implemented but is not yet exposed as public
service commands.

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
```

These commands do not require a CU endpoint, Azure sign-in, or an azd project.
Hidden `metadata` output is generated from the runtime Cobra command tree.
The extension inherits azd global flags instead of defining a second set.

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

The internal runtime resolves configuration before constructing the azd-backed
credential. These settings are not yet exposed as public CU service commands.
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
Domain input validation alone does not claim wire/API-version conformance. Public
CU command wiring remains a separate step.

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
Azure or incur service charges. The transport is not yet wired to public CU
commands.

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
- Delete uses the service's idempotent semantics. The future command must look up
  existence first when a missing analyzer should be reported as an error.
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
