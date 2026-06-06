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

7. **PR gate** (PR builds only): `Get-CoverageDiff.ps1` compares the merged
   profile against the latest successful `main` baseline and fails the build
   on either a per-package decrease > 0.5 pp or overall coverage below the
   floor. Release/main builds skip this step and only publish artifacts.

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

The CI pipeline enforces a two-gate coverage check on **PR builds only**
using `Get-CoverageDiff.ps1` (invoked from
`eng/pipelines/templates/stages/code-coverage-upload.yml`):

- **Per-package gate**: any PR-touched package that drops more than
  `MaxPackageDecrease` percentage points (default **0.5 pp**) versus the
  latest successful `main` baseline fails the build.
- **Overall floor gate**: if the PR's overall coverage falls below
  `MinOverallCoverage` (default **69%**), the build fails.
- **Failure mode**: the script emits `##vso[task.logissue type=error]`
  for each breached gate and exits with code `2`, which fails the ADO job.
- **Scope**: release, scheduled, and `main` builds **do not** enforce
  these gates — they only publish coverage artifacts. A coverage dip on
  `main` will surface on the next PR rather than block a release.
- **Ratchet policy**: see the *Adjusting the absolute floor* runbook
  below.

## Scripts Reference

| Script | Location | Purpose |
|--------|----------|---------|
| `Get-LocalCoverageReport.ps1` | `eng/scripts/` | Developer-facing: runs coverage locally in any of the 4 modes |
| `Get-CICoverageReport.ps1` | `eng/scripts/` | Downloads combined coverage from Azure DevOps CI builds |
| `Filter-GeneratedCoverage.ps1` | `eng/scripts/` | Strips auto-generated files (`.pb.go`) from coverage profiles |
| `Get-CoverageDiff.ps1` | `eng/scripts/` | PR coverage gate: two-gate check (per-package decrease + overall floor) used by CI and `mage coverage:pr` |
| `Test-CodeCoverageThreshold.ps1` | `eng/scripts/` | Local minimum-coverage helper used by `Get-LocalCoverageReport.ps1 -MinCoverage` (no longer wired into CI) |
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
| `mage coverage:check` | Enforce 50% threshold (unit only; CI gate is 55% combined) | Go 1.26 |
| `mage coverage:diff` | Compare current branch coverage vs main baseline (advisory; honors `COVERAGE_MAX_PACKAGE_DECREASE` / `COVERAGE_MIN_OVERALL` / `COVERAGE_FAIL_ON_DECREASE`) | Go 1.26 |
| `mage coverage:pr` | Preview the CI PR coverage gate locally (fail-loud on either: per-package regression > 0.5 pp, or overall < 69%) | Go 1.26 |
| `mage coverage:report` | Merge raw covdata input directories into a single `cover.out` (used by CI; honors `COVERAGE_REPORT_*` env vars) | Go 1.26 |

Environment variables for optional overrides:

| Variable | Used by | Purpose |
|----------|---------|---------|
| `COVERAGE_PULL_REQUEST_ID` | `hybrid`, `ci` | Target a specific PR's CI run |
| `COVERAGE_BUILD_ID` | `hybrid`, `ci` | Target a specific ADO build ID |
| `COVERAGE_MODE` | `html` | Set to `full` or `hybrid` (default: `unit`) |
| `COVERAGE_MIN` | `check` | Override threshold (default: `55`) |
| `COVERAGE_MAX_PACKAGE_DECREASE` | `diff`, `pr` | Maximum tolerated per-package coverage drop in percentage points (defaults come from `Get-CoverageDiff.ps1`, currently `0.5`; PR-touched packages only when changed-files can be resolved). Set to `-1` to disable the per-package gate (the floor gate stays active unless `COVERAGE_MIN_OVERALL` is also set to `-1`). |
| `COVERAGE_MIN_OVERALL` | `diff`, `pr` | Absolute floor for overall coverage in percent (defaults come from `Get-CoverageDiff.ps1`, currently `69`). Set to `-1` to disable the floor gate. |
| `COVERAGE_FAIL_ON_DECREASE` | `diff` | Set to `1` / `true` to exit `2` when EITHER gate is breached (`pr` always fails loud). Any other non-zero exit indicates a script/infra error, not a gate breach. **Note:** setting `COVERAGE_MAX_PACKAGE_DECREASE` alone does NOT enable fail-loud mode for `mage coverage:diff` — you must also set `COVERAGE_FAIL_ON_DECREASE=1` (or use `mage coverage:pr`, which always fails loud). |
| `COVERAGE_BASELINE` | `diff`, `pr` | Path to baseline coverage profile (default: `cover-ci-combined.out` or download from CI) |
| `COVERAGE_CURRENT` | `diff`, `pr` | Path to current coverage profile (default: `cover-local.out`) |
| `COVERAGE_REPORT_UNIT_INPUTS` | `report` | Comma-separated list of unit-test covdata input directories. |
| `COVERAGE_REPORT_INT_INPUTS` | `report` | Comma-separated list of integration-test covdata input directories (optional). |
| `COVERAGE_REPORT_OUTPUT` | `report` | Output `cover.out` path (textfmt). |
| `COVERAGE_REPORT_MERGED_DIR` | `report` | Optional intermediate merged covdata directory. Created if absent. |

