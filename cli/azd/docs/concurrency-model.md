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

| Lock                       | Protects                          | Acquired by                                                        |
|----------------------------|-----------------------------------|--------------------------------------------------------------------|
| `expandedEnvMu sync.Mutex` | `expandedEnvCache` per-target map | `expandServiceEnv` (read-cache and write-cache critical sections)  |

**Contract**: Each service-target instance is shared across the
package/publish/deploy steps for that service AND across parallel services
that happen to share a target. The cache is a memoization optimization, not
a correctness requirement, but the underlying map mutation must be guarded.

**Why it matters**: Container-Apps and AKS targets compute and persist
template-hash values (`SetServiceProperty` → `env.DotenvSet`) under
`expandedEnvMu` AND under `Environment.mu`. Adding a new write path that
touches the dotenv must acquire `Environment`'s lock; adding a new write
that mutates the per-target cache must acquire `expandedEnvMu`. **Do not
introduce a third disjoint mutex** — splitting the dotenv writes across
multiple unrelated mutexes creates a split-mutex race (one goroutine writes
under lock A, another reads under lock B, both touching the same map).

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
