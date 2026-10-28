# Creating an Extension

This guide covers how to create, build, and publish an extension for the Azure Developer CLI.

## Overview

azd extensions use a gRPC-based framework to add functionality. The extension framework is [generally available (GA)](https://devblogs.microsoft.com/azure-sdk/azd-extension-framework-ga/); individual extensions you create may still be experimental or pre-release until you publish a stable version. Extensions can provide:

- **Custom commands** — Expose new command groups and commands to azd
- **Lifecycle events** — Subscribe to project and service lifecycle events (pre/post provision, deploy, etc.)
- **MCP server** — Provide Model Context Protocol tools for AI agents
- **Framework service providers** — Add build/restore support for new languages
- **Service target providers** — Add deployment support for new Azure hosting targets
- **Provisioning providers** — Add custom infrastructure provisioning support
- **Metadata** — Provide metadata about commands and capabilities

## Prerequisites

- Go 1.26 or later
- A fork of the [azure-dev](https://github.com/Azure/azure-dev) repository
- The `azd` Developer Extension installed (`azd extension install microsoft.azd.extensions`)
- One of the supported languages: Go (best support), .NET (C#), Python, or JavaScript

## Getting Started

### 1. Create the extension

First-party extensions live under `cli/azd/extensions/`. Create a new directory for your extension:

```bash
mkdir -p cli/azd/extensions/my.extension
cd cli/azd/extensions/my.extension
```

### 2. Define extension.yaml

Create an `extension.yaml` manifest that declares the extension's metadata and capabilities:

```yaml
id: my.extension
displayName: My Extension
description: A brief description of what this extension does
version: 0.1.0
capabilities:
  - lifecycle-events
```

### 3. Implement the extension

Implement the required interfaces for your declared capabilities. See the extension framework services documentation for interface details.

If your Go extension creates role assignments, use the stable [`Account().GetCurrentPrincipal`](../../cli/azd/docs/extensions/extension-framework.md#getcurrentprincipal) method with the target subscription ID and request types from `azdext` or `contracts/v1`. The host resolves the resource-tenant object ID and principal type without returning an access token. Pin the published azd SDK to v1.36.0 or later and set `requiredAzdVersion: ">=1.36.0"` before replacing an existing lookup. The equivalent beta API is deprecated.

Go extensions that resolve local Foundry configuration `$ref` files should use [`foundry.ResolveFileRefs`](../../cli/azd/pkg/foundry/includes.go). Each referenced file must contain exactly one YAML or JSON object; additional documents, trailing content, arrays, and scalars are rejected. Resolution retains YAML value types, aliases, and sibling overlays. Pass only the selected configuration when unrelated references should remain unopened. Extensions must consume an SDK release containing this validation before their binaries enforce it.

For Foundry developer access, reuse
[`foundry.AccessPreparation`](../../cli/azd/pkg/foundry/access.go) rather than
duplicating RBAC policy. Validate the selected endpoint against its ARM project
ID with `foundry.ProjectAccessScope` (or `ProjectScopeFromEnvironment` when
using an active environment). Reuse `LoadProjectEnvironment`,
`RoleAssignmentsSkipped`, `LookupProjectTenant`, and `NewCredential` for
common setup, then supply the credential, a non-mutating
data-plane `Probe`, an injected `Warn` callback, and optionally `Progress` for
grant attempts, propagation waits, and confirmed readiness. Keep both callbacks
on the command's injected writer. Call `Ensure` only before
operations intended to prepare access, never from read/delete or diagnostic
paths. It skips grants when effective access already works and waits after a
new grant. Consume a core SDK release containing these APIs before publishing
the extension; use a local Go workspace for cross-module development.

The default scaffolds and SDK `EventManager` use the stable `v1` contract.
Go extensions can opt into beta structured messages for service
`predeploy`/`postdeploy` handlers with
`ExtensionHost.WithBetaServiceEventHandler`. See
[contract versioning](../../cli/azd/docs/extensions/contract-versioning.md)
for the stream requirements and legacy-client behavior.

### 4. Build

```bash
# Using the developer extension
azd x build

# Or using Go directly
go build
```

### 5. Register in the Main Extension Registry

When your extension is stable and ready for production use, add an entry to `cli/azd/extensions/registry.json` with version, capabilities, and download URLs.

`ext-registry-ci` validates `requiredAzdVersion` for added or modified versions in both registries against [the latest published azd version](https://aka.ms/azure-dev/versions/cli/latest). This endpoint is updated by the core release pipeline's final `PublishVersionTxt` stage. Extension binaries may be published earlier, but registry updates must wait for core publishing to finish. Rerun the workflow once publishing completes.

### 6. Publish to the Dev Registry First (Recommended)

For extensions that are still in development or preview, consider publishing to the **dev (experimental) registry** first to gather feedback before graduating to the main registry:

1. Add your extension entry to `cli/azd/extensions/registry.dev.json` in a PR to the [azure-dev](https://github.com/Azure/azure-dev) repository.
2. Your entry must pass schema validation (CI checks this automatically) and include all required metadata — `id`, `namespace`, `displayName`, `versions` with artifacts and SHA256/SHA512 checksums.
3. Users can then install your extension by adding the dev source and installing from it:

   ```bash
   azd extension source add -n dev -t url -l "https://aka.ms/azd/extensions/registry/dev"
   azd extension install my.extension --source dev
   ```

4. Once your extension is stable and meets the quality bar, submit a follow-up PR to add it to `cli/azd/extensions/registry.json`. Users who installed from the dev registry will be **automatically promoted** to the main registry on their next `azd extension update`.

> [!NOTE]
> Extensions in the dev registry have no stability guarantees, are unsigned, and are not covered by Azure support. This is expected and appropriate for pre-release testing. See the [Dev/Experimental Extension Registry](../../cli/azd/docs/extensions/extension-resolution-and-versioning.md#devexperimental-extension-registry) guide for full details.

## First-party CI builds

Go extensions in this repository keep a thin `ci-build.ps1` wrapper in each extension directory. The wrapper calls [`cli/azd/extensions/scripts/ci-build.ps1`](../../cli/azd/extensions/scripts/ci-build.ps1), which owns the common build flags, target policy, coverage, record builds, and error handling. The first-party generation template creates the wrapper. Keep the shared script in the checkout when building an extension.

Run the script from the extension directory with an explicit output filename:

```powershell
./ci-build.ps1 -OutputFileName bin/my-extension-linux-amd64 -Version 0.1.0 -SourceVersion abc123
```

The default version comes from the extension's own `version.txt`. Wrappers pass the actual linker packages through `-VersionPackages`, use `-VersionOnly` when commit and build-date fields do not exist, and retain any required version-output format or native argument mode. Document non-obvious exceptions beside the affected code and test them.

The scripts resolve the effective target through `go env GOOS GOARCH`. Linux amd64 builds disable cgo to avoid inheriting the build agent's GLIBC requirement, as addressed by [Azure/azure-dev#10393](https://github.com/Azure/azure-dev/pull/10393). ARM64 and non-Linux builds preserve the caller's cgo settings, including the macOS ARM64 pipeline's `CGO_ENABLED=1`. The scripts restore cgo environment state after building and leave `GOEXPERIMENT` unchanged.

`-CodeCoverageEnabled` adds Go coverage instrumentation. `-BuildRecordMode` builds both the production binary and a second binary with the `record` tag. Record filenames add `-record` on Linux/macOS or insert `-record` before `.exe` on Windows. Coverage applies to both binaries when both switches are supplied. Release jobs build a separate binary without either switch.

From the repository root, run the build-contract tests:

```powershell
Invoke-Pester -Path ./eng/scripts/Test-ExtensionBuildScripts.Tests.ps1 -CI
```

The tests check each wrapper's native argument boundaries, metadata, switch forwarding, and failure propagation. The full build-policy matrix runs once against the shared script. Changes to the shared script trigger all extension release pipelines; include its path in both `trigger` and `pr` filters when adding a pipeline. Real native and cross-build jobs still need to validate dependency compatibility and artifact startup. These repository CI scripts are not required for extensions developed outside `azure-dev`.

## Command-level lifecycle follow-up

Beta project `post*` handlers can contribute next-step guidance to the
parent command's human-readable completion message. Use the preview
`EventsBeta()` client to subscribe and receive an invocation ID, then call
`CommandResult().SetFollowUp` while processing that invocation.

For published extensions using this preview API, set `requiredAzdVersion`
to `>=1.35.0` for the current release line. See the
[SDK reference](../../cli/azd/docs/extensions/extension-sdk-reference.md#project-lifecycle-follow-up)
for host compatibility, subscription errors, and how contributions from
multiple handlers and workflow steps are resolved.

## Extension Design Guidelines

- **Extend existing command categories** — Use verb-first structure (e.g., `azd add <resource>`)
- **Reuse parameter patterns** — Use established flags like `--subscription`, `--name`, `--type`
- **Integrate with help** — Make your extension discoverable through `azd help`
- **Error handling** — Use `ServiceError` for Azure API errors and `LocalError` for client-side errors
- **Telemetry** — Follow the [extension telemetry guide](../../cli/azd/docs/extensions/extension-telemetry.md);
  first-party `ReportUsage` attributes must be declared and classified in
  `cli/azd/extensions/telemetry/fields.go`
- **Deployment preview** — To support `azd deploy --preview`, register the host with
  `WithServiceTargetPreview` and implement `azdext.ServiceTargetPreviewProvider`
  using stable `azdext.ServiceConfig` and `azdext.ServiceDeployPreviewResult`.
  This [stable v1 contract](../../cli/azd/docs/extensions/extension-framework.md#deployment-preview)
  targets host and SDK 1.37.0. Once published, pin SDK v1.37.0 or later and set
  `requiredAzdVersion: ">=1.37.0"`. Deprecated beta APIs remain supported.
  A preview runs on a fresh provider without `Initialize` and must not build,
  deploy, or persist deployment state.

## Detailed Reference

Commands that do not need the selected azd environment can emit
`"launch": {"noAzdEnvironment": true}` on their command metadata. These launch
settings are per-command, not inherited; they skip loading the selected azd
environment, resolving its Key Vault references, and injecting its variables.
Inherited parent-process environment variables and azd launch/protocol variables
are still passed, and explicit Environment gRPC calls are unaffected. Omitting
`launch` or setting `noAzdEnvironment` to `false` preserves the default behavior. Require
azd 1.37.0 or later when relying on this behavior. See
[command launch settings](../../cli/azd/docs/extensions/extension-sdk-reference.md#command-launch-settings).

Use `Project().AddService` for complete service creation or replacement.
For updates, prefer service-specific setters for the fields your extension
owns, leaving unknown fields host-side. The host preserves unrelated raw YAML,
including empty mappings, literal templates, and large integers. Explicitly
send an empty section when `env: {}` is required. A sequence of RPCs is not
transactional. See
[service configuration persistence](../architecture/extension-framework.md#service-configuration-persistence).

Treat failed mutating RPCs as uncertain unless the host confirms completion.
For `Project.AddService`, use the optional
[save-failure acknowledgment](../architecture/extension-framework.md#project-service-save-acknowledgment),
the read-only beta `GetAddServiceCapabilities` RPC, a fresh typed operation ID,
and local ownership checks before compensating local edits. Only an explicit
unsupported response or `Unimplemented` from the capability RPC selects the stable
host path. Never retry a failed mutating RPC on another channel. Unacknowledged failures
require retention and explicit inspection guidance rather than status-code-based
rollback; do not trust custom metadata/trailers as a substitute for the typed API.
The acknowledgment covers completed pre-save rejections only on host builds
that implement that behavior. It proves that the operation cannot write later;
it does not replace a root-file comparison or ownership checks.

For comprehensive extension development documentation, see:

- [Extension Framework](../../cli/azd/docs/extensions/extension-framework.md) — Full framework guide
- [Extension Framework Services](../../cli/azd/docs/extensions/extension-framework-services.md) — Adding language/framework support
- [Extensions Style Guide](../../cli/azd/docs/extensions/extensions-style-guide.md) — Design guidelines and best practices
