# Concurrency Model

This document describes the concurrency contracts that the azd parallel execution
engine ([exegraph](../pkg/exegraph)) places on long-lived service types.

Before the graph-driven `up`/`provision`/`deploy` pipelines, almost every type
in azd was assumed to run on a single goroutine. Parallel layer provisioning
and parallel service `package`/`publish`/`deploy` changed that — multiple
graph step actions can now read and mutate shared state at the same time.

The types listed below now have **explicit locking contracts**. If you are
adding a method or a write path to one of them, you MUST acquire the
documented lock before touching the protected fields. Adding an unguarded
write produces a race that often only surfaces under parallel deploys
(`-race` tests catch it; single-goroutine tests do not).

When in doubt: read the field comment in the source. The lock and the field
it protects are co-located by convention.

---

## Scheduler limits and phase groups

The graph scheduler applies a hard global ceiling and optional limits for named
groups. It admits ready work in round-robin order across groups while preserving
critical-path priority within each group. Limits are maxima, not reservations.
When other groups have no ready work, one group can use every available global
slot up to its own limit.

The scheduler coordinator enforces all limits before dispatch. Workers never
wait for group capacity, so package work cannot occupy every worker while it
waits for another package step to finish. Active work is not preempted, but a
continuously ready group cannot starve another ready group when slots become
available.

| Command | Hard global ceiling | Phase groups |
|---------|---------------------|--------------|
| `azd up` | `AZD_CONCURRENCY_MAX`, then `AZD_UP_CONCURRENCY`, then `AZD_DEPLOY_CONCURRENCY`, then the scheduler default | Package: `AZD_PACKAGE_CONCURRENCY`, then `AZD_UP_CONCURRENCY`; provision: `AZD_PROVISION_CONCURRENCY`, then `AZD_UP_CONCURRENCY`; publish and deploy: `AZD_DEPLOY_CONCURRENCY`, then `AZD_UP_CONCURRENCY` |
| `azd deploy` | `AZD_CONCURRENCY_MAX`, then `AZD_DEPLOY_CONCURRENCY`, then the scheduler default | Package: `AZD_PACKAGE_CONCURRENCY`, then `AZD_DEPLOY_CONCURRENCY`; publish and deploy: `AZD_DEPLOY_CONCURRENCY` |
| `azd provision` | `AZD_CONCURRENCY_MAX`, then `AZD_PROVISION_CONCURRENCY`, then the scheduler default | Provision: `AZD_PROVISION_CONCURRENCY` |

Package and provision work in `azd up` can overlap while retaining independent
limits. Publish and deploy share one budget because both are part of the
deployment phase. Standalone `azd package` remains sequential.

All configured values are positive integers clamped to `64`. An explicitly set
invalid or non-positive value disables that limit and blocks fallback. Fallback
occurs only when the higher-precedence variable is unset.

---

## Service Deploy Ordering

Service deployment uses a **sequential-by-default** model to preserve
backward compatibility with existing templates:

| Scenario | Deploy ordering | Why |
|----------|----------------|-----|
| No service declares `uses:` targeting another service and no deploy build-gate policy applies | **Sequential** in the service order supplied to the graph | Preserves the earlier implicit deploy ordering |
| Any service declares `uses: [other-service]` | **Graph-driven** per declared edges | Explicit deps enable safe parallelism |
| A deploy build-gate policy applies (currently Aspire) | **Graph-driven**, with local image preparation coordinated at runtime | Avoids unnecessarily serializing the subsequent Azure deployments |

Package and publish graph steps remain eligible to run in parallel regardless
of `uses:` declarations. The standard .NET compatibility fallback described
below may serialize only the local `dotnet publish` subprocesses.

### .NET build isolation

Parallel .NET publishes can share transitive `ProjectReference` outputs even
when each command has a different final `--output` directory. Without further
isolation, those commands can contend on source-tree `bin` and `obj` files.

The service graph coordinates local .NET builds without adding dependency
edges:

- Every non-Aspire service whose configured language is .NET receives the
  opaque `"dotnet"` coordination key.
