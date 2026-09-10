# Go dependency version synchronization

`cli/azd/go.mod` is the source of truth for dependencies shared by azd core and
first-party Go extensions.

When an extension directly requires a dependency that is also directly required
by core, both modules must use the same version. This prevents an extension from
silently moving ahead of or falling behind the dependency version tested by
core. Indirect requirements are not checked because `go mod tidy` derives them
from each module's dependency graph.

## Check and synchronize versions

Run these commands from `cli/azd`:

```bash
mage checkDependencyVersions
mage syncDependencyVersions
```

The check command reports mismatches without changing files. The sync command
updates unapproved mismatches to the versions in the core `go.mod` and runs
`go mod tidy` in each changed extension.

## Temporary overrides

If an extension cannot align with core, add an entry to
`dependency-versions.json`. An override must:

- identify the extension and dependency;
- pin the exact exceptional version;
- explain why the exception is required; and
- link to an `Azure/azure-dev` issue tracking its removal.

Overrides are temporary. Validation fails when an override no longer matches an
active version difference, ensuring stale entries are removed.

When an extension needs a newer dependency, prefer upgrading core and every
other extension that directly requires the dependency in the same change.
