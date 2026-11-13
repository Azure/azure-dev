# Shared CLI modules

`cli/shared/` contains independently versioned Go modules reused by two or more
extensions. They are source/build dependencies compiled into extension binaries,
not installable azd extensions, runtime providers, or gRPC services. Do not add
`extension.yaml` files or extension registry entries for these libraries.

Each child module owns its `go.mod`, dependency versions, tests, compatibility
policy, release notes, and release tags. Releases are independent of azd core
and of extension binary artifacts. A nested module uses its directory prefix in
tags: `cli/shared/foundry/v0.1.0`, not `v0.1.0` or an `azd-ext-*` binary tag.
Follow Go semantic import versioning for v2 and subsequent major versions.

Shared modules must not import an extension's `internal/` packages. Dependencies
on azd core must be deliberate and documented, and must not defeat independent
release. The Foundry module specifically forbids a core-module dependency.
Host SDK/gRPC adaptation and product-specific command policy belong in consumers.

Adding a new child module under `cli/shared/` requires at least two concrete
consumers or an approved migration plan identifying those consumers. The initial
[Foundry module](foundry/README.md) provides shared project access readiness
(`access/`) and typed errors (`ferrors/`) for the Foundry extensions
(`azure.ai.agents`, `azure.ai.toolboxes`, `azure.ai.skills`, and sibling
`azure.ai.*` extensions). Each consumer is migrated in a separate PR after a
shared-module tag is available.
