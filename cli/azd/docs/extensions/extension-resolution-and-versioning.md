# Extension Resolution and Versioning

This document describes how the Azure Developer CLI (`azd`) resolves extensions from configured sources, selects versions using semantic versioning constraints, checks compatibility with the running `azd` version, and installs artifacts for the current platform. It also provides semantic versioning guidance for extension authors and troubleshooting steps for common issues.

## Extension Sources

### Source Types

Extension sources are manifests that describe the extensions available for installation. Each source has a name, a type, and a location. `azd` supports two source types:

| Type | Location | Description |
|------|----------|-------------|
| `url` | HTTP/HTTPS endpoint | Remote JSON manifest fetched over the network. |
| `file` | Local filesystem path | Local JSON file, useful for development and offline scenarios. |

Sources are configured in `~/.azd/config.json`. You can manage them with the following commands:

```bash
# List configured sources
azd extension source list

# Add a URL-based source
azd extension source add -n my-source -t url -l "https://example.com/extensions.json"

# Add a file-based source
azd extension source add -n local-dev -t file -l "/path/to/registry.json"

# Remove a source
azd extension source remove my-source
```

### Default Source

When no sources are configured, `azd` automatically creates a default source:

| Property | Value |
|----------|-------|
| Name | `azd` |
| Type | `url` |
| Location | `https://aka.ms/azd/extensions/registry` |

If you remove this source, you can re-add it manually:

```bash
azd extension source add -n azd -t url -l "https://aka.ms/azd/extensions/registry"
```

### Source Ordering

Sources are sorted **alphabetically by name** — not by insertion order. This means a source named `"alpha"` is always consulted before `"beta"`, regardless of when each was added.

## Resolution Algorithm

When you run a command like `azd extension install <id>`, `azd` resolves the extension through the following steps:

### 1. Load and Sort Sources

All configured sources are loaded from `~/.azd/config.json` and sorted alphabetically by name. If no sources exist, the default `"azd"` source is created automatically.

### 2. Search Across Sources

`azd` searches every source for extensions matching the requested ID. There is **no failover** behavior — if a source is unreachable (network error, missing file), the operation fails immediately with an error. `azd` does not skip unreachable sources and continue to the next one.

### 3. Handle Conflicts

If the same extension ID exists in **two or more sources**, `azd` returns an error rather than silently picking one:

```
The <id> extension was found in multiple sources.
```

To resolve this, specify the source explicitly:

```bash
azd extension install <id> --source <source-name>
```

There is no priority or merge logic between sources — each extension must be unambiguously resolvable to a single source (unless `--source` is provided).

## Version Constraints

### Constraint Syntax

