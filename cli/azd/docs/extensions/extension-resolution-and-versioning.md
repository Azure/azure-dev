# Extension Resolution and Versioning

This document describes how the Azure Developer CLI (`azd`) resolves extensions from configured sources, selects versions using semantic versioning constraints, checks compatibility with the running `azd` version, and installs artifacts for the current platform. It also provides semantic versioning guidance for extension authors and troubleshooting steps for common issues.

## Extension Sources

### Source Types

Extension sources are manifests that describe the extensions available for installation. Each source has a name, a type, and a location. `azd` supports two configurable source types:

| Type | Location | Description |
|------|----------|-------------|
| `url` | HTTP/HTTPS endpoint | Remote JSON manifest fetched over the network. |
| `file` | Local filesystem path | Local JSON file, useful for development and offline scenarios. |

In addition, extensions installed from a [self-contained bundle](#self-contained-bundles) are tagged with a reserved `bundle` source. `bundle` is not a configurable source type and never appears in `azd extension source list` — it simply marks an extension that has no live registry to track updates against. Such extensions are listed with their `bundle` source in `azd extension list` and are skipped by `azd extension update`. The name `bundle` is reserved, so it cannot be used as a user-configured source name.

Source names must contain 1 to 64 lowercase ASCII letters, digits, hyphens, or underscores, and must begin and end with a letter or digit. Invalid names are rejected rather than normalized. If an older configuration contains an invalid name, extension source loading reports the exact entry to remove and re-add.

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

### Source Categories

For telemetry, `azd` classifies sources by type and location rather than by the user-defined name:

| Category | Source |
|----------|--------|
| `azd` | Main aka.ms registry or its resolved GitHub URL |
| `dev` | Dev aka.ms registry or its resolved GitHub URL |
| `nightly` | Nightly aka.ms registry or its resolved GitHub URL |
| `local` | File source |
| `bundle` | Self-contained extension bundle |
| `other` | Other URL or custom source type |
| `unknown` | Legacy installed record without a persisted category |

The category is stored with an installed extension so it remains available if the configured source is later removed or changed. Source names, URLs, paths, and hosts are not emitted.

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
- Install resolution owns this policy centrally. `azd init`, project auto-install, command auto-install, explicit install, update, provider lookup, and recursive dependencies all use the same compatibility-aware resolver. The selected release and any newer incompatible release are recorded on that result so install and update can warn without applying a second compatibility filter.
- During `azd extension install` and `azd extension update`, if a **newer incompatible version** exists beyond the selected version, `azd` shows a **warning** suggesting the user update `azd`. An explicit `--version` pin does not produce this warning.
- If **no compatible versions** remain after filtering, the install **fails** with guidance to update `azd`. The install also fails if the user explicitly requests a specific version that is incompatible.
- Catalogue commands still retain the full published metadata so they can display incompatible releases and explain their requirements.
- If `requiredAzdVersion` is **empty or cannot be parsed**, the version is treated as compatible (fail-open). This ensures that extensions without the field remain installable.

## Install Flow

Once a version is resolved, installation proceeds through these steps:

1. **Resolve version** — Apply the version constraint against available versions, filter by `azd` compatibility, and select the highest match.
2. **Resolve dependencies** — If the extension declares dependencies, resolve each one recursively from the **same source as the parent extension**, then fall back to the main `azd` registry when that source has no compatible version satisfying the dependency constraint. Other configured sources are not searched. Self-contained bundles do not use the fallback because all of their dependencies must be included in the bundle. Dependencies use the declared version constraint (or `latest`) and are filtered by their `requiredAzdVersion` compatibility with the running `azd` version. Passing `--no-dependencies` skips this step entirely: only the named extension is installed, its declared dependencies are neither resolved nor installed, and the installed-dependency version constraints are not enforced. This is intended for callers that only need the extension's own binary (for example, generating command snapshots) and cannot guarantee the registry's dependency graph is internally consistent.
3. **Match platform artifact** — Find the artifact for the current OS and architecture. `azd` first looks for `<os>/<arch>` (for example, `linux/amd64` or `windows/amd64`). If no exact match is found, it falls back to `<os>` only (for example, `linux` or `windows`).
4. **Download** — Fetch the artifact from its URL (HTTP/HTTPS) or copy from a local file path.
5. **Validate checksum** — Verify the downloaded file against the published checksum. Supported algorithms are `sha256` and `sha512`.
6. **Extract** — Unpack the artifact based on its file type:
   - `.zip` — extracted as a ZIP archive
   - `.tar.gz` — extracted as a gzipped tar archive
   - Other — treated as a raw binary and copied directly
7. **Set permissions** — On Unix-like systems, set the executable permission on the extension binary.
8. **Update configuration** — Record the installed extension and version in `~/.azd/config.json` under the `extension.installed` section.

### Re-installing over an existing extension

`azd extension install <id>` keys off the extension **id**, so installing an id that is already present is handled based on whether the **source** is changing and on the version relationship. `--force` bypasses all of these guards.

When the source is **not** changing (same source as the installed extension):

- **Same version** — a no-op; the install is skipped.
- **Newer version** — updated in place.
- **Older version** — a downgrade; `azd` **prompts for confirmation** before replacing the newer install with an older one. Declining skips the install. In `--no-prompt` mode `azd` skips with guidance to pass `--force`, and `--force` proceeds without prompting.

When the source **is** changing (for example installing a bundle build over a registry build, or vice versa), the artifacts may differ, so `azd` does not silently proceed, no-op, or block a downgrade. Instead it **prompts for confirmation** before replacing the installed extension. The prompt states the version transition explicitly — *Reinstall*, *Update to `<version>`*, or *Downgrade to `<version>`* — and the target source. Declining skips the install; confirming reinstalls and re-points the extension to the new source. In `--no-prompt` mode `azd` skips with guidance to pass `--force`, and `--force` proceeds without prompting.

Because each bundle install registers a unique transient source, installing from **any** bundle over an already-installed extension is always treated as a source change — so it prompts even when the bundled version matches the installed one (the two builds may not be byte-identical).

For registry-backed installs, a required dependency must resolve from the parent's source or the main `azd` registry. For self-contained bundles, it must resolve from the bundle itself. If the dependency is not already installed and cannot be resolved from the applicable sources, the install fails with actionable guidance.

## Self-Contained Bundles

A **self-contained bundle** is a single portable `.zip` that contains a well-known `registry.json` plus the extension artifacts it references. It lets you share a one-off build (for example, a PR build or an internal extension) without hosting a registry — the recipient runs a single command to install everything from the file, or from a single link when the `.zip` is hosted somewhere reachable.

### Producing a bundle

Extension authors create a bundle with the `azd x` developer extension:

```bash
azd x pack --bundle
```

This builds the platform artifacts and emits a single `<id>_<version>.zip` whose root contains a `registry.json` and an `artifacts/` directory. The registry's artifact URLs are **relative** (for example, `artifacts/my-ext-linux-amd64.tar.gz`), and each artifact carries an embedded `sha256` checksum. Extension packs (which have no binaries of their own) are supported as registry-only bundles.

### Installing a bundle

Consumers install a bundle by passing its path to `azd extension install`:

```bash
azd extension install ./my-ext_1.0.0.zip
```

A bundle can also be installed directly from an `https` URL, so a preview or internal build can be shared as a single link:

```bash
azd extension install https://example.com/builds/my-ext_1.0.0.zip
```

The install flow treats the bundle as an **installer, not a registry** — nothing about the bundle persists as a configured source once installation finishes:

1. **Download** (URLs only) the bundle to a temporary file. Download failures — an unreachable host or a non-`200` response — are reported as such, separately from a `.zip` that turns out not to be a valid bundle. From here on, remote and local bundles follow the exact same path.
2. **Extract** the bundle into a temporary directory.
3. **Register an ephemeral source** that reads the extracted `registry.json` and rewrites each relative artifact URL to an absolute path anchored inside the extracted directory. This is what allows the standard install flow — including checksum validation — to resolve the bundled artifacts unchanged. Relative paths that escape the bundle directory are rejected. The source name is transient and is never surfaced to the user.
4. **Install** the bundled extension through the normal install path. Bundles are produced per extension by `azd x pack --bundle`, so a bundle declares a single extension.
5. **Clean up** — once the extension is installed, `azd` re-points it to the reserved `bundle` source, removes the ephemeral source, and deletes the temporary extraction directory along with any downloaded `.zip`. Temporary files are removed whether the install succeeds or fails. The only durable state left behind is the installed extension itself (its binary under `~/.azd/extensions/<id>/` and its `extension.installed` record).

### Lifecycle of a bundle-installed extension

Because a bundle does not register a lasting source, a bundle-installed extension is tracked under the reserved `bundle` source:

- `azd extension list` shows it with its `bundle` source and a normal `✓ Up to date` status. It has no "latest" version to compare against, so no update is ever reported.
- `azd extension update` skips bundle-installed extensions with a note that they were installed from a self-contained bundle.
- `azd extension source list` does **not** show an entry for the bundle — there is no leftover source to clean up.

To update a bundle-installed extension, install a newer bundle:

```bash
azd extension install ./my-ext_2.0.0.zip
```

To switch a bundle-installed extension back to a registry-tracked one, install it explicitly from a configured source:

```bash
azd extension install <extension-id> --source <source-name>
```

### Trust model

Bundles run arbitrary extension binaries on your machine. The embedded `sha256` checksums verify that each artifact matches the checksum declared in the `registry.json` received in the same bundle. They can detect accidental corruption or inconsistencies within that bundle, but they do not authenticate the publisher or protect against an attacker replacing both the artifacts and their checksums. Bundles are not signed. Only install bundles obtained from a source you trust. Remote bundle installation requires `https` because HTTP downloads can be modified in transit.

## Declaring Extensions in `azure.yaml`

Projects can declare required extensions and version constraints in `azure.yaml`. `azd init` reads this configuration and installs each extension automatically, and the project commands listed below re-check it before they run.

### Format

```yaml
requiredVersions:
  extensions:
    azure.ai.agents: ">=1.0.0"
    microsoft.azd.demo: "latest"
    my.custom.extension: "^2.0.0"
```

Each entry maps an extension ID to a version constraint string. The same constraint syntax described in [Version Constraints](#version-constraints) applies here.

### Behavior during `azd init`

- When `azd init` runs, it reads the `requiredVersions.extensions` map and installs each extension with the specified constraint.
- If the constraint value is `null` or empty, `"latest"` is used (the highest available version is installed).
- If an extension is already installed (any version), `azd init` **skips it** — it does not check whether the installed version satisfies the configured constraint.
- Version selection, source eligibility, and recursive dependency installation all apply `requiredAzdVersion` compatibility filtering.

> **Note:** The following is a known limitation in the current implementation and may be addressed in a future version:
>
> - `azd init` does not check whether an already-installed extension satisfies the configured version constraint.

### Behavior during project commands

Cloning a repository or editing `azure.yaml` by hand skips `azd init`, so the project commands that resolve a provider check for extensions again before running: `up`, `provision`, `deploy`, `package`, `restore`, `down` and `env refresh`.

Resolution during project commands differs from `azd init` in two ways:

- It **does** check installed extensions against the configured constraint, and fails with the conflicting constraint rather than proceeding with an unsatisfying version.
- It resolves not only `requiredVersions.extensions` but also the providers the project implies (see below).

Resolution only prompts for extensions that are genuinely missing, and it is skipped when the command renders help instead of running, such as `azd up --help`.

Before installing anything, `azd` displays the complete set of missing extensions and their eligible configured sources. A source is eligible only when it publishes a version that satisfies the project constraint, supports the running azd version, and provides the capability and provider the command needs. A single missing extension is shown with its ID, source or sources, and description. Multiple missing extensions are summarized in a table and confirmed together.

When the official azd registry is one of several sources, `azd` recommends it but allows another configured source to be selected. For multiple extensions, a source selected for the first extension can be reused for the remaining extensions only when that source publishes every remaining requirement. Otherwise, azd prompts for each ambiguous source separately. Declining installation prints `Canceled: required extension isn't installed.`, stops the requested command before it runs, and exits successfully.

With an explicit local `--no-prompt`, azd installs automatically only when every missing extension has one eligible source. The sources do not need to be the same. If any extension is available from several sources, azd stops and prints the exact `azd extension install` commands that can resolve the ambiguity.

Auto-install remains disabled in detected CI/CD environments, including when CI detection enables no-prompt mode automatically. The error lists manual install commands for every missing extension so the pipeline can install them explicitly before rerunning the project command.

### Inferred extension requirements

Beyond the explicit `requiredVersions.extensions` list, project commands infer requirements from the providers the project uses:

- Each `services.<name>.host` value must be supplied by an extension declaring the `service-target-provider` capability for that host.
- Each `infra.provider` value (including entries under `infra.layers`) must be supplied by an extension declaring the `provisioning-provider` capability for that provider.

Providers that `azd` implements itself are never resolved through an extension and never contact a registry: the built-in hosts (`appservice`, `containerapp`, `function`, `staticwebapp`, `aks`, `ai.endpoint`) and the built-in provisioning providers (`bicep`, `terraform`).

When several extensions publish the same provider, `azd` prompts for the one to install. When an extension is already selected by `requiredVersions.extensions`, or is pulled in as a dependency of one, that version is used instead of installing another extension. If a version selected that way cannot supply the provider, `azd` reports the conflicting constraint rather than installing a second extension that the first would override.

An extension only qualifies when the version `azd` would select publishes the provider. Selection first removes releases that do not support the running azd version, then chooses the highest remaining version. An older compatible release can therefore supply a provider when newer releases require a newer azd. azd does not select an older release merely because the newest compatible release dropped the provider. A provider that an installed extension already supplies is not resolved again.

## Caching

### Cache Location

`azd` caches source manifests locally to avoid fetching them on every operation:

```
~/.azd/cache/extensions/<source-name>.json
```

Each source has its own cache file. Because configured source names use the canonical format described above, the source name maps directly to a unique cache filename.

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
| *"version X was not found; latest compatible version is Y"* | The extension exists, but the requested version is not published by the eligible source. | Run the suggested install command to use the latest compatible version, or inspect available versions with `azd extension show X`. |
| *"not compatible with azd X"* | Matching extension metadata exists, but the selected release requires another azd version. | Use an azd version that satisfies the reported constraint, then retry. |
| *"dependency X not found"* | A recursive dependency is not installed and is missing from the applicable sources: the parent source and main `azd` registry for registry-backed installs, or the bundle for a bundle install. | Include the dependency in the parent source or bundle, publish it to `azd` for a registry-backed install, or install it explicitly before installing the parent. |
| *"no version satisfies constraint"* | The applicable sources contain the dependency, but none of its versions match the parent extension's constraint. | Include or publish a compatible dependency version, install one explicitly, or update the parent extension's constraint. |
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

The dev (experimental) registry is a separate extension source for bleeding-edge, pre-release, and community-contributed extensions that have not yet been promoted to the official `azd` registry. It lives alongside the main registry in the `azure-dev` repository and is served via a dedicated aka.ms link. While `azd` and `dev` are the official source names, custom source names must follow the canonical source-name format.

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

### Update and Dev→Main Promotion

When you run `azd extension update`, extensions installed from the dev registry are evaluated for **one-way promotion** to the main registry. Promotion occurs automatically when:

1. **The extension is no longer in the dev registry** — it was removed from `registry.dev.json` after being promoted to `registry.json`.
2. **The main registry has a newer version** — the latest version in the main registry is strictly greater than the latest version in the dev registry.

When promotion happens, the extension's stored source switches from `dev` to `azd`. This is a one-way operation — extensions are never demoted from the main registry back to the dev registry.

> [!NOTE]
> If the main and dev registries have the **same** latest version, the extension stays on its current (dev) source. Equal versions are source-sticky.

The update priority chain is:

1. **Explicit `--source` flag** — always wins if provided
2. **Stored source** — the source the extension was originally installed from
3. **Main registry fallback** — `azd` checks the main registry for promotion opportunities

Promotion events are tracked via `ext.promote` telemetry. Update events (regardless of promotion) are tracked via `ext.update`.

#### Example: Dev→Main Promotion in Action

```bash
# Install from dev registry
azd extension install my.extension --source dev

# Later, the extension graduates to the main registry with a newer version.
# Running update will auto-promote:
azd extension update my.extension
# Output: my.extension updated from 1.0.0-beta.2 (dev) → 1.0.0 (azd)
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

Sources are sorted **alphabetically by name**. With the default naming (`azd` and `dev`), `azd` is consulted first because `"azd"` sorts before `"dev"`. If you name your dev source `"aaa-dev"`, it would be consulted first. The name only affects the order in which sources are searched — it does not affect update or promotion behavior.

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

## Nightly Extension Registry

The nightly registry contains **automatically built, always-latest** development snapshots of first-party extensions. Each scheduled pipeline run rebuilds an extension from `main`, signs the Windows and macOS binaries, uploads them to an always-latest storage folder, and updates a single entry in the nightly registry. Installing a nightly always gives you the most recent nightly build available at that time.

| Property | Main Registry | Nightly Registry |
|----------|---------------|------------------|
| URL | `https://aka.ms/azd/extensions/registry` | `https://raw.githubusercontent.com/Azure/azure-dev/nightly/cli/azd/extensions/registry.nightly.json` |
| Source file | `cli/azd/extensions/registry.json` (on `main`) | `cli/azd/extensions/registry.nightly.json` (on the `nightly` branch) |
| Source name | `azd` (built-in default) | `nightly` (opt-in) |
| Version shape | `1.2.3` | `1.2.3-nightly.<buildId>` (or `1.2.3-preview.nightly.<buildId>`) |
| Signed binaries | Yes | Windows/macOS signed; Linux unsigned |
| History retained | Yes | No — only the latest nightly per extension |
| Support | Covered by Azure support | **Not covered** |

> [!CAUTION]
> Nightly extensions are built from `main` and come with **no stability guarantees**. Only the current nightly version is retained - older nightly versions are not installable.

### Adding the Nightly Registry

The nightly registry must be added, manually. To opt in:

```bash
# Add the nightly registry as a source named "nightly"
azd extension source add -n nightly -t url -l "https://raw.githubusercontent.com/Azure/azure-dev/nightly/cli/azd/extensions/registry.nightly.json"
```

Then, to install a nightly-built extension:

```bash
azd extension install <extension-id> --source nightly
```

To remove the nightly registry later:

```bash
azd extension source remove nightly
```

### Update and Nightly→Main Promotion

Nightly versions use semver prerelease labels, so the standard `azd extension update` flow works:

- A newer nightly (higher build id, or a higher base version) supersedes an older one, so `azd extension update` pulls the latest nightly.
- When the extension ships a **stable** release whose base version matches your nightly (for example stable `1.2.3` versus `1.2.3-nightly.200`), the stable release outranks the nightly and you are **automatically promoted** to the `azd` registry on your next update.

> [!NOTE]
> If your nightly was built from a **prerelease** base (for example `1.2.3-preview.nightly.60`), it sorts **above** the matching stable prerelease `1.2.3-preview`. In that case you are not promoted until the stable registry advances to a higher base version. This is expected semver precedence behavior.

## Related Documentation

| Document | Description |
|----------|-------------|
| [Extension Framework](./extension-framework.md) | Architecture overview, source and extension management commands, developing extensions. |
| [Extension SDK Reference](./extension-sdk-reference.md) | Complete API reference for the `azdext` SDK helpers. |
| [Extension End-to-End Walkthrough](./extension-e2e-walkthrough.md) | Build a complete extension from scratch. |
| [Extension Style Guide](./extensions-style-guide.md) | Design guidelines for command integration, flags, and discoverability. |
