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

If the same extension ID exists in **two or more sources**, `azd` handles the conflict differently depending on the mode:

- **Interactive mode** — `azd` prompts the user to choose which source to install from.
- **Non-interactive mode** (`--no-prompt` or CI environments) — `azd` returns an error:

  ```
  The <id> extension was found in multiple sources.
  ```

To avoid the prompt or error, specify the source explicitly:

```bash
azd extension install <id> --source <source-name>
```

There is no priority or merge logic between sources — the `--source` flag is the only way to disambiguate programmatically.

## Version Constraints

### Constraint Syntax

Version constraints differ between the CLI and `azure.yaml`:

#### CLI `--version` flag

The `azd extension install --version` flag accepts only an **exact version string** or **`latest`** (the default when omitted):

```bash
# Install an exact version
azd extension install my.extension --version 1.0.0

# Install the latest version (default)
azd extension install my.extension --version latest
azd extension install my.extension
```

#### `azure.yaml` `requiredVersions.extensions`

The `requiredVersions.extensions` section in `azure.yaml` supports the full semver constraint syntax provided by the [Masterminds semver](https://github.com/Masterminds/semver) library:

| Syntax | Example | Matches |
|--------|---------|---------|
| Exact | `1.0.0` | Only `1.0.0` |
| Caret | `^1.2.3` | `>=1.2.3, <2.0.0` |
| Tilde | `~1.2.3` | `>=1.2.3, <1.3.0` |
| Range | `>=1.0.0,<2.0.0` | Explicit lower and upper bounds |
| Latest | `latest` or omitted | Highest available version |

```yaml
requiredVersions:
  extensions:
    azure.ai.agents: ">=1.0.0"
    microsoft.azd.demo: "latest"
    my.custom.extension: "^2.0.0"
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

- `azd` filters out all versions whose `requiredAzdVersion` constraint is not satisfied by the running `azd` version, then selects the **highest remaining compatible version** that also matches the user's version constraint.
- If a **newer incompatible version** exists beyond the selected version, `azd` shows a **warning** suggesting the user upgrade `azd`.
- If **no compatible versions** remain after filtering, the install **fails** with guidance to upgrade `azd`. The install also fails if the user explicitly requests a specific version that is incompatible.
- If `requiredAzdVersion` is **empty or cannot be parsed**, the version is treated as compatible (fail-open). This ensures that extensions without the field remain installable.

## Install Flow

Once a version is resolved, installation proceeds through these steps:

1. **Resolve version** — Apply the version constraint against available versions, filter by `azd` compatibility, and select the highest match.
2. **Resolve dependencies** — If the extension declares dependencies, resolve each one recursively from the **same source as the parent extension**. Cross-source dependency resolution is not performed. Dependencies use the declared version constraint (or `latest`) but do **not** go through `azd` version compatibility filtering — `requiredAzdVersion` checks are only applied to the top-level extension.
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
- If an extension is already installed (any version), `azd init` **skips it** — it does not check whether the installed version satisfies the configured constraint.
- `azd init` does **not** apply `requiredAzdVersion` compatibility filtering (unlike `azd extension install`).

> **Note:** These are known limitations in the current implementation and may be addressed in future versions:
>
> - `azd init` does not check whether an already-installed extension satisfies the configured version constraint.
> - `azd init` does not apply `requiredAzdVersion` compatibility filtering.
> - Dependency (transitive) installation calls `Install()` directly without passing through `requiredAzdVersion` compatibility filtering, so a dependency may be installed even if its `requiredAzdVersion` is not satisfied by the running `azd` version.

## Caching

### Cache Location

`azd` caches source manifests locally to avoid fetching them on every operation:

```
~/.azd/cache/extensions/<source-name>.json
```

Each source has its own cache file. The filename is derived from the source name by lowercasing it and replacing any characters outside `[a-zA-Z0-9._-]` with `_`. For example, a source named `"My Source!"` would be cached as `my_source_.json`.

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

When `latest` is specified (or the version is omitted), `azd` selects the **highest semantic version**, which can be a pre-release if it sorts higher than the latest stable version. For semver range constraints in `azure.yaml`, pre-release versions are generally excluded unless the constraint itself explicitly includes a pre-release identifier.

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

## Dev/Experimental Extension Registry

The dev (experimental) registry is a separate extension source for bleeding-edge, pre-release, and community-contributed extensions that have not yet been promoted to the official `azd` registry. It lives alongside the main registry in the `azure-dev` repository and is served via a dedicated aka.ms link. While `azd` and `dev` are the official source names, the extension source system supports adding custom sources with any name via `azd extension source add`.

| Property | Main Registry | Dev Registry |
|----------|---------------|--------------|
| URL | `https://aka.ms/azd/extensions/registry` | `https://aka.ms/azd/extensions/registry/dev` |
| Source file | `cli/azd/extensions/registry.json` | `cli/azd/extensions/registry.dev.json` |
| Source name | `azd` (built-in default) | `dev` (official dev registry) |
| Signed binaries | Yes | **No** |
| Support | Covered by Azure support | **Not covered** |

### Experimental vs. Main Registry Criteria

The following criteria determine whether an extension belongs in the dev registry or the main registry:

| Criteria | Main (azd) | Experimental (dev) |
|----------|------------|-------------------|
| **Binary signing** | Signed builds | Unsigned builds |
| **Stability** | Stable releases | Preview, alpha, beta, or pre-release versions |
| **Vetting** | Vetted by the azd team; meets quality bar | Community contributions not yet reviewed; internal experiments |
| **API surface** | Follows [semver guidance](#semantic-versioning-guidance) | May change between versions without notice |
| **Availability** | Maintained with deprecation process | May be removed without notice |

An extension can exist in **both** registries simultaneously. For example, the main registry may contain version `1.2.0` while the dev registry contains `2.0.0-beta.1`. This allows authors to publish stable releases through the main registry while testing upcoming versions through the dev registry.

### Stability Expectations

> [!CAUTION]
> Extensions in the dev registry come with **no stability guarantees**.

When using experimental extensions, expect:

- **Breaking changes** between versions without prior notice
- **Removal** of extensions from the registry without deprecation
- **No Azure support** — experimental extensions are not covered by any Azure support plan
- **Unsigned binaries** — your system may show security warnings when running them
- **Rough edges** — incomplete documentation, missing error messages, and untested edge cases

The dev registry is intended for early adopters, extension authors testing pre-release builds, and internal teams validating extensions before official publication.

### Adding the Dev Registry

The dev registry is **not** configured by default. To opt in:

```bash
# Add the dev registry as a source named "dev"
azd extension source add -n dev -t url -l "https://aka.ms/azd/extensions/registry/dev"
```

Verify it was added:

```bash
azd extension source list
```

You should see both `azd` (the built-in default) and `dev` listed.

To remove the dev registry later:

```bash
azd extension source remove dev
```

### Installing Experimental Extensions

Once the dev source is configured, you can browse and install experimental extensions:

```bash
# List all available extensions (from all configured sources)
azd extension list --available

# Install an extension from the dev registry explicitly
azd extension install my.experimental.extension --source dev

# Install a specific pre-release version
azd extension install my.experimental.extension --version 2.0.0-beta.1 --source dev
```

If an extension exists in both the `azd` and `dev` sources and you do not specify `--source`, `azd` will prompt you to choose (in interactive mode) or return an error (in non-interactive mode). See [Handle Conflicts](#3-handle-conflicts) for details.

### Upgrade and Dev→Main Promotion

When you run `azd extension upgrade`, extensions installed from the dev registry are evaluated for **one-way promotion** to the main registry. Promotion occurs automatically when:

1. **The extension is no longer in the dev registry** — it was removed from `registry.dev.json` after being promoted to `registry.json`.
2. **The main registry has a newer version** — the latest version in the main registry is strictly greater than the latest version in the dev registry.

When promotion happens, the extension's stored source switches from `dev` to `azd`. This is a one-way operation — extensions are never demoted from the main registry back to the dev registry.

> [!NOTE]
> If the main and dev registries have the **same** latest version, the extension stays on its current (dev) source. Equal versions are source-sticky.

The upgrade priority chain is:

1. **Explicit `--source` flag** — always wins if provided
2. **Stored source** — the source the extension was originally installed from
3. **Main registry fallback** — `azd` checks the main registry for promotion opportunities

Promotion events are tracked via `ext.promote` telemetry. Upgrade events (regardless of promotion) are tracked via `ext.upgrade`.

#### Example: Dev→Main Promotion in Action

```bash
# Install from dev registry
azd extension install my.extension --source dev

# Later, the extension graduates to the main registry with a newer version.
# Running upgrade will auto-promote:
azd extension upgrade my.extension
# Output: my.extension upgraded from 1.0.0-beta.2 (dev) → 1.0.0 (azd)
```

### Submitting an Extension to the Dev Registry

To publish an extension to the dev registry, submit a pull request to the [azure-dev](https://github.com/Azure/azure-dev) repository that adds your extension entry to `cli/azd/extensions/registry.dev.json`.

#### Requirements

Your extension entry must:

1. **Pass schema validation** — The entry must conform to the [registry schema](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/registry.schema.json). CI validates this automatically via `ext-registry-ci.yml`.
2. **Include all required metadata:**
   - `id` — Unique identifier (lowercase, alphanumeric, dots, and hyphens: `^[a-z0-9-.]+$`)
   - `namespace` — Classification namespace
   - `displayName` — Human-readable name
   - `description` — Brief description of the extension's purpose
   - `versions` — At least one version entry with `version`, `capabilities`, `usage`, `examples`, and `artifacts`
3. **Include checksums for all artifacts** — Each artifact must declare a `checksum` with an `algorithm` (`sha256` or `sha512`) and `value`.
4. **Provide platform artifacts** — At minimum, include artifacts for `linux/amd64`, `darwin/amd64`, `darwin/arm64`, and `windows/amd64`.

#### Example Entry

```json
{
  "id": "my.experimental.extension",
  "namespace": "my",
  "displayName": "My Experimental Extension",
  "description": "An experimental extension for testing new features.",
  "versions": [
    {
      "version": "0.1.0",
      "capabilities": ["custom-commands"],
      "usage": "azd my-command [options]",
      "examples": [
        {
          "name": "basic-usage",
          "description": "Run my-command with a flag.",
          "usage": "azd my-command --flag value"
        }
      ],
      "artifacts": {
        "linux/amd64": {
          "url": "https://github.com/my-org/my-ext/releases/download/v0.1.0/my-ext-linux-amd64.tar.gz",
          "checksum": {
            "algorithm": "sha256",
            "value": "abc123..."
          }
        },
        "darwin/amd64": {
          "url": "https://github.com/my-org/my-ext/releases/download/v0.1.0/my-ext-darwin-amd64.tar.gz",
          "checksum": {
            "algorithm": "sha256",
            "value": "bcd234..."
          }
        },
        "darwin/arm64": {
          "url": "https://github.com/my-org/my-ext/releases/download/v0.1.0/my-ext-darwin-arm64.tar.gz",
          "checksum": {
            "algorithm": "sha256",
            "value": "def456..."
          }
        },
        "windows/amd64": {
          "url": "https://github.com/my-org/my-ext/releases/download/v0.1.0/my-ext-windows-amd64.zip",
          "checksum": {
            "algorithm": "sha256",
            "value": "789ghi..."
          }
        }
      }
    }
  ]
}
```

#### Review Process

- A maintainer will review your PR for schema compliance, metadata completeness, and artifact accessibility.
- There is no formal quality gate for the dev registry — it is intentionally lower-friction than the main registry.
- Extensions that mature and meet the [main registry criteria](#experimental-vs-main-registry-criteria) can be promoted via a separate PR to `registry.json`.

### Troubleshooting Multi-Registry Scenarios

#### Extension exists in both registries

When the same extension ID is present in both `azd` and `dev`:

- **Interactive mode** — `azd` prompts you to choose which source to install from.
- **Non-interactive mode** — `azd` fails with `"found in multiple sources"`.
- **Resolution** — Use `--source` to specify explicitly:

  ```bash
  azd extension install my.extension --source dev
  azd extension install my.extension --source azd
  ```

#### Source ordering affects resolution

Sources are sorted **alphabetically by name**. With the default naming (`azd` and `dev`), `azd` is consulted first because `"azd"` sorts before `"dev"`. If you name your dev source `"aaa-dev"`, it would be consulted first. The name only affects the order in which sources are searched — it does not affect upgrade or promotion behavior.

#### Stale cache after registry updates

If a recently published extension does not appear, the local cache may not have expired yet:

```bash
# Force a fresh fetch by setting TTL to zero
export AZD_EXTENSION_CACHE_TTL=0s       # Linux/macOS
$env:AZD_EXTENSION_CACHE_TTL = "0s"     # PowerShell

# Then retry
azd extension list --available
```

Or clear the cache manually:

```bash
# Linux/macOS
rm -rf ~/.azd/cache/extensions/

# PowerShell
Remove-Item -Recurse -Force "$env:USERPROFILE\.azd\cache\extensions\"
```

#### Unreachable dev source blocks all operations

If the dev registry URL is unreachable (network issue, DNS failure), operations that load sources will **fail** rather than skip the unreachable source. To unblock yourself, remove the dev source temporarily:

```bash
azd extension source remove dev
```

## Related Documentation

| Document | Description |
|----------|-------------|
| [Extension Framework](./extension-framework.md) | Architecture overview, source and extension management commands, developing extensions. |
| [Extension SDK Reference](./extension-sdk-reference.md) | Complete API reference for the `azdext` SDK helpers. |
| [Extension End-to-End Walkthrough](./extension-e2e-walkthrough.md) | Build a complete extension from scratch. |
| [Extension Style Guide](./extensions-style-guide.md) | Design guidelines for command integration, flags, and discoverability. |
