# Environment-state API migration

The environment-state validation update intentionally changes several exported Go
APIs to return errors. This is a source-incompatible exception to the
[Go module compatibility policy](sdk-versioning.md#compatibility), not a
backward-compatible signature change. Callers importing these packages must
migrate when upgrading to a module version containing this update.

- `azdcontext.AzdContext.EnvironmentRoot` and `GetEnvironmentWorkDirectory` now
  return `(string, error)`. The session-state getter also returns an error.
- `environment.DataStore`, `environment.Manager`, and their implementations now
  return `(string, error)` from `EnvPath` and `ConfigPath`. Custom implementations
  and mocks must update both signatures. Dev Center does not expose a configuration
  file path; its `ConfigPath` returns an error wrapping `errors.ErrUnsupported`.
- `state.NewStateCacheManager` now accepts `*azdcontext.AzdContext` instead of an
  environment-directory string. Construct the context with the project directory,
  not its `.azure` directory. `GetCachePath` and `GetStateChangePath` now return
  `(string, error)`.

Check and propagate each error before using its accompanying path or session.
Do not substitute an empty path or treat a failed state read as missing state.
The update deliberately does not add deprecated wrappers that discard errors or
panic during normal input validation.
