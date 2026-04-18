# Exegraph: DAG-Based Parallel Execution for Azure Developer CLI

## Problem

`azd provision`, `azd deploy`, and `azd up` execute sequentially — infrastructure
layers deploy one-by-one, services package/publish/deploy in series. For projects
with multiple Bicep layers or many services, wall-clock time scales linearly with
the number of resources.

## Solution

Introduce `pkg/exegraph` — a general-purpose DAG execution engine — and wire it
into provisioning, deployment, and unified `up`. Steps with no dependency
relationship run concurrently. All graph-driven execution is unconditional - no feature
flags required.

## Activation

| Command         | Condition                                                    |
|-----------------|--------------------------------------------------------------|
| `azd provision` | Always — single-layer runs as a one-node graph (reusing the injected `provisionManager`), multi-layer runs as an N-node graph with per-layer env clones |
| `azd deploy`    | Always — all services run through the service graph regardless of count |
| `azd up`        | Always, unless the project defines a custom `workflows.up:` in `azure.yaml` |

The unified `up` DAG runs project command hooks (`prepackage`/`postpackage`/`preprovision`/`postprovision`/`predeploy`/`postdeploy`)
as synthetic `cmdhook-*` nodes with explicit dependencies, and fires `project.EventPackage` / `project.EventDeploy`
listeners as synthetic `event-*` nodes. Projects with those hooks/handlers no longer fall through to a sequential
path. Only user-authored `workflows.up:` still runs via `workflow.Runner`.
Each sub-command spawned by `workflow.Runner` (`azd package`, `azd provision`, `azd deploy`)
still runs its own phase-scoped DAG internally — `azd provision` uses the layer graph,
`azd deploy` uses the service graph — so parallel provisioning and parallel service deploys
are preserved. What is lost is the unified cross-phase DAG: packaging cannot overlap with
provisioning, and `cmdhook-*` node integration is not available.

## Architecture

```
pkg/exegraph/                     ← Pure engine (zero azd deps except OTel tracing)
  step.go           (87 lines)    ← StepFunc, StepStatus, StepSkippedError, RunResult
  graph.go         (182 lines)    ← DAG: AddStep, Validate, Priority, Steps
  scheduler.go     (429 lines)    ← Worker pool: Run / RunWithResult

internal/cmd/
  provision_graph.go   (942)      ← Provision DAG: unified path; single-layer = one-node graph, multi-layer = N-node graph with per-layer env clones
  deploy.go            (548)      ← Deploy graph: unified path; package→publish→deploy per service (including N=1)
  deploy_progress.go   (258)      ← Progress table (interactive rewrite / CI line mode)
  service_graph.go     (279)      ← Shared service-step builder used by deploy + up (ecosystem-agnostic; gate policy injected via serviceGraphOptions.buildGateKey)
  up_graph.go          (469)      ← Unified DAG: cmdhook-preprovision → provision → cmdhook-postprovision → cmdhook-predeploy → event-predeploy → publish/deploy → event-postdeploy → cmdhook-postdeploy, with a parallel cmdhook-prepackage → event-prepackage → package-<svc> → event-postpackage → cmdhook-postpackage chain that gates event-predeploy
  project_hooks.go      (65)      ← runProjectCommandHook helper for cmdhook-* DAG nodes
  aspire_gate.go        (30)      ← Aspire build-gate policy (aspireBuildGateKey); the only Aspire-specific file in the exegraph call-graph — isolated so it can move with Aspire when Aspire becomes an extension

pkg/infra/provisioning/bicep/
  layer_deps.go        (262)      ← Static Bicep dependency analysis
  bicep_provider.go    (~160 net) ← Adaptive ARM polling

pkg/tools/bicep/
  bicep.go             (~100 net) ← In-memory Bicep compile cache

pkg/input/                        ← Ref-counted console previewer
pkg/project/                      ← Thread-safe service manager, env locking
```

Line counts are snapshots at the commit that introduced the change — they drift with
follow-up edits. Directory layout and file responsibilities are stable.

