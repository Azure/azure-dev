# Performance Foundations for Azure Developer CLI

## Overview

This changeset introduces targeted performance optimizations to reduce deployment latency in `azd`,
particularly when multiple services are provisioned and deployed concurrently. Each change addresses a
specific bottleneck observed during parallel `azd up` runs with 4-8 services.

## Changes

### 1. HTTP Connection Pooling (TunedTransport)

**Files:** `pkg/httputil/util.go`, `cmd/deps.go`

**Problem:** Go's `http.DefaultTransport` limits connections to 2 per host (`MaxIdleConnsPerHost=2`) and
100 total idle connections. When `azd` deploys 8 services in parallel, each hitting ARM endpoints
(`management.azure.com`), connections are torn down and re-established constantly. Every new TLS 1.2+
handshake to ARM costs 1-2 round trips (50-150ms per handshake depending on region).

**Solution:** `TunedTransport()` clones `http.DefaultTransport` and raises:
- `MaxIdleConns`: 100 -> 200 (total idle pool across all hosts)
- `MaxConnsPerHost`: 0 (unlimited) -> 50 (bounded but generous)
- `MaxIdleConnsPerHost`: 2 -> 50 (matches MaxConnsPerHost so idle connections aren't evicted)
- `IdleConnTimeout`: 90s -> 30s (reclaim unused connections faster)
- `DisableKeepAlives`: false (explicit; HTTP/1.1 keep-alive per RFC 7230 Section 6.3)

**Evidence:** Go stdlib defaults are documented in `net/http/transport.go`. The per-host idle limit of 2
is the primary bottleneck; raising it to match `MaxConnsPerHost` ensures connections created during a
burst are retained for subsequent requests to the same host. The 30s idle timeout is sufficient because
`azd` operations complete within seconds of each other during parallel deployment.

**Wiring:** `cmd/deps.go` replaces `http.DefaultClient` with `&http.Client{Transport: TunedTransport()}`
so all SDK clients created through dependency injection inherit the tuned transport.

### 2. ARM Client Caching (sync.Map)

**Files:** `pkg/azapi/resource_service.go`, `pkg/azapi/standard_deployments.go`,
`pkg/azapi/stack_deployments.go`

**Problem:** ARM SDK clients (`armresources.Client`, `DeploymentsClient`, `DeploymentOperationsClient`,
`armdeploymentstacks.Client`) were re-created on every API call. Each construction builds an HTTP
pipeline (retry policy, logging policy, auth policy). While not as expensive as a TLS handshake, this
adds unnecessary CPU and allocation overhead when the same subscription is used repeatedly.

**Solution:** Cache clients in `sync.Map` fields keyed by subscription ID. The pattern uses
`Load` for the fast path and `LoadOrStore` on miss. The "benign race" comment documents that concurrent
cache misses may create duplicate clients; `LoadOrStore` ensures only one is retained. This is safe
because Azure SDK ARM clients are stateless and goroutine-safe (they hold only the pipeline
configuration, not per-request state).

**Evidence:** Azure SDK for Go documentation confirms ARM clients are safe for concurrent use:
"A client is safe for concurrent use across goroutines" (azure-sdk-for-go design guidelines). `sync.Map`
is the standard Go pattern for read-heavy caches with infrequent writes, avoiding lock contention on the
hot path.

### 3. Adaptive Poll Frequency

**Files:** `pkg/azapi/standard_deployments.go`, `pkg/azapi/stack_deployments.go`

**Problem:** `PollUntilDone(ctx, nil)` uses the Azure SDK's default polling interval of 30 seconds.
For deploy operations that typically complete in 30-120 seconds, this means the first successful poll may
arrive 30s after the deployment actually finished, adding unnecessary wall-clock latency.

**Solution:** Two tuned frequencies:
- `deployPollFrequency = 5s` for deploy and delete operations (variable completion time; 6x faster
  than the 30s default while leaving ample headroom against ARM rate limits)
- `slowPollFrequency = 5s` for WhatIf and Validate operations (consistently slow at 30-90s; aggressive
  polling wastes ARM read quota without benefit)

The 5s interval balances latency reduction against ARM rate limits: 1200 reads per 5 minutes per
subscription. With 8 parallel deployments polling at 5s, that's 96 reads/minute per operation type,
leaving substantial headroom for other ARM reads (list operations, status checks, etc.).

**Evidence:** Azure SDK `runtime.PollUntilDoneOptions.Frequency` is the documented mechanism. ARM rate
limits are documented at
`learn.microsoft.com/en-us/azure/azure-resource-manager/management/request-limits-and-throttling`.

### 4. Zip Deploy Retry with SCM Readiness Probe

**Files:** `pkg/azapi/webapp.go`, `pkg/azsdk/zip_deploy_client.go`

**Problem:** During concurrent `azd up`, ARM applies site configuration (app settings) shortly after the
App Service resource is created. This triggers an SCM (Kudu) container restart. If the zip deploy starts
while the SCM container is restarting, the Oryx build process fails with "the build process failed".
This is a transient failure specific to the concurrent provisioning + deployment pattern.

**Solution:**
- `isBuildFailure(err)` detects the specific transient error string ("the build process failed") while
  excluding genuine build errors that contain "logs for more info"
- On build failure, retry up to 2 additional times (3 total attempts)
- Before each retry, `waitForScmReady()` polls the SCM `/api/deployments` endpoint until it returns
  HTTP 200, indicating the container has finished restarting (90s timeout, 5s poll interval)
- The zip file `ReadSeeker` is rewound via `Seek(0, io.SeekStart)` before each retry
- After the retry loop exits (success or exhausted), the zip file is also rewound before the non-Linux
  fallback `Deploy()` path, since the tracked deploy consumed the reader

**OTel Tracing:** The entire `DeployAppServiceZip` function is wrapped in a tracing span named
`"deploy.appservice.zip"` with attributes: `deploy.appservice.app` (app name),
`deploy.appservice.rg` (resource group), `deploy.appservice.linux` (boolean), and
`deploy.appservice.attempt` (1-indexed attempt number, updated per retry iteration). The span uses
named return `err` with `span.EndWithStatus(err)` to automatically record failure status.

`IsScmReady()` on `ZipDeployClient` sends a lightweight GET to `/api/deployments`. Connection errors
return `(false, nil)` rather than propagating - the SCM is still restarting, which is the expected state.
The `//nolint:nilerr` directive documents this intentional error suppression for the linter.

### 5. ACR Credential Exponential Backoff

**File:** `pkg/project/container_helper.go`

**Problem:** ACR credential retrieval after resource creation used a constant 20-second retry delay
(`retry.NewConstant(20s)`). In the common case, credentials are available within 1-5 seconds after the
ACR resource is provisioned. The 20s constant delay means even a single retry wastes 15-19 seconds.

**Solution:** Changed to exponential backoff starting at 2 seconds:
`retry.NewExponential(2s)` with max 5 retries produces delays of 2s, 4s, 8s, 16s, 32s (62s worst case
vs 60s worst case with the old constant 20s * 3 retries). The exponential curve means most transient
404s (DNS propagation, eventual consistency) resolve on the first or second retry, while the longer
tail preserves roughly the same total retry window for slow DNS propagation edge cases.

**Evidence:** Azure DNS propagation FAQ confirms changes "typically take effect within 60 seconds" but
are often faster. The old link in the comment
(`learn.microsoft.com/en-us/azure/dns/dns-faq#how-long-does-it-take-for-dns-changes-to-take-effect-`)
documents worst-case TTL, not typical latency.

### 6. Docker Path Resolution Fix

**Files:** `pkg/project/container_helper.go`, `pkg/project/framework_service_docker.go`,
`pkg/project/framework_service_docker_test.go`

**Problem:** Docker path resolution was inconsistent across `Build()`, `runRemoteBuild()`,
`packBuild()`, and `useDotNetPublishForDockerBuild()`. User-specified `docker.path` and
`docker.context` in `azure.yaml` are relative to the project root (where `azure.yaml` lives), but
default paths (`./Dockerfile`, `.`) are relative to the service directory. The old code used
`serviceConfig.Path()` for both cases, which broke when a .NET service's `azure.yaml` specified a
custom `docker.path` pointing to a Dockerfile outside the service directory.

**Solution:** Extracted `resolveDockerPaths()` that resolves docker.path and docker.context
to absolute paths. User-specified paths (from `azure.yaml`) are resolved relative to the project
root (where `azure.yaml` lives), while default paths (`./Dockerfile`, `.`) are resolved relative to
the service directory via `resolveServiceDir()`. This centralizes path resolution that was
previously duplicated across `Build()`, `runRemoteBuild()`, and other call sites.

Called from `Build()`, `runRemoteBuild()`, and `useDotNetPublishForDockerBuild()` (which now calls
`resolveDockerPaths()` directly instead of reimplementing the logic). Tests updated to expect
absolute resolved paths, including dynamic resolution in the table-driven `Test_DockerProject_Build`
test. `filepath.Clean` is applied to both resolved paths to normalize separators and prevent
path traversal.

### 7. ReadRawResponse Body Close Fix

**File:** `pkg/httputil/util.go`

**Problem:** `ReadRawResponse` read `response.Body` via `io.ReadAll` but never closed it. While Go's
HTTP client reuses connections when the body is fully read and closed, an unclosed body prevents the
underlying TCP connection from returning to the pool.

**Solution:** Added `defer response.Body.Close()` at the top of the function.

### 8. UpdateAppServiceAppSetting Helper

**File:** `pkg/azapi/webapp.go`

**Problem:** No API existed to update a single App Service application setting without replacing the
entire set. Callers that needed to add or modify one setting had to manually read-modify-write the
full settings dictionary, duplicating boilerplate.

**Solution:** Added `UpdateAppServiceAppSetting(ctx, subscriptionId, resourceGroup, appName, key,
value)` that reads existing settings via `ListApplicationSettings`, adds/overwrites the specified
key-value pair, and writes all settings back via `UpdateApplicationSettings`. This preserves other
settings that are not being modified. The function handles the nil-properties edge case by
initializing the map if empty.

### 9. OTel Tracing Spans for Deployment Profiling

**Files:** `pkg/azapi/standard_deployments.go`, `pkg/azapi/stack_deployments.go`,
`pkg/project/container_helper.go`, `pkg/project/container_helper_test.go`

**Problem:** Only `DeployAppServiceZip` had OTel tracing. ARM deployments, validation,
WhatIf operations, and container build/publish had no span coverage, making it impossible to
profile where wall-clock time is spent during parallel deployment.

**Solution:** Added 11 OTel tracing spans to all performance-critical deployment paths using
the established pattern: `tracing.Start(ctx, spanName)` + named return `err` + `defer func()
{ span.EndWithStatus(err) }()`. Each span includes `attribute.String` for subscription,
resource group, and deployment name to enable correlation in trace viewers.

**ARM Standard Deployments** (`standard_deployments.go` — 6 spans):
- `arm.deploy.subscription` — `DeployToSubscription` (ARM deploy at subscription scope)
- `arm.deploy.resourcegroup` — `DeployToResourceGroup` (ARM deploy at resource group scope)
- `arm.whatif.subscription` — `WhatIfDeployToSubscription` (WhatIf preview at subscription)
- `arm.whatif.resourcegroup` — `WhatIfDeployToResourceGroup` (WhatIf preview at resource group)
- `arm.validate.subscription` — `ValidatePreflightToSubscription` (preflight validation)
- `arm.validate.resourcegroup` — `ValidatePreflightToResourceGroup` (preflight validation)

**Deployment Stacks** (`stack_deployments.go` — 2 spans):
- `arm.stack.deploy.subscription` — `DeployToSubscription` (deployment stack at subscription)
- `arm.stack.deploy.resourcegroup` — `DeployToResourceGroup` (deployment stack at resource group)

**Container Operations** (`container_helper.go` — 3 spans):
- `container.publish` — `Publish` (end-to-end container publish orchestration; includes
  `container.remotebuild` boolean attribute)
- `container.credentials` — `Credentials` (ACR credential retrieval with exponential backoff)
- `container.remotebuild` — `runRemoteBuild` (ACR remote build including context upload)

**Test Updates** (`container_helper_test.go`): Mock assertions for `Login` updated from exact
context matching (`*mockContext.Context`) to `mock.Anything` because `tracing.Start` wraps the
context with a span value, causing strict equality to fail. This applies to
`setupContainerRegistryMocks`, `Test_ContainerHelper_Deploy`, and
`Test_ContainerHelper_Deploy_ImageOverride` test assertions.

**Evidence:** These spans cover the four longest-running operation categories during parallel
deployment: ARM provisioning (30-120s), WhatIf/Validate (30-90s), container build/push (30-
300s), and ACR credential retrieval (2-62s with backoff). Together with the existing
`deploy.appservice.zip` span, they provide full end-to-end visibility for profiling `azd up`.

### 10. Supporting Changes

**Files:** `.gitignore`, `cli/azd/.vscode/cspell-azd-dictionary.txt`

- `.gitignore`: Added coverage artifacts (`cover-*`, `cover_*`, `review-*.diff`) and Playwright MCP
  directory (`.playwright-mcp/`)
- `cspell-azd-dictionary.txt`: Added `keepalives` (HTTP keep-alive terminology in TunedTransport),
  `nilerr` (golangci-lint nolint directive in `IsScmReady`), `appsettings` (App Service terminology)

### 11. Container App Adaptive Poll Frequency

**File:** `pkg/containerapps/container_app.go`

**Problem:** Container App create/update/job operations used `PollUntilDone(ctx, nil)`, defaulting
to the Azure SDK's 30-second polling interval. Unlike ARM template deployments (which go through
`standard_deployments.go` where poll frequency was already tuned in §3), Container App revision
updates go through a separate code path (`containerAppService.DeployYaml`, `AddRevision`,
`updateContainerApp`, `UpdateContainerAppJob`). Templates with multiple Container Apps (e.g., the
5-service "shop" template) suffered compounded tail latency: up to 28 seconds wasted per service ×
5 services = **140 seconds** of unnecessary wait time.

