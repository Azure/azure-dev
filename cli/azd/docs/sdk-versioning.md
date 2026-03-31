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

## Version Synchronization

The SDK version in `pkg/azdext/version.go` mirrors the CLI version in `cli/version.txt`. Both are updated automatically by `eng/scripts/Update-CliVersion.ps1` during the release process.

Go module tags (`cli/azd/vX.Y.Z`) are created by the AzDO release pipeline (`eng/pipelines/templates/steps/publish-cli.yml`) alongside the CLI release tag.