## Core Engine — `pkg/exegraph/`

### Step (`step.go`)

Defines the unit of work:

- **StepFunc** — `func(ctx context.Context) error`
- **Step** — `Name string`, `DependsOn []string`, `Tags []string`, `Action StepFunc`
  (per-step timeout is expressed via `RunOptions.StepTimeout`, not a `Step` field)
- **StepStatus** — `Pending → Running → Done | Failed | Skipped`
- **StepSkippedError** — returned by a step to mark itself skipped; downstream steps skip too
- **RunResult** — per-step timing (`StepTiming`), status, error, plus aggregate duration

### Graph (`graph.go`)

Insertion-order-deterministic DAG:

- **AddStep** — appends a step; duplicate names error
- **Validate** — DFS cycle detection + missing-dependency check
- **Priority** — transitive-dependent count heuristic (steps with more downstream work run first)
- **Steps** — returns steps in insertion order (deterministic scheduling)

### Scheduler (`scheduler.go`)

Event-driven bounded worker pool:

- **Concurrency** — `MaxConcurrency=0` (default) caps workers at `min(stepCount, GOMAXPROCS×2)`.
  Explicit positive values override this (values larger than `min(stepCount, GOMAXPROCS×2)`
  have no effect; the worker count never exceeds the natural cap).
- **Error policies** — `FailFast` cancels all on first error; `ContinueOnError` runs remaining independent steps
- **Per-step timeout** — uniform `RunOptions.StepTimeout` wraps every step's context with
  `context.WithTimeout`. Zero (the default) means no deadline. A step that exceeds the
  timeout returns `context.DeadlineExceeded` and is classified as a genuine failure
  (distinct from parent-cancel / FailFast tear-down, which are classified as skipped).
- **Panic recovery** — `runStep` recovers panics as errors; callback panics also recovered
- **OTel tracing** — root span `exegraph.run`, per-step child span `exegraph.step` with step name/deps/tags attributes
- **Completeness safety net** — post-run assertion that all steps resolved; detects graph scheduling bugs
- **Callbacks** — optional `OnStepStart`/`OnStepDone` for progress reporting (panic-safe)

## Bicep Layer Analysis

### Static Dependency Analysis (`layer_deps.go`)

Parses Bicep layers to build a dependency graph:

1. Scans each layer's `.bicep` file for `output <name>` declarations
2. Scans parameter files for environment references:
   - `.bicepparam` → `readEnvironmentVariable('NAME')`
   - JSON params → `${NAME}` patterns
3. Creates edges: if layer B references env var X and layer A produces output X, then B depends on A
4. **Env-skip optimization**: if the env already has variable X (`env.LookupEnv`), the edge is skipped —
   re-runs use cached `.env` values and avoid waiting on a fresh provision

Detects cycles, duplicate outputs, and missing references.

### Adaptive ARM Polling (`bicep_provider.go`)

Replaces fixed-interval ARM deployment polling with exponential backoff:

- **`adaptivePoller`** struct: `minInterval=1s`, `maxInterval=10s`, `backoffFactor=2.0`
- Resets to `minInterval` when new resources appear (resource count changes)
- Backs off exponentially when no progress detected
- **Throttle detection**: recognizes HTTP 429 responses; after 5 consecutive throttles,
  forces `maxInterval` to reduce API pressure

### Bicep Compile Cache (`bicep.go`)

In-memory `sync.Map` cache keyed by SHA-256 of the full Bicep file tree:

- Recursively hashes the main `.bicep` file + local module references
- Ignores registry modules (`br:`, `ts:` prefixes)
- Includes companion `.bicepparam` content in hash
- Cache hit skips `bicep build` subprocess entirely

## Command Integration

### Graph-Driven Provision (`provision_graph.go`)

1. For zero layers: short-circuits with "No provisioning layers defined — nothing to
   provision/preview." (no graph execution, no state mutation)