- The isolation helper is consumed by standard .NET package operations,
  including Functions and App Service, and by .NET container publishing when
  no Dockerfile is present. Generic `dotnet restore`, `dotnet build`, and
  explicit Dockerfile builds are unchanged.
- When the gate is present and the installed SDK is 8.0.100 or later, each
  local `dotnet publish` receives a unique temporary `--artifacts-path`.
  Intermediate outputs are isolated while the existing final package output
  or container image remains unchanged.
- With an older SDK, a failed SDK capability probe, or a temporary-directory
  creation failure, standard .NET operations sharing the key use the same
  mutex. The lock is held only around the `dotnet publish` subprocess, not the
  subsequent Azure deployment.
- Temporary-tree cleanup is attempted after the operation succeeds or fails.
  A cleanup failure is logged and does not replace the publish result.

The standard .NET package/publish gate is enabled only when at least two
services receive the `"dotnet"` key and effective graph concurrency is not
`1`. This gate does not count as explicit deploy ordering, so standard services
without `uses:` retain the sequential deploy fallback described above.

`azd deploy --from-package` bypasses source packaging and does not enable the
standard .NET package/publish gate. The existing Aspire deploy-time build gate
remains separate; this policy does not change Aspire behavior. The importer
currently rejects an Aspire AppHost alongside another explicitly configured
service, and every service imported from an AppHost manifest is Aspire-managed,
so the two gate populations do not coexist in a normal project graph.

Projects that explicitly override the MSBuild `BaseOutputPath` or
`BaseIntermediateOutputPath` properties may bypass the SDK's
`--artifacts-path` isolation. Set `AZD_CONCURRENCY_MAX=1` for `azd deploy`
or `azd up` when such projects share build outputs. This serializes all graph
steps even when phase-specific concurrency limits are set.

### How `uses:` enables parallel deployment

In `azure.yaml`, the `uses:` field on a service declares deploy-time
dependencies. When `web` declares `uses: [api]`, `deploy-web` waits for
`deploy-api` to complete before starting. Services without mutual `uses:`
edges deploy in parallel.

```yaml
services:
  api:
    host: containerapp
    language: python
  web:
    host: containerapp
    language: js
    uses:
      - api   # deploy-web waits for deploy-api
  worker:
    host: containerapp
    language: python
    # no uses: → deploys in parallel with api (or sequentially if no
    # service in the project declares any uses: edges)
```

When **no service** in the project declares a `uses:` entry targeting another
service and no deploy build-gate policy applies, the graph builder chains
deploy steps in the service order supplied to the graph. This prevents
regressions in templates where one service reads environment variables
(e.g. `SERVICE_API_ENDPOINT_URL`) set by a previously deployed service.

A diagnostic log message is emitted when the sequential fallback activates:
> `deploying N services sequentially (no uses: edges declared; add uses: to azure.yaml to enable parallel deployment)`

### Environment variable flow during deployment

Each service's `Deploy` step writes `SERVICE_<NAME>_ENDPOINT_URL` into the
shared `.env` after completing. In sequential mode, a later service can read
earlier services' endpoint URLs because the `.env` is updated between steps.
In parallel mode with explicit `uses:` edges, the same guarantee holds
because `deploy-web` doesn't start until `deploy-api` has written its
endpoint URL.

**If you depend on another service's endpoint URL, declare `uses:`.**

---

## `pkg/environment.Environment`

| Lock                     | Protects                                          | Acquired by                                                                |
|--------------------------|---------------------------------------------------|----------------------------------------------------------------------------|
| `mu sync.RWMutex`        | Identity, dotenv, deletion tracking, and environment config | Variable access, `Config()` operations, `SnapshotState`, `ReplaceState`, `MergeAndSave` |

**Contract**: All readers acquire `mu.RLock()`; all writers acquire `mu.Lock()`.
Iteration over the underlying map (e.g. snapshotting for a hook) must hold
the lock for the duration of the iteration — do not release the lock and
then range over a captured map reference.

