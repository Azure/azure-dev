# Code Coverage Guide

## Overview

azd measures code coverage from two complementary sources:

- **Unit tests** (`go test -short`): Exercise individual functions and packages
  in-process. Fast, no external dependencies.
- **Integration/functional tests** (`go test` without `-short`): Spawn the
  coverage-instrumented `azd` binary and exercise real CLI workflows. These
  contribute coverage that unit tests alone cannot reach.

Combining both produces the true coverage picture. Running only unit tests
typically underestimates coverage by roughly 9 percentage points compared to
the combined CI result.

## Coverage Architecture

The CI pipeline collects coverage in several stages:

1. **Build**: `ci-build.ps1` builds the azd binary with `go build -cover`,
   producing an instrumented binary that emits coverage data on exit.

2. **Unit tests**: `ci-test.ps1` runs `go test -short -cover -args
   --test.gocoverdir=<dir>` — the `-cover` flag instruments test binaries,
   and `--test.gocoverdir` writes binary coverage to a directory.

3. **Integration tests**: `ci-test.ps1` runs `go test` (without `-short`)
   with `GOCOVERDIR` set. Functional tests in `test/functional/` spawn the
   coverage-instrumented binary; `GOCOVERDIR` causes it to write coverage
   data on exit.

4. **Artifact upload**: Coverage directories are uploaded as Azure DevOps
   pipeline artifacts (`cover-unit` and `cover-int`) per platform.

5. **Merge**: `code-coverage-upload.yml` downloads artifacts from all
   platforms and merges them with `go tool covdata merge`.

6. **Filter**: `Filter-GeneratedCoverage.ps1` removes auto-generated files
   (e.g., `*.pb.go`) so coverage reflects only hand-written code.

7. **Threshold**: `Test-CodeCoverageThreshold.ps1` enforces the minimum
   coverage gate, failing the build if coverage drops below the threshold.

## Developer Modes

Four modes are available for measuring coverage locally:

### A. Unit Only (recommended for iteration)

```powershell
./eng/scripts/Get-LocalCoverageReport.ps1 -ShowReport -UnitOnly
```

- **Prerequisites**: Go 1.26
- **Speed**: ~5-10 minutes
- **What it does**: Runs `go test -short -cover` and collects unit coverage.
- **When to use**: During active development for fast feedback on your changes.

### B. Hybrid (local unit + CI integration)

```powershell
./eng/scripts/Get-LocalCoverageReport.ps1 -ShowReport -MergeWithCI
```

- **Prerequisites**: Go 1.26, `az login` (Azure CLI authenticated to azure-sdk org)
- **Speed**: ~6-11 minutes (unit tests + ~1 min download)
- **What it does**: Runs unit tests locally, downloads the integration coverage
  from the latest successful CI build, and merges both.
- **When to use**: When you want combined numbers without running the slow
  integration tests locally. Great for pre-PR checks.

Options:
- `-PullRequestId <N>`: Use integration coverage from a specific PR's CI build
  instead of latest main.
- `-BuildId <N>`: Use a specific Azure DevOps build ID.

### C. Full Local (unit + integration)

```powershell
./eng/scripts/Get-LocalCoverageReport.ps1 -ShowReport
```

- **Prerequisites**: Go 1.26, Azure subscription, service principal credentials
  (see Prerequisites section below)
- **Speed**: ~30-60 minutes
- **What it does**: Builds azd with `-cover`, runs both unit and integration
  tests locally, and merges the coverage.
- **When to use**: When you need fully self-contained coverage with no CI
  dependency, or when testing integration changes that haven't been pushed yet.

### D. CI Baseline (latest main)

```powershell
./eng/scripts/Get-CICoverageReport.ps1 -ShowReport
```

- **Prerequisites**: `az login` (Azure CLI authenticated to azure-sdk org)
- **Speed**: ~1 minute
- **What it does**: Downloads both unit and integration coverage from the latest
  successful CI build on main and produces a report.
- **When to use**: To see the current baseline or compare your changes against
  the latest stable numbers.

Options:
- `-PullRequestId <N>`: Download coverage from a specific PR's CI build.
- `-BuildId <N>`: Use a specific build ID.
- `-MinCoverage <N>`: Filter the report to packages below a threshold.

### Common Options

All local modes (`Get-LocalCoverageReport.ps1`) support:
- `-Html`: Generate an HTML report and open it in your browser.
- `-MinCoverage <N>`: Fail if total coverage is below this percentage.
- `-ShowReport`: Print a per-package coverage table sorted by percentage.

## Prerequisites

### Unit Only