2. For `--preview`: bypasses the exegraph entirely and calls `provisionManager.Preview()`
   directly (preview has no hooks, no env updates, no cache invalidation, and is always
   single-layer, so a graph adds no value)
3. For a single non-preview layer: builds a one-node exegraph whose step calls the
   injected `provisionManager` directly, preserving bit-for-bit parity with the legacy
   sequential path and respecting test mocks of the manager
4. For multiple non-preview layers: calls `AnalyzeLayerDependencies`, then creates one
   exegraph step per Bicep layer with edges from the analysis. Each layer runs against
   a cloned environment + freshly constructed `provisioning.Manager` for safe concurrent
   execution. Subscription / location prompts are resolved up-front (once) against the
   shared manager before concurrent steps start, so CI runs are race-free
5. All step failures flow through `wrapProvisionError(ctx, unwrapStepErrors(result))` at
   the outer boundary: the scheduler's `step "X" failed:` prefix is stripped, and
   preflight-abort / JSON state dump / OpenAI-access / Responsible-AI wrappers are
   applied exactly once
6. `FailFast` error policy
7. Concurrency limit configurable via `AZD_PROVISION_CONCURRENCY` env var
8. Wraps console output in `syncConsole` (mutex-wrapped message/spinner methods)

### Graph-Driven Deploy (`deploy.go`)

Activates unconditionally. Per service, creates three steps:

1. **`package-<svc>`** — no dependencies; runs `serviceManager.Package` or uses `--from-package`
2. **`publish-<svc>`** — depends on `package-<svc>`; runs `serviceManager.Publish`
3. **`deploy-<svc>`** — depends on `publish-<svc>`; runs `serviceManager.Deploy`

**Build gate (soft serialization)**: `serviceGraphOptions.buildGateKey` is an
optional callback that returns an opaque string grouping for each service.
Services sharing a non-empty key serialize on a "first wins, rest wait" basis
— the first service in the group runs free, every later service in the same
group depends on that first deploy step. Services returning `""` (or when the
callback is nil) run in full parallelism. The graph builder is agnostic to
the policy; today both `azd deploy` and `azd up` supply the
`aspireBuildGateKey` callback (returns `"aspire"` for services with
`DotNetContainerApp != nil`) to serialize the shared .NET AppHost build.
Independent groups can coexist — keys are only compared within the set, never
across.

Progress displayed via `deployProgressTracker` — interactive mode rewrites lines with ANSI;
non-interactive mode prints one line per event. `RenderFinal` is a no-op in non-interactive
mode to avoid polluting `--output json`.

### Unified Up (`up_graph.go`)

Builds a single DAG that unifies provisioning and deployment for every invocation
of `azd up` (the only exception is when the project defines a custom `workflows.up:`).

Node layout:

Provision chain:
- `cmdhook-preprovision` — fires the `preprovision` project command hook (no-op if not declared)
- `provision-<layer>` per Bicep layer (dependencies from `layer_deps.go` analysis)
- `cmdhook-postprovision` (depends on all provision nodes)
- `cmdhook-predeploy` (depends on postprovision sink)

Package chain (runs concurrently with the provision chain so packaging overlaps with provisioning):
- `cmdhook-prepackage` — fires the `prepackage` project command hook; **no dependencies**
- `event-prepackage` — fires `project.EventPackage` Before listeners (depends on `cmdhook-prepackage`)
- `package-<svc>` per service — depends on `event-prepackage` (via `serviceGraphOptions.packageExtraDeps`)
- `event-postpackage` — fires `project.EventPackage` After listeners (depends on every `package-<svc>` node)
- `cmdhook-postpackage` — fires the `postpackage` project command hook (depends on `event-postpackage`)

Deploy chain:
- `event-predeploy` — fires `project.EventDeploy` Before listeners; depends on
  `cmdhook-predeploy` **and** `cmdhook-postpackage` **and** every `package-<svc>`
  node (so predeploy handlers see packaged artifacts and the legacy
  `postpackage`-before-`predeploy` ordering is preserved)
