# Releasing the Foundry shared module

This module starts at v0.x and is released independently of azd core and extension
binary artifacts. Publishing a Go module requires a repository tag, not an
`extension.yaml`, extension bundle, or registry update.

Before a release:

1. Merge the reviewed shared-module change and confirm the independent CI passed.
2. Review public API compatibility and update `CHANGELOG.md`. For v0 releases,
   explicitly record any breaking changes.
3. Verify `GOWORK=off` build/tests, race tests on a supported platform, and
   `python eng/check-boundaries.py` from the module directory.
4. Review dependency updates and compatibility with the proving consumers.
5. An authorized maintainer creates and pushes an annotated tag on the reviewed
   commit: `cli/shared/foundry/v0.1.0` for the first release. Do not use the core
   `cli/azd/v*` tags or binary `azd-ext-*` tags. Do not create a tag for an
   unmerged draft or move a published tag.
6. Verify `GOWORK=off go mod download -json github.com/azure/azure-dev/cli/shared/foundry@v0.1.0`
   succeeds through the normal Go module infrastructure.
7. Update each consuming extension's direct dependency to that tag; remove any
   draft workspace/replace directives and build/test the extension as its own
   module.

For v2 or later, follow Go semantic import versioning, including the `/v2`
module/import suffix.