**Solution:** Added `containerAppPollFrequency = 5 * time.Second` constant and applied it to all
three `PollUntilDone` call sites:
- `DeployYaml` → `BeginCreateOrUpdate` poller (line 321)
- `updateContainerApp` → `BeginUpdate` poller (line 546)
- `UpdateContainerAppJob` → `BeginUpdate` poller (line 780)

**Evidence:** Container App revision updates typically complete in 10-60 seconds (observed in perf
benchmarks). The same 5s frequency used for ARM deploy operations (§3) balances latency reduction
(6x faster than the 30s default) against ARM rate limits. With 8 parallel services, this generates
~96 polls/min per operation type — well within the 1200 reads/5min/subscription limit.

### 12. ACR Login Caching per Registry

**File:** `pkg/azapi/container_registry.go`

**Problem:** `containerRegistryService.Login()` was called for every service during deploy,
regardless of whether the same registry had already been authenticated. For templates pushing
multiple container images to the same ACR (e.g., shop with 5 services sharing one registry),
this meant 4 redundant credential exchanges (AAD → ACR token swap via `/oauth2/exchange`) and
4 redundant `docker login` commands. Each credential exchange involves an HTTP round-trip to the
ACR token endpoint plus an ARM token acquisition — typically 2-5 seconds per call.