`Env` is the single interface for variable access, configuration, and raw state.
`Environment` remains the concrete implementation returned by constructors and
the manager; data stores accept `Env` so a view can be saved or reloaded without
being unwrapped. Provider views may map variable access, but must delegate
identity, `Config()`, `SnapshotState`, `ReplaceState`, and `MergeAndSave`
unchanged to the underlying environment.

`SnapshotState` returns an `EnvironmentState` with detached dotenv and config data. It includes loader
variables that `Dotenv()` filters out, excludes process-environment fallback
values, and preserves unresolved secret references and vault state.
`ReplaceState` installs a detached copy in the same environment and clears pending
deletions. Explicit reload discards pending in-memory changes; it is not a merge.
Stores load and validate both files before replacing any state.

`Config()` returns a stable view, not the underlying config object. Retained views
continue to operate on the current config after reload. Config reads and writes
use the environment lock; returned maps/slices and mutable values supplied to
`Set` are detached. Reads, including secret resolution and cloning, use the read
lock; `Set`, `SetSecret`, and `Unset` use the exclusive lock.
Use `Config().Set`/`Unset` to modify config, rather than mutating values returned
by `Raw` or `Get`.

`FileConfigManager.Save` accepts these views and snapshots them before acquiring
its own lock. This preserves unsaved secrets and keeps the lock order consistent
with environment saves: environment lock, then file-manager lock.

`Name()` is the single identity accessor, including for storage and lock paths.
An explicitly supplied name wins over `AZURE_ENV_NAME`. When no name is known,
the accessor checks the environment's values first, then the process environment
if the key is absent. It caches a nonempty result under the environment lock;
later variable updates or reloads do not rename the environment.
Local, blob, and Dev Center save/reload operations resolve and validate this name
before accessing storage. Missing or invalid names (including `.` and `..`) fail
instead of addressing the project-level `.azure` directory. Views must delegate
`Name()` unchanged rather than deriving identity from mapped variables.

Dev Center reload updates a detached snapshot, then replaces the live state only
after configuration sync and output retrieval succeed. Discovered platform
settings are also kept in a private copy until the environment update succeeds,
so a failed reload leaves both sets of live data unchanged. Settings obtained
from prompts are still copied into the environment on a successful retry.

**Why it matters**: `Environment` is shared across parallel layer provision
steps, parallel service deploy steps, and pre/post-provision/-deploy hooks.
A second goroutine reading `dotenv` while another writes it is a data race
and Go's runtime will panic on a concurrent map write.

---

## `pkg/environment.Manager`

| Lock                       | Protects                                              | Acquired by                                                       |
|----------------------------|-------------------------------------------------------|-------------------------------------------------------------------|
| `cacheMu sync.RWMutex`     | `cache map[string]*Environment` (env-name → instance) | `Get`, `LoadOrCreateInteractive`, `Save`, `Reload`, `cachePut`    |
| `saveMu sync.Mutex`        | The .env file write critical section                  | `Save` (held across read → merge → write to prevent torn writes)  |

**Save path in `local_file_data_store.Save()`**: The reload-merge-write
cycle reads the stored dotenv under the file lock without modifying live state,
then passes it to `MergeAndSave` as `storedDotenv`. That operation holds `env.mu.Lock()` across merging the
current in-memory values and deletions, writing config and dotenv, and committing
the merged dotenv. Setters that arrive during the write wait and remain pending
afterward. On write failure, live state and deletion tracking remain unchanged;
only a successful write acknowledges pending deletions.

The `MergeAndSave` writer callback receives detached state. It must not call the
environment (including `Config()` methods) or re-enter manager/local-store
`Save`/`Reload` paths that reacquire `manager.saveMu` or the `.env` flock. It may
call `FileConfigManager.Save` with detached `state.Config`; that mutex is acquired
after `env.mu`. Compute paths before entering the callback. The lock is
deliberately held during I/O; separate snapshot and commit locks would allow
intervening writes to be lost.

