# Azure Content Understanding

Native Go foundation for the Azure Content Understanding first-party extension.
This development build provides help, version, metadata, and runtime foundations.
Analyzer REST operations are implemented internally but are not yet exposed as
public commands. Analysis submission is not implemented yet.

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
Inline analysis is preview-only; its GA rejection and two-version request/response
mapping belong to the upcoming transport tests. Domain input validation does not
claim wire/API-version conformance. No REST adapter or new public CU command is
enabled by the contract alone.

### Authenticated HTTP transport

`internal/cuclient.RESTTransport` supplies the HTTP foundation for the upcoming
REST adapter. It accepts an injected azd-backed `azcore.TokenCredential` and uses
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
versions. It is not a complete `Client` implementation until analysis is added.

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
