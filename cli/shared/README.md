# Shared CLI modules

`cli/shared/` contains independently versioned Go modules reused by two or more
extensions. They are source/build dependencies compiled into extension binaries,
not installable azd extensions, runtime providers, or gRPC services. Do not add
`extension.yaml` files or extension registry entries for these libraries.

Each child owns its `go.mod`, dependency versions, tests, compatibility policy,
release notes, and release tags. Releases are independent of azd core and of
extension binary artifacts. A nested module uses its directory prefix in tags:
`cli/shared/foundry/v0.1.0`, not `v0.1.0` or an `azd-ext-*` binary tag. Follow Go
semantic import versioning for v2 and subsequent major versions.

Shared modules must not import an extension's `internal/` packages. Dependencies
on azd core must be deliberate and documented, and must not defeat independent
release. The Foundry module specifically forbids a core-module dependency.
Host SDK/gRPC adaptation and product-specific command policy belong in consumers.

A new child module requires two concrete consumers or an approved migration
plan identifying them. The initial [Foundry module](foundry/README.md) has a
migration plan for toolboxes first, followed by agents, skills, routines,
datasets, and evaluations. Each consumer is migrated in a separate PR after a
shared-module tag is available.