The dotenv write still uses a sibling temporary file and atomic rename.
Config and dotenv are not a single transactional file pair: a later failure can
leave config written while dotenv is unchanged. In-memory state is retained so
the operation can be retried. Blob saves serialize one detached snapshot, so both
uploads describe the same captured state, but the uploads are not atomic together.

**Contract**: `cacheMu` ensures every caller asking for env "X" gets the
**same** `*Environment` instance — without this, parallel deploy steps would
each get their own copy and writes would diverge. `saveMu` serializes the
read-modify-write cycle on the .env file so two concurrent `Save` calls
cannot interleave and clobber each other's writes.

**Why it matters**: A future `Manager` method that loads or persists
environment state must take the appropriate lock or it will either return
inconsistent instances (cache miss → divergent writes) or corrupt the .env
file on disk.

---

## `pkg/tools/kubectl.Cli`

| Lock              | Protects                                  | Acquired by                                                   |
|-------------------|-------------------------------------------|---------------------------------------------------------------|
| `mu sync.Mutex`   | `env map[string]string`, `cwd`, `kubeConfig` | `WithEnv`, `WithCwd`, `Cwd`, `Env`, all `Exec`/`applyTemplate` reads |

**Contract**: A single `kubectl.Cli` instance is shared across all parallel
deploy steps. Setters (`WithEnv`/`WithCwd`) take the write lock; readers
(`Exec`, `applyTemplate`) snapshot under the lock and then run the external
process without holding it.

**Why it matters**: Without `mu`, two AKS service-target goroutines could
race on `env` (one writing `KUBECONFIG=…`, the other reading it for an
`Exec`) and produce non-deterministic command-line behavior.

---

## `pkg/tools/docker.Cli`

| Synchronization | Protects | Used by |
|-----------------|----------|---------|
| `engineOnce sync.Once` | Initialization of `containerEngine` and `engineErr` | `selectContainerEngine` |

**Contract**: Runtime selection happens once per `Cli`, on the first call that needs an engine name. `selectContainerEngine` reads `AZD_CONTAINER_RUNTIME` and PATH inside `engineOnce.Do`, then publishes an immutable engine name and selection error. Every reader goes through `selectContainerEngine`; no other code may write these fields. Changing the environment or PATH requires a new `Cli`.

The selected value uses the shared `tools.ContainerEngine` type and its Docker/Podman constants through `ContainerHelper` and the .NET container methods. String conversion happens when constructing external commands. The .NET methods also accept the zero value to use the SDK's default runtime.

`ContainerEngine`, `Name`, `InstallUrl`, and container operations use that same selection. Lightweight name lookup defaults to Docker if selection fails; `CheckInstalled` reports the cached selection error. Each `CheckInstalled` call repeats version and daemon checks outside `sync.Once`, so readiness failures and cancellations are not cached. Builds and other container subprocesses also run outside `sync.Once`.

**Why it matters**: Parallel services and remote-build fallbacks share the
singleton `docker.Cli`.

---

## `pkg/project.containerAppTarget` and `pkg/project.aksTarget`

These targets no longer carry package-level `envMu` / `aksEnvMu` mutexes
or per-target `expandedEnvCache`/`expandedEnvMu` fields. All dotenv
access is protected by `Environment.mu` internally, and `Manager.saveMu`
serializes disk writes. The external mutexes were removed once
`Environment` became internally thread-safe (see above).

**Contract**: Adding a new write path that touches the dotenv map does
NOT need an external mutex — `Environment` handles that internally.
AKS Kustomize env expansion (`K8s.Kustomize.Env.Expand`) reads from
`env.Getenv` which acquires `Environment.mu.RLock()` internally.

---

## `pkg/project.serviceManager`

| Lock              | Protects                                            | Acquired by                                              |
|-------------------|-----------------------------------------------------|----------------------------------------------------------|
| `mu sync.Mutex`   | `initialized map[*ServiceConfig]map[any]bool`       | `Initialize`, `runHooks`, all per-service init bookkeeping |