## PR Coverage Check (Fail-Loud)

PRs run a **two-gate** coverage check as part of the
`code-coverage-upload.yml` Azure DevOps stage. After unit + integration
coverage is merged via `mage coverage:report`, the pipeline:

1. Resolves the list of `.go` files touched by the PR via
   `git diff --name-only --no-renames --diff-filter=AMRD origin/<targetBranch>...HEAD`,
   so per-package results are scoped to the packages this PR touches.
2. Runs `eng/scripts/Get-CoverageDiff.ps1` against the merged baseline
   from the latest successful build of the PR target branch and the PR's `cover.out`.
3. Prints a per-package report (regressions first), the overall delta,
   and the configured tolerances.
4. **Fails the build (`exit 2`) when EITHER of the following is true:**
   - **Per-package decrease**: any single PR-touched package drops by
     more than `MaxPackageDecrease` pp (default **0.5 pp**).
   - **Absolute floor**: overall coverage falls below
     `MinOverallCoverage` percent (default **69%**).
5. Surfaces every breach via `##vso[task.logissue type=error]` so each
   one shows up in the PR check summary.

Per-package results outside the PR-touched set are advisory; they appear
in the report but do not gate the build. There is intentionally **no PR
comment**; the build log is the source of truth.

### Reproducing the gate locally

```powershell
# 1. Build the unit-only profile for your branch
mage coverage:unit

# 2. Run the same gate CI runs
mage coverage:pr
```

`mage coverage:pr` runs `git fetch --no-tags --depth=200 origin main` (best-effort),
resolves changed files via `git merge-base origin/main HEAD` for the
per-package report, applies the default 0.5 pp per-package tolerance and
69% absolute floor, and exits with code `2` when either is breached (any
other non-zero exit indicates a script/infra error). On `main`,
in detached-HEAD state, or when git resolution fails, the target returns
an error rather than silently passing (the "preview" guarantee depends on
running against the same inputs CI uses). For an advisory run on `main`,
use `mage coverage:diff` instead.

### Configuring the tolerance

Override per run:

```powershell
$env:COVERAGE_MAX_PACKAGE_DECREASE = "1.0"; mage coverage:pr
```

Or use the advisory `coverage:diff` target with explicit opt-in:

```powershell
$env:COVERAGE_MAX_PACKAGE_DECREASE = "0.5"
$env:COVERAGE_MIN_OVERALL = "69"
$env:COVERAGE_FAIL_ON_DECREASE = "1"
mage coverage:diff
```

### Adjusting the absolute floor (`MinOverallCoverage`)

The PR pipeline fails when overall coverage falls below
`MinOverallCoverage` (default **69%**). The floor is calibrated just below
the observed main overall coverage so it ratchets quality up while leaving
a small safety margin for normal churn. The release / `main` / scheduled
pipelines do not enforce the floor — `CodeCoverage_Upload` runs there only
to publish coverage artifacts. When a wave of refactors or generated-code
changes shifts overall coverage below the floor, follow this runbook so
PRs don't get jammed:

