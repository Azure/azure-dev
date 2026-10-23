# cli/shared

Independently versioned Go modules intended for reuse by two or more azd extensions.

## Contract

- Modules under `cli/shared/` are **build-time** (source) dependencies compiled into extension binaries. They are **not** installable azd extensions, runtime providers, or gRPC services, and must not ship an `extension.yaml` or registry entry.
- Each child module owns its own `go.mod`, dependencies, tests, compatibility policy, and release tags.
- Shared modules are released independently from azd core and from installable extension artifacts.
- Go module tags use the child module directory prefix, for example `cli/shared/foundry/v0.1.0`.
- While package boundaries and compatibility policy stabilize, shared modules use `v0.x` tags.
- Shared modules must not import extension `internal/` packages.
- Shared modules should not depend on `github.com/azure/azure-dev/cli/azd` unless that dependency is deliberate, documented, and does not defeat independent versioning.

## Modules

| Module | Path | Purpose |
|--------|------|---------|
| foundry | [`cli/shared/foundry`](./foundry) | Configuration and helpers shared by Microsoft Foundry extensions |