- `publish-<svc>` depends on `package-<svc>` + `event-predeploy` (and therefore
  transitively on all provisioning via the cmdhook-predeploy gate)
- `deploy-<svc>` depends on `publish-<svc>` (plus any edge added by
  `serviceGraphOptions.buildGateKey` — today that's the Aspire build-gate for
  `DotNetContainerApp` services)
- `event-postdeploy` — fires `project.EventDeploy` After listeners (depends on
  all `deploy-<svc>` nodes)
- `cmdhook-postdeploy` (depends on event-postdeploy)

Deploy timeout honors `--timeout` / `AZD_DEPLOY_TIMEOUT` via the shared
`resolveDeployTimeout` helper.

## Thread Safety

| Component | Mechanism | Protects |
|-----------|-----------|----------|
| `console.go` | `previewerRefCount` + `sync/atomic.Pointer[progressLog]` | Concurrent DAG step previewer output; ref-count ensures previewer stops only when last user finishes |
| `console_previewer_writer.go` | Atomic pointer nil-check | Write-after-stop returns `len, nil` with a log message instead of panicking |
| `service_manager.go` | `sync.Mutex` on `operationCache` and `initialized` map | Concurrent service Package/Deploy operations sharing caches |
| `service_target_containerapp.go` | Caller-supplied `envMu` (the deploy graph's shared env mutex) | Read of `SERVICE_<NAME>_TEMPLATE_HASH` in `evaluateTemplateHash` + post-deploy `DotenvSet`+`Save` of the same key are both held under `envMu` to protect the underlying `map[string]string` from concurrent service deploys |
| `provision_graph.go` (multi-layer) | `envMu` (per-run shared mutex) protects `deps.env` reads/writes; `hookMu` (per-run shared mutex) serializes hook + event handler execution | Step 0 clone, step 4 reload+merge+save, and step 8 final reload of `deps.env` happen under `envMu`. Steps 1-2 and 5-7 (pre/post hooks + project events) hold `hookMu` so non-thread-safe handlers (AKS k8s context, .NET appsettings) never run concurrently across layers |

**Direct revision API shortcut** (`service_target_containerapp.go`): For each service deploy,
`evaluateTemplateHash` (read-only) compares the on-disk infrastructure template's SHA-256 against
the value previously stored under `SERVICE_<NAME>_TEMPLATE_HASH`. When the hash matches, the full
ARM deployment is skipped and a direct revision API call is used instead. When it differs (or no
prior value exists), the full ARM deploy runs and the caller persists the new hash **only after
the deploy succeeds** — so a failed deploy does not leave a stored hash that would cause the next
run to skip the still-required full deployment via the optimization path.

## Observability

OTel events and attributes added:

| Event/Field | Value |
|-------------|-------|
| `exegraph.run` | Root span for each graph execution |
| `exegraph.step` | Child span per step |
| `exegraph.step.count` | Number of steps in graph |
| `exegraph.max_concurrency` | Effective worker count |
| `exegraph.error_policy` | `FailFast` or `ContinueOnError` |
| `exegraph.step.name` | Step name |
| `exegraph.step.deps` | Step dependency list |
| `exegraph.step.tags` | Step tags |
| `exegraph.step.timeout_s` | Step timeout in seconds |

## Test Coverage

| Test file | Tests | Coverage |
|-----------|-------|----------|
| `pkg/exegraph/graph_test.go` | 15 | Mutation rules, ordering, cycles, priority, tags |
| `pkg/exegraph/scheduler_test.go` | 33 | Execution semantics, cancellation, skip propagation, concurrency bounds, panic recovery, goroutine cleanup, timing, per-step timeout |
| `pkg/infra/provisioning/bicep/layer_deps_test.go` | 12 | Temp file fixtures, cycles, env-skip, missing refs |
| `internal/cmd/provision_graph_test.go` | 7 | Graph build, execution ordering, `dependsOn` edge ordering, env merge (preserves subprocess writes + concurrent merges converge), reload (refreshes `deps.env` from disk for downstream-layer clones) |
| `internal/cmd/provision_security_test.go` | 2 | Env serialization, clone isolation |
| `internal/cmd/deploy_graph_test.go` | 4 | Graph construction, Aspire gating, generic multi-group gating |
| `internal/cmd/deploy_progress_test.go` | 13 | Interactive/non-interactive rendering, truncation, final render |
| `pkg/tools/bicep/bicep_cache_test.go` | 6 | Cache hit/miss, hash stability, module resolution |

**48 exegraph engine tests** (15 graph + 33 scheduler). Additional integration tests across
provisioning, deployment, and thread-safety modules (see individual package `*_test.go` files).

## Environment Variables

| Variable | Scope | Default | Effect |
|----------|-------|---------|--------|
| `AZD_PROVISION_CONCURRENCY` | `azd provision` (multi-layer) | `0` (unlimited, capped at `min(layerCount, GOMAXPROCS×2)`) | Overrides the scheduler's worker count for layer provisioning. Values `> 64` are clamped to `64`. Non-positive or non-integer values fall back to default. |
| `AZD_DEPLOY_CONCURRENCY` | `azd deploy` | `0` (unlimited, capped at `min(stepCount, GOMAXPROCS×2)`) | Overrides the scheduler's worker count for package/publish/deploy steps. Values `> 64` are clamped to `64`. Non-positive or non-integer values fall back to default. |
| `AZD_UP_CONCURRENCY` | `azd up` (unified DAG) | `0` (unlimited, capped at `min(stepCount, GOMAXPROCS×2)`) | Overrides the scheduler's worker count for the unified up DAG. Values `> 64` are clamped to `64`. Non-positive or non-integer values fall back to default. |
| `AZD_DEPLOY_TIMEOUT` | `azd deploy` / `azd up` | `1200` (20 minutes) | Per-service deploy timeout in whole seconds. Precedence: `--timeout` CLI flag first, then `AZD_DEPLOY_TIMEOUT`, then the default. Invalid or non-positive values cause an immediate error. |

No new environment variables are introduced at the graph engine layer — `pkg/exegraph` is
configuration-neutral. All three concurrency knobs live in the command layer and map to
the scheduler's `RunOptions.MaxConcurrency` field.

## Known Limitations

1. **Error policy** — All production paths use `FailFast`. `ContinueOnError` is
   implemented in the scheduler but not exposed as a user option.

2. **Custom workflows** — Projects defining a custom `workflows.up:` in `azure.yaml`
   bypass the **unified cross-phase** DAG and run through `workflow.Runner` instead.
   Each sub-command spawned by `workflow.Runner` (`azd provision`, `azd deploy`,
   `azd package --all`) still executes its own phase-scoped DAG internally, so
   parallel multi-layer provisioning and parallel service deploys are preserved.
   What is lost is cross-phase fusion (packaging cannot overlap with provisioning)
   and the synthetic `cmdhook-*` node integration. Projects using only project
   command hooks (no custom workflow) take the unified DAG path with `cmdhook-*`
   nodes.

## Semantic Differences from the Legacy Sequential Path

The unified DAG preserves user-observable behavior for `azd provision` and
`azd deploy` when run standalone. A small number of deliberate deviations exist
for the `azd up` path; they are documented here so they are discoverable during
code review and triage.

1. **`predeploy` project command hook runs concurrently with packaging.**
   The `cmdhook-predeploy` node depends only on provisioning, and the packaging
   chain (`cmdhook-prepackage` → `event-prepackage` → `package-<svc>` →
   `event-postpackage` → `cmdhook-postpackage`) starts in parallel with
   provisioning. `cmdhook-predeploy` therefore overlaps with the packaging
   chain. This is intentional (maximum overlap; packaging is a local operation
   that cannot meaningfully observe predeploy state). The `predeploy` hook
   still runs before `publish-<svc>` and `deploy-<svc>` because
   `event-predeploy` gates both, and the legacy `postpackage`-before-`predeploy`
   ordering is preserved because `event-predeploy` also depends on
   `cmdhook-postpackage`.

2. **`project.EventDeploy` Before listeners observe packaged artifacts.**
   `event-predeploy` depends on every `package-<svc>` step (in addition to
   `cmdhook-predeploy`), so Before-listeners see the finished package set.
   The legacy sequential path fired predeploy after packaging for the first
   service only and before packaging for subsequent services; the DAG path
   makes this ordering deterministic.

3. **`--output json` with multi-layer `provision`.**
   The legacy path emitted one JSON state dump per layer. The DAG path emits
   a single JSON dump (success or failure) using the first layer's manager
   after all layers complete. Consumers that parsed per-layer JSON on
   multi-layer deployments must switch to parsing a single document.

## Implementation Notes for Readers

Things a reader of the code should know before editing:

1. **`pkg/exegraph` is pure** — the engine imports only stdlib + OTel. It never
   reaches into azd-specific types. Adding an azd dependency here is a red flag;
   put integration glue in `internal/cmd/` instead.

2. **Step struct has no `Timeout` field.** Earlier drafts had one; it was
   promoted to a scheduler-wide `RunOptions.StepTimeout` because every production
   caller wanted the same budget for every step in a given run. If you need
   truly per-step timeouts, add them to `Step` and combine with `RunOptions.StepTimeout`
   via `min()` inside `runStep`.

3. **Cancellation has two distinct flavors** and they are classified differently:
   - **Parent/FailFast cancel** (`runCtx.Err() != nil`) → affected steps are
     recorded as `StepSkipped`, their errors are *not* appended to `allErrors`,
     and they do *not* appear in `result.Failed`. The canceling error (`ctx.Err()`)
     is appended once at run boundary if no other error exists, so
     `ctx.Cancel()` always surfaces a non-nil `Run()` error.
   - **Per-step `StepTimeout` expiration** (`runCtx.Err() == nil`) → a real
     step failure. Appears in `allErrors` and `result.Failed`, triggers
     FailFast tear-down of peers.
   Search for `isSchedulerCancel` in `scheduler.go` for the classification code.

4. **`provision_graph.go` single-layer path is deliberately a one-node graph.**
   It could be written as "call the manager directly, skip the engine," but
   that would require two code paths. The one-node graph uses the injected
   `provisionManager` verbatim, preserves mock-friendliness for unit tests, and
   keeps observability (spans, events) identical across 1-layer and N-layer
   runs. Do not "optimize" it away.

5. **Preview (`--preview`) bypasses the engine entirely.** Preview is always
   single-layer, has no hooks, no env writes, no cache invalidation, and its
   UX is disjoint from provision. The bypass lives in `provisionPreview` —
   see `provision_graph.go` around the `--preview` check.

6. **Error unwrapping at the boundary is non-negotiable.** The scheduler prefixes
   step errors with `step "NAME" failed: `. Callers in `provision_graph.go` and
   `deploy.go` run the result through `unwrapStepErrors` before handing it to
   the existing wrappers (`wrapProvisionError`, etc.) so user-facing errors look
   identical to the legacy path. Skipping this unwrap breaks golden tests and
   UX expectations.

7. **Subscription/location prompts happen before the graph starts.** For
   multi-layer provision, the shared manager resolves subscription & location
   once (interactively if needed) *before* per-layer managers are constructed.
   Otherwise two layers would race to prompt the user. See the comment block
   in `provisionLayersGraph` describing the "resolve before spawn" invariant.

8. **Per-layer env clones are deep copies, but cross-layer hook writes need a
   reload.** Each multi-layer step gets its own `Environment` clone so
   concurrent `env.DotenvSet` writes don't race. Outputs from successful layer
   completion are written back into the shared env. Cross-layer output
   references are resolved by `layer_deps.go` at graph-build time (not at
   runtime). Hook-mediated edges (where layer A's `postprovision` hook writes
   `azd env set FOO=bar` that layer B's `.bicepparam` reads) are NOT
   detectable statically — authors must declare them via
   `infra.layers[].dependsOn`.

   `runProvisionSingleLayer` runs an 8-step lifecycle inside one scheduler
   step: pre-hooks → pre-event → `mgr.Deploy` → env merge → service event →
   post-event → post-hooks → **reload** `deps.env` from disk. The merge step
   reloads `deps.env` *before* applying outputs and saving, so the parent
   process's stale in-memory state never clobbers subprocess writes from
   pre-hooks. The final reload (step 8) re-syncs `deps.env` after post-hook
   subprocess writes, so any downstream layer's clone observes those writes
   at its own step 0. Both reloads happen under `envMu`. Concurrent sibling
   layers (no `dependsOn` edge) intentionally do NOT see each other's
   mid-flight hook writes — that's the contract `dependsOn` exists to
   override. The merge / reload helpers are extracted as
   `mergeLayerOutputsLocked` / `reloadSharedEnvLocked` and tested directly.

9. **Build gate is policy-driven, not Aspire-specific.** The DAG builder
   (`service_graph.go`) is ecosystem-agnostic: it consumes an opaque-string
   `buildGateKey` callback on `serviceGraphOptions`. Services returning the
   same non-empty key serialize on the first one — "first wins, rest wait" —
   and services returning `""` run in full parallelism. Today the only gate
   in use is `aspireBuildGateKey` (in `cli/azd/internal/cmd/aspire_gate.go`),
   which returns `"aspire"` for services whose `DotNetContainerApp` options
   are populated by the Aspire importer. That gate exists because the .NET
   AppHost build is shared state, not because deploy itself can't
   parallelize. When Aspire moves into an extension, only that helper — not
   the DAG builder — has to move with it.

10. **`deploy_progress.go` renders differently in interactive vs CI.**
    Interactive mode rewrites terminal lines with ANSI escapes;
    non-interactive mode (detected via `console.IsSpinnerInteractive()`)
    prints one line per state transition and makes `RenderFinal` a no-op.
    This is deliberate: `--output json` must never have progress noise
    interleaved with the JSON document.

11. **`runStep` panic recovery converts panics to errors, not process aborts.**
    A panicking step is logged with stack trace and its status is `StepFailed`.
    FailFast tear-down still applies. If you want "crash the whole CLI on
    panic" behavior, remove the `recover()` in `runStep` — but understand that
    will take down parallel peer steps mid-flight.

12. **OTel attribute keys live in `internal/tracing/fields/fields.go`.**
    When adding a new `exegraph.*` span attribute, define the `AttributeKey`
    there (not inline in `scheduler.go`) so the telemetry schema stays
    centralized.

13. **Multi-layer provision adoption telemetry.** Each `azd provision` /
    `azd up` run that takes the multi-layer path emits four `provision.layer.*`
    attributes on the ambient command span (defined alongside the
    `exegraph.*` keys in `internal/tracing/fields/fields.go`):

    | Attribute | What it measures |
    |---|---|
    | `provision.layer.count` | Total `infra.layers[]` declared |
    | `provision.layer.max_parallel` | Largest dependency level after analysis (max achievable parallelism) |
    | `provision.layer.safe_fallback_count` | Layers that triggered the safe-by-default detector fallback |
    | `provision.layer.explicit_dependson_count` | Layers using `infra.layers[].dependsOn` |

    These are SystemMetadata only — counts, no template content — and let
    the azd team answer "what fraction of projects use multi-layer?",
    "how parallel is the typical project?", and "how often does the
    safe-by-default fallback engage on real templates?" without inspecting
    user content. As of this PR, an `org:Azure-Samples filename:azure.yaml`
    audit found **zero awesome-azd templates using multi-layer
    `infra.layers[]`**; only three community user repositories on GitHub
    declare it. These attributes will let us track whether that changes
    after the feature ships.