1. **Confirm the dip is real** — pull the latest combined profile and read
   the overall number:

   ```powershell
   mage coverage:ci
   go tool cover "-func=cover-ci-combined.out" | Select-String "^total:"
   ```

   If the dip is genuine (not a flaky platform leg producing artifact gaps),
   continue.

2. **Lower the floor temporarily** in
   `eng/pipelines/templates/stages/code-coverage-upload.yml` — find the
   `MinOverallCoverage` parameter and set `default:` to **1pp below** the new
   observed overall (e.g. observed 62.5% → set 61). This unblocks `main`.

3. **File a coverage-debt issue** capturing: the package(s) responsible for
   the regression, the floor delta you applied, and a target date to ratchet
   back. Without this step the floor silently stays loose forever.

4. **Ratchet the floor back up** once the responsible package(s) regain
   coverage. Bump `default:` to **1pp below the new observed overall**.
   Avoid moving the floor in increments larger than 2pp at a time so that a
   single flaky monthly measurement can't lock in an artificially-high
   floor.

> ⚠️ **Branch-protection note (one-time setup, repo admin):** the gate's
> exit code is only enforced on merges if the `CodeCoverage_Upload` stage
> is configured as a **required status check** on `main` in the repo's
> branch-protection rules. Without this, a PR can merge while the coverage
> check is red.
>
> A repo admin can confirm or add the rule via the GitHub UI
> (`Settings → Branches → Branch protection rules → main → Require status
> checks to pass before merging`), selecting the
> `azure-dev - ci - CodeCoverage_Upload` check.
>
> Equivalent `gh` CLI command (run by an admin once after the first
> successful build of this PR):
>
> ```bash
> gh api -X PATCH repos/Azure/azure-dev/branches/main/protection \
>     -f 'required_status_checks[strict]=true' \
>     -f 'required_status_checks[contexts][]=azure-dev - ci - CodeCoverage_Upload'
> ```
>
> The exact context name comes from the GitHub Checks tab on a PR build —
> verify the string before applying. The check appears only after the
> stage has run on at least one PR, so seed it by opening a draft PR
> first.

### Worked example

Suppose this PR touches `pkg/auth` and drops its coverage from 72.0% → 48.0% (a
24.0 pp drop, well past the 0.5 pp tolerance). Overall coverage stays at 70.0%
(comfortably above the 69% floor — passes). The CI step prints:

```
============================================================
Coverage Report
============================================================
Baseline: baseline
Overall: 70.4% -> 70.0% (-0.4%)
  Tolerance: -0.5 pp per package before failing the gate
  Floor: overall coverage must stay >= 69.0%
PR-touched packages (2 packages):
  pkg/auth                  72.0% ->  48.0%  ( -24.0%)  regress  (1 file touched)
  pkg/project               81.0% ->  82.0%  (  +1.0%)  improved (2 files touched)
============================================================
RESULT: FAIL
============================================================
Breached gate(s):
  - 1 package(s) dropped more than 0.5 pp:
      pkg/auth: 72.0% -> 48.0% (-24.0 pp)
============================================================
```

…then emits:

```
##vso[task.logissue type=error]Package pkg/auth dropped 24.0 pp (max allowed: -0.5 pp).
```

…and exits 2. The PR check summary shows the error, and the build fails. If
overall coverage had also fallen below 69.0%, a second `##vso[task.logissue]`
line would name the floor breach (both gates report independently).

## Troubleshooting

| Error | Fix |
|-------|-----|
| `az account get-access-token` failed | Run `az login`. Ensure access to the `azure-sdk` ADO org. |
| No coverage artifacts found | Verify the CI build completed with `succeeded` result and artifacts (`cover-unit`, `cover-int`) were uploaded. |
| Integration coverage empty locally | Requires a binary built with `go build -cover` and `GOCOVERDIR` set. Use `-MergeWithCI` for an easier path. |
| GOWORK errors | Set `$env:GOWORK = "off"`. The coverage scripts do this automatically, but set it manually for standalone `go tool covdata` commands. |
| Slow unit tests | Omit `-count=1` for cached runs during iteration. Ensure you are in `cli/azd/`, not the repo root. |
