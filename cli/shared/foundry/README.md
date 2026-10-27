# Shared Foundry module

```go
module github.com/azure/azure-dev/cli/shared/foundry
```

This independently versioned Go library is a build-time dependency for Foundry
extensions. It is not an azd extension or gRPC capability and has no registry
entry. Its `go.mod`, implementation, and transitive build graph must not require
or import `github.com/azure/azure-dev/cli/azd` or extension-internal packages.

## Scope and compatibility

`access/` provides verified project binding, operation-specific probes, portable
typed errors, conservative RBAC grants, and bounded propagation. Interfaces use
module-owned types and standard Go contexts, not host SDK messages. See the
[access design](docs/access-design.md) for boundaries and safeguards.

The API starts at **v0.x**. Record breaking changes explicitly in release notes;
consumers pin exact released versions. v0 does not imply a stability guarantee.
Release independently from azd core and extension binaries using
`cli/shared/foundry/v0.x.y` tags. See [release instructions](RELEASING.md).

This access foundation aligns with [#9834](https://github.com/Azure/azure-dev/issues/9834),
but **does not complete that issue**. Its initial configuration extraction still
needs a dedicated change: move file-reference resolution, YAML editing, and
environment expansion from `cli/azd/pkg/foundry` into `config/` with parity tests,
then retain deprecated forwarding APIs in core for azd v1. This change does not
remove, duplicate, or modify that existing exported configuration implementation.

## Consumer migration

1. Publish the shared module independently of core.
2. Adopt `github.com/azure/azure-dev/cli/shared/foundry/access` in toolboxes as
   the proving consumer for [#10718](https://github.com/Azure/azure-dev/issues/10718).
3. Migrate agents, skills, routines, datasets, and evaluations in separate PRs.
   Keep SDK identity/environment adaptation and structured-error translation at
   each extension boundary; the small adapters contain no separate RBAC algorithm.
4. Add a direct `require .../cli/shared/foundry v0.x.y` in each consumer. Test it
   as an ordinary Go module without a workspace or `replace` before merging.
   An external workspace may be used for draft local validation before the tag exists.
5. Follow the #9834 configuration extraction and compatibility-wrapper plan.
   Projects, agents, connections, toolboxes, routines, and evaluations currently
   importing `cli/azd/pkg/foundry` should migrate to the future `config/` package
   in separate PRs after its first tag. Do not remove core's exported v1 surface.

No shared-module tag has been published by this change. A successful tagged,
no-replacement consumer and exercised first release are release gates, not claims
made by this draft. Additional consolidation candidates are project-context
cascades, credentials, environment keys, and configuration helpers; track their
extraction separately under #9834 rather than expanding the access change.

## Development and dependency policy

From this directory, independent of the core and extension workspaces:

```sh
GOWORK=off go mod download
GOWORK=off go build ./...
GOWORK=off go test ./... -count=1
GOWORK=off go test -race ./... -count=1  # requires a supported CGO toolchain
GOWORK=off go vet ./...
python eng/check-boundaries.py
```

CI builds/tests the module independently on Windows and Linux, runs race tests on
Linux, and checks dependency boundaries, formatting, copyright, lint, and spelling.

The module owns its dependency upgrades through Dependabot. Initial Azure SDK
versions align with the existing consumer/core pins, but a library release does
not require a core dependency update. For versions also used by extensions,
review Go minimal-version selection and test each proving consumer at adoption
or upgrade time; update compatible extension pins together where appropriate.
Do not silently edit core/extension module files from this module's tooling.
This is the independent-module counterpart to core's extension dependency sync
policy, not an exemption permitting unreviewed compatibility drift.
