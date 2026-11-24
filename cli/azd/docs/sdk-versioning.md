# Go Module Versioning

The `github.com/azure/azure-dev/cli/azd` Go module is tagged with semantic version tags that mirror the CLI release version.

| CLI Release Tag | Go Module Tag | Go Module Reference |
|-----------------|---------------|---------------------|
| `azure-dev-cli_1.23.13` | `cli/azd/v1.23.13` | `github.com/azure/azure-dev/cli/azd v1.23.13` |
| `azure-dev-cli_1.24.0-beta.1` | `cli/azd/v1.24.0-beta.1` | `github.com/azure/azure-dev/cli/azd v1.24.0-beta.1` |

## For Extension Developers

Use standard semver references in your `go.mod` instead of pseudo-versions:

```go.mod
// Before (pseudo-version):
require github.com/azure/azure-dev/cli/azd v0.0.0-20260305185830-a633d43bb543

// After (semver):
require github.com/azure/azure-dev/cli/azd v1.23.13
```

### Upgrading

```bash
go get github.com/azure/azure-dev/cli/azd@v1.24.0
go mod tidy
```

### Environment Variable Constants

Use `github.com/azure/azure-dev/cli/azd/pkg/environment/constants` when you only
need environment variable names, such as `constants.TenantIdEnvVarName`. This
package has no imports and does not bring in environment management or Azure
Storage dependencies. The existing constants in `pkg/environment` remain
available as aliases for source compatibility.

For local development before an SDK release includes this package, use the
local `replace` directive described below. Before publishing an extension, pin
an SDK release containing the package and remove the local override.

### Local Development

For development within the monorepo, add a `replace` directive to use the local copy:

```go.mod
replace github.com/azure/azure-dev/cli/azd => ../..
```

This takes precedence during local builds. The `require` version still specifies the minimum published version.

## Compatibility

- **Patch versions** (1.23.x → 1.23.y): Backward compatible bug fixes
- **Minor versions** (1.x.0 → 1.y.0): New features, backward compatible
- **Major versions**: Would require a module path change (e.g., `/v2` suffix) per [Go module versioning](https://go.dev/ref/mod#versions)

The environment-state validation update is an intentional source-incompatible exception.
See [Environment-state API migration](environment-state-api-migration.md) for affected APIs
and migration guidance.

## Version Synchronization

The SDK version in `pkg/azdext/version.go` mirrors the CLI version in `cli/version.txt`. Both are updated automatically by `eng/scripts/Update-CliVersion.ps1` during the release process.

Go module tags (`cli/azd/vX.Y.Z`) are created by the AzDO release pipeline (`eng/pipelines/templates/steps/publish-cli.yml`) alongside the CLI release tag.