**Solution:** Added two-layer deduplication to `containerRegistryService`:
- `loginGroup singleflight.Group` deduplicates in-flight concurrent login attempts. Uses `DoChan`
  so each caller can independently cancel via its own context without affecting other waiters.
  The shared work runs under `context.WithoutCancel` to avoid tying it to any single caller.
- `loginDone sync.Map` tracks registries that have already been authenticated this session.
  Subsequent `Login()` calls return immediately with a log message.

**Evidence:** Docker's credential store also caches logins, so the `docker login` call itself
would have been idempotent. However, the expensive part is the *credential exchange* (`Credentials()`
→ `getAcrToken()` → AAD token → ACR refresh token), which is not cached by Docker. With 5
services sharing one ACR, this saves 4 × ~3s = **~12 seconds** plus 4 redundant Docker CLI
invocations.

## Test Coverage

| Change | Test File | Coverage |
|--------|-----------|----------|
| `TunedTransport` | `pkg/httputil/util_test.go` | Verifies all pool params; verifies DefaultTransport not mutated |
| `isBuildFailure` | `pkg/azapi/webapp_test.go` | 6 cases: nil, unrelated, exact match, wrapped, real build failure, partial |
| Docker path resolution | `pkg/project/framework_service_docker_test.go` | Updated to verify absolute resolved paths via dynamic resolution |
| `resolveDockerPaths` | `pkg/project/container_helper_coverage3_test.go` | 4 sub-tests: defaults, user-specified, absolute, mixed |
| Docker build args | `pkg/project/framework_service_docker_test.go` | Table-driven tests resolve expected paths to match production behavior |
| Tracing context propagation | `pkg/project/container_helper_test.go` | Mock assertions updated for tracing context wrapping (`mock.Anything`) |

The ARM client caching, TunedTransport wiring, poll frequency (both ARM and Container App),
SCM retry logic, ACR login caching, `UpdateAppServiceAppSetting`, and OTel tracing spans are
infrastructure changes that operate through Azure SDK interactions and are validated through
integration and end-to-end playback tests rather than isolated unit tests.