- [Go](https://go.dev/dl/) 1.26
- Set `$env:GOWORK="off"` (required for this monorepo layout)

### Hybrid (-MergeWithCI) and CI Baseline

- Go 1.26
- [Azure CLI](https://learn.microsoft.com/cli/azure/install-azure-cli)
  authenticated to the azure-sdk organization:
  ```powershell
  az login
  ```
  The scripts use `az account get-access-token` to authenticate with
  the Azure DevOps API for downloading pipeline artifacts.

### Full Local

- Go 1.26
- Azure subscription with a service principal configured for integration tests.
  Set these environment variables:
  ```powershell
  $env:AZD_TEST_CLIENT_ID = "<service-principal-client-id>"
  $env:AZD_TEST_TENANT_ID = "<tenant-id>"
  $env:AZD_TEST_AZURE_SUBSCRIPTION_ID = "<subscription-id>"
  $env:AZD_TEST_AZURE_LOCATION = "eastus2"  # or your preferred region
  ```

## Generated Code Filtering

`Filter-GeneratedCoverage.ps1` removes coverage entries for auto-generated
files so that percentages reflect only hand-written source code. Currently,
only protobuf-generated files (`*.pb.go`) are filtered.

Impact: Filtering typically raises the reported coverage by about 4.6
percentage points because generated files have low or no test coverage and
would otherwise drag down the average.

Custom patterns can be added via the `-ExcludePatterns` parameter if
additional code generators are introduced.

## CI Gate

The CI pipeline enforces a minimum coverage threshold using
`Test-CodeCoverageThreshold.ps1` in the `release-cli.yml` pipeline.

- **Current threshold**: Check the pipeline definition for the latest value.
- **Ratchet policy**: The threshold is periodically raised as coverage improves.
  PRs that reduce coverage below the threshold will fail the coverage gate.
- **Enforcement**: The threshold script parses `go tool cover -func` output and
  exits non-zero if the total statement coverage is below the minimum.

## Scripts Reference

| Script | Location | Purpose |
|--------|----------|---------|
| `Get-LocalCoverageReport.ps1` | `eng/scripts/` | Developer-facing: runs coverage locally in any of the 4 modes |
| `Get-CICoverageReport.ps1` | `eng/scripts/` | Downloads combined coverage from Azure DevOps CI builds |
| `Filter-GeneratedCoverage.ps1` | `eng/scripts/` | Strips auto-generated files (`.pb.go`) from coverage profiles |
| `Test-CodeCoverageThreshold.ps1` | `eng/scripts/` | Enforces minimum coverage gate; used by CI and `-MinCoverage` |
| `Convert-GoCoverageToCobertura.ps1` | `eng/scripts/` | Converts Go coverage to Cobertura XML for ADO reporting (CI only) |
| `ci-build.ps1` | `eng/scripts/` | CI: builds azd binary with `-cover` instrumentation |
| `ci-test.ps1` | `eng/scripts/` | CI: runs unit and integration tests with coverage collection |

## Mage Targets

All modes are also available as `mage` targets (from `cli/azd/`):

| Target | Mode | Prerequisites |
|--------|------|---------------|
| `mage coverage:unit` | Unit only + report | Go 1.26 |
| `mage coverage:full` | Full local (unit + integration) + report | Go 1.26, Azure resources |
| `mage coverage:hybrid` | Hybrid (local unit + CI integration) + report | Go 1.26, `az login` |
| `mage coverage:ci` | CI baseline report | `az login` |
| `mage coverage:html` | HTML report (unit only by default) | Go 1.26 |
| `mage coverage:check` | Enforce 55% threshold (unit only) | Go 1.26 |

Environment variables for optional overrides:

| Variable | Used by | Purpose |
|----------|---------|---------|
| `COVERAGE_PULL_REQUEST_ID` | `hybrid`, `ci` | Target a specific PR's CI run |
| `COVERAGE_BUILD_ID` | `hybrid`, `ci` | Target a specific ADO build ID |
| `COVERAGE_MODE` | `html` | Set to `full` or `hybrid` (default: `unit`) |
| `COVERAGE_MIN` | `check` | Override threshold (default: `55`) |

## Troubleshooting

| Error | Fix |
|-------|-----|
| `az account get-access-token` failed | Run `az login`. Ensure access to the `azure-sdk` ADO org. |
| No coverage artifacts found | Verify the CI build completed with `succeeded` result and artifacts (`cover-unit`, `cover-int`) were uploaded. |
| Integration coverage empty locally | Requires a binary built with `go build -cover` and `GOCOVERDIR` set. Use `-MergeWithCI` for an easier path. |
| GOWORK errors | Set `$env:GOWORK = "off"`. The coverage scripts do this automatically, but set it manually for standalone `go tool covdata` commands. |
| Slow unit tests | Omit `-count=1` for cached runs during iteration. Ensure you are in `cli/azd/`, not the repo root. |