**Contract**: `initialized` tracks "has this service config been initialized
for this consumer" so duplicate `Initialize` calls are no-ops. With parallel
service deploys, two goroutines may race on the same `ServiceConfig` and
both attempt initialization; the lock ensures only one succeeds.

---

## Lock Acquisition Order

Consistent lock ordering prevents deadlocks. The environment persistence path
uses this nested hierarchy for local saves:

```text
1. manager.saveMu         (in-process sync.Mutex — serializes goroutines)
2. local flock             (cross-process OS file lock — serializes processes)
3. env.mu                  (per-Environment sync.RWMutex)
4. fileConfigManager.mu    (in-process config save mutex, acquired under env.mu)
```

A Manager-mediated local Save takes all four locks. Reload takes the first three;
config loading does not acquire `fileConfigManager.mu`. Paths that acquire only a
subset must preserve this relative order. Never acquire an outer lock while
holding an inner one.

### Why subprocess hooks cannot deadlock

Parallel service hooks (pre/post-deploy, pre/post-package) may spawn `azd env set`
subprocesses concurrently. Each subprocess is a separate OS process with its **own**
`manager.saveMu` instance — in-process mutexes are not shared across processes.

Cross-process serialization is handled entirely by **flock** (the OS-level file
lock on the `.env.lock` file). Within each subprocess the same acquisition order
applies: Save uses saveMu → flock → env.mu → fileConfigManager.mu, while Reload
stops at env.mu. Since saveMu is per-process and never shared across process
boundaries, circular wait is impossible.

```text
Parent azd process (saveMu serializes goroutines A and B):
  goroutine A: saveMu -> flock -> env.mu -> fileConfigManager.mu
  goroutine B: saveMu -> flock -> env.mu -> fileConfigManager.mu

Hook subprocess 1 (azd env set FOO=bar):
  main: saveMu -> flock -> env.mu -> fileConfigManager.mu

Hook subprocess 2 (azd env set BAZ=qux):
  main: saveMu -> flock -> env.mu -> fileConfigManager.mu

Across processes, only flock provides mutual exclusion.
Within a process, saveMu serializes Save/Reload; fileConfigManager.mu
protects the config write beneath env.mu.
```

### What is held during subprocess invocations

When the parent process launches hook subprocesses, it does **not** hold saveMu
or flock during the subprocess lifetime. The hook runner starts the subprocess
and waits for it to exit — locks are only acquired when the subprocess (or the
parent) calls `Save`/`Reload`. This means hook subprocesses never contend with
locks held by the parent's hook-launch path.

---

## Adding new concurrent state

When you introduce a new field on one of the types above (or a new type that
will be shared across graph steps), follow this checklist:

1. **Decide the lock granularity**. A single `mu sync.Mutex` co-located with
   the protected fields is the default. Reach for `sync.RWMutex` only when
   the read path dominates and contention measurements justify it.
2. **Co-locate the lock with its fields**. Place the `sync.Mutex` field
   immediately above the fields it protects, and add a one-line comment
   stating exactly what is protected.
3. **Hold the lock across the full critical section**. In particular, a
   read-modify-write on a map MUST hold the lock from the read through the
   write — releasing in between is a TOCTOU race.
4. **Do not call into other locked types while holding your lock** unless
   you have verified the lock-acquisition order is consistent across all
   call sites. Inconsistent ordering across two locks = deadlock.
5. **Test with `go test -race`**. Single-goroutine tests will not catch
   missed locks; the race detector will.

---

## Why this exists (one-paragraph history)

Before [#7776](https://github.com/Azure/azure-dev/pull/7776), azd's `up`,
`provision`, and `deploy` commands ran each service and each layer
sequentially. Most state types had no explicit concurrency model because
none was needed. The graph-driven engine introduced in that PR runs
multiple service steps and (when `infra.layers[]` is configured) multiple
layer provision steps in parallel — and surfaced races in the types listed
above. The locks documented here were added to make those types safe; this
document exists so the next contributor adding a method to one of them
knows they must keep them safe.
