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

## Service Deploy Ordering

Service deployment uses a **sequential-by-default** model to preserve
backward compatibility with existing templates:

| Scenario | Deploy ordering | Why |
|----------|----------------|-----|
| No service declares `uses:` targeting another service | **Sequential** in alphabetical order | Templates relied on implicit ordering; alphabetical matches legacy `ServiceStable()` |
| Any service declares `uses: [other-service]` | **Graph-driven** per declared edges | Explicit deps enable safe parallelism |

**Package and publish steps always run in parallel** regardless of `uses:`
declarations — only deploy ordering is affected by the fallback.

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

When **no service** in the project declares a `uses:` entry targeting
another service, the graph builder chains deploy steps in alphabetical
order (matching the `ServiceStable()` sort used by the legacy sequential
path). This prevents regressions in templates where one service reads
environment variables (e.g. `SERVICE_API_ENDPOINT_URL`) set by a
previously deployed service.

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
| `mu sync.RWMutex`        | `dotenv map[string]string`, `deletedKeys`         | `Getenv`, `LookupEnv`, `Dotenv`, `DotenvSet`, `DotenvDelete`, `Reload`, all helpers |

**Contract**: All readers acquire `mu.RLock()`; all writers acquire `mu.Lock()`.
Iteration over the underlying map (e.g. snapshotting for a hook) must hold
the lock for the duration of the iteration — do not release the lock and
then range over a captured map reference.

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
cycle snapshots `dotenv`/`deletedKeys` under `env.mu.RLock()`, calls
`reloadLocked` (which acquires `env.mu` internally via `replaceState`),
then overlays the snapshot and replays deletions under `env.mu.Lock()`.
This ensures the overlay writes don't race with concurrent `DotenvSet`/
`DotenvDelete` calls from parallel service publishes.

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