When installing an extension, you can specify a version constraint using the `--version` flag. `azd` uses the [Masterminds semver](https://github.com/Masterminds/semver) library to parse and evaluate constraints.

| Syntax | Example | Matches |
|--------|---------|---------|
| Exact | `1.0.0` | Only `1.0.0` |
| Caret | `^1.2.3` | `>=1.2.3, <2.0.0` |
| Tilde | `~1.2.3` | `>=1.2.3, <1.3.0` |
| Range | `>=1.0.0,<2.0.0` | Explicit lower and upper bounds |
| Latest | `latest` or omitted | Highest available version |

Examples:

```bash
# Install an exact version
azd extension install my.extension --version 1.0.0

# Install the latest patch in the 1.2.x line
azd extension install my.extension --version "~1.2.0"

# Install any compatible version in the 1.x range
azd extension install my.extension --version "^1.0.0"

# Install with explicit bounds
azd extension install my.extension --version ">=1.0.0,<2.0.0"

# Install the latest version (default)
azd extension install my.extension
```

### Version Selection

When multiple versions satisfy the constraint, `azd` selects the **highest** matching version. For example, if versions `1.0.0`, `1.1.0`, and `1.2.0` are available and the constraint is `^1.0.0`, version `1.2.0` is installed.

## azd Version Compatibility

### `requiredAzdVersion` Field

Each extension version can declare a minimum `azd` version via the `requiredAzdVersion` field in its metadata. This field accepts any semver constraint expression (for example, `">= 1.24.0"`).

When `azd` resolves versions, it filters them into compatible and incompatible sets based on the running `azd` version:

- **Compatible**: the running `azd` version satisfies the `requiredAzdVersion` constraint.
- **Incompatible**: the running `azd` version does not satisfy the constraint.

### Behavior

- If the highest version matching the user's constraint is **compatible**, it is installed normally.
- If the highest version matching the user's constraint is **incompatible**, the install fails with guidance to upgrade `azd`.
- If `requiredAzdVersion` is **empty or cannot be parsed**, the version is treated as compatible (fail-open). This ensures that extensions without the field remain installable.

## Install Flow

Once a version is resolved, installation proceeds through these steps:

1. **Resolve version** — Apply the version constraint against available versions, filter by `azd` compatibility, and select the highest match.
2. **Resolve dependencies** — If the extension declares dependencies, resolve each one recursively from configured sources. Dependencies follow the same conflict and version rules.
3. **Match platform artifact** — Find the artifact for the current OS and architecture. `azd` first looks for `<os>/<arch>` (for example, `linux/amd64` or `windows/amd64`). If no exact match is found, it falls back to `<os>` only (for example, `linux` or `windows`).
4. **Download** — Fetch the artifact from its URL (HTTP/HTTPS) or copy from a local file path.
5. **Validate checksum** — Verify the downloaded file against the published checksum. Supported algorithms are `sha256` and `sha512`.
6. **Extract** — Unpack the artifact based on its file type:
   - `.zip` — extracted as a ZIP archive
   - `.tar.gz` — extracted as a gzipped tar archive
   - Other — treated as a raw binary and copied directly
7. **Set permissions** — On Unix-like systems, set the executable permission on the extension binary.
8. **Update configuration** — Record the installed extension and version in `~/.azd/config.json` under the `extension.installed` section.

## Declaring Extensions in `azure.yaml`

Projects can declare required extensions and version constraints in `azure.yaml`. When `azd init` runs, it reads this configuration and installs each extension automatically.

### Format

```yaml
requiredVersions:
  extensions:
    azure.ai.agents: ">=1.0.0"
    microsoft.azd.demo: "latest"
    my.custom.extension: "^2.0.0"
```

Each entry maps an extension ID to a version constraint string. The same constraint syntax described in [Version Constraints](#version-constraints) applies here.

### Behavior

- When `azd init` runs, it reads the `requiredVersions.extensions` map and installs each extension with the specified constraint.
- If the constraint value is `null` or empty, `"latest"` is used (the highest available version is installed).
- Extensions already installed at a satisfying version are not re-downloaded.

## Caching

### Cache Location

`azd` caches source manifests locally to avoid fetching them on every operation:

```
~/.azd/cache/extensions/<source-name>.json
```

Each source has its own cache file, named after the source.

### Default TTL

The cache has a default time-to-live (TTL) of **4 hours**. After the TTL expires, the next operation that needs the source manifest triggers a fresh HTTP fetch.

### Overriding the TTL

Set the `AZD_EXTENSION_CACHE_TTL` environment variable to override the default TTL. The value uses Go `time.Duration` format:

```bash
# Disable caching entirely (always fetch fresh)
export AZD_EXTENSION_CACHE_TTL=0s

# Set a 30-minute TTL
export AZD_EXTENSION_CACHE_TTL=30m

# Set a 1-hour TTL
export AZD_EXTENSION_CACHE_TTL=1h
```

To clear the cache manually, delete the files in `~/.azd/cache/extensions/`.

## Semantic Versioning Guidance

Extension authors should follow [Semantic Versioning 2.0.0](https://semver.org/) when publishing new versions. Consistent versioning enables consumers to use constraint expressions (caret `^`, tilde `~`, ranges) and trust that updates within a range will not break their workflow.

### Major Version Bump (Breaking Changes)

Increment the **major** version when you make incompatible changes. Examples:

- Remove or rename a CLI command or subcommand
- Remove or rename a CLI flag
- Change an output schema in a breaking way (remove fields, change types)
- Change a required input format incompatibly
- Drop support for an OS or architecture
- Remove a declared capability

### Minor Version Bump (New Features)

Increment the **minor** version when you add functionality in a backward-compatible manner. Examples:

- Add a new CLI command or subcommand
- Add a new CLI flag to an existing command
- Add new fields to an output schema
- Add a new lifecycle event handler
- Add support for a new OS or architecture
- Add a new capability

### Patch Version Bump (Fixes)

Increment the **patch** version for backward-compatible bug fixes. Examples:

- Fix a bug in existing behavior
- Improve performance without changing the API
- Update documentation
- Update dependencies with no user-facing API change

### Pre-release Versions

Use pre-release suffixes for testing before a stable release:

```
2.0.0-alpha.1
2.0.0-beta.1
2.0.0-rc.1
```

Pre-release versions are not selected by default when a user installs with `latest` — they must be requested explicitly with an exact version constraint.

## Troubleshooting

### Common Errors

| Error | Cause | Fix |
|-------|-------|-----|
| *"extension X not found"* | The extension ID is not present in any configured source. | Verify your sources with `azd extension source list`. Check the extension ID spelling. |
| *"found in multiple sources, specify exact source"* | The extension exists in two or more configured sources. | Use `azd extension install X --source <name>` to specify which source to use. |
| *"no matching version found"* | The version constraint excludes all available versions. | Check available versions with `azd extension show X`. Relax the constraint. |
| *"dependency X not found"* | A recursive dependency declared by the extension is missing from all sources. | Ensure the dependency is published to an accessible source. |
| Stale version installed | The source cache has not expired yet, so `azd` is using an older manifest. | Set `AZD_EXTENSION_CACHE_TTL=0s` or delete files in `~/.azd/cache/extensions/`. |

### Diagnostic Steps

1. **Check configured sources:**

   ```bash
   azd extension source list
   ```

2. **Inspect available versions for an extension:**

   ```bash
   azd extension show <extension-id>
   ```

3. **Force a fresh source fetch:**

   ```bash
   export AZD_EXTENSION_CACHE_TTL=0s
   azd extension install <extension-id>
   ```

4. **Install from a specific source:**

   ```bash
   azd extension install <extension-id> --source <source-name>
   ```

## Related Documentation

| Document | Description |
|----------|-------------|
| [Extension Framework](./extension-framework.md) | Architecture overview, source and extension management commands, developing extensions. |
| [Extension SDK Reference](./extension-sdk-reference.md) | Complete API reference for the `azdext` SDK helpers. |
| [Extension End-to-End Walkthrough](./extension-e2e-walkthrough.md) | Build a complete extension from scratch. |
| [Extension Style Guide](./extensions-style-guide.md) | Design guidelines for command integration, flags, and discoverability. |
