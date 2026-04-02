#!/usr/bin/env pwsh

# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

<#
.SYNOPSIS
    Generates test coverage locally, with optional CI integration merge.

.DESCRIPTION
    Builds the azd binary with coverage instrumentation, runs unit tests and
    optionally integration/functional tests, merges the coverage data, filters
    generated code, and produces a per-package coverage report.

    This script mirrors the CI coverage pipeline (ci-build.ps1 + ci-test.ps1
    + code-coverage-upload.yml) but runs entirely locally — no Azure DevOps
    artifact downloads required.

    Three modes are available:
      - Unit only (-UnitOnly): Fastest — runs 'go test -short' with coverage.
      - Hybrid (-MergeWithCI): Runs unit tests locally and downloads
        integration coverage from the latest CI build, then merges both.
        Requires 'az login' for Azure DevOps access.
      - Full combined (default): Builds with -cover and runs both unit and
        integration/functional tests locally. Slowest but fully self-contained.

    Coverage collection uses Go's binary coverage format:
      - Unit tests: 'go test -cover -args --test.gocoverdir=<dir>' captures
        in-process test coverage.
      - Integration tests: The GOCOVERDIR environment variable causes the
        coverage-instrumented azd binary to emit coverage whenever functional
        tests spawn it.

    For CI-produced coverage (which includes multi-platform merges), use
    Get-CICoverageReport.ps1. For a full overview of all coverage modes,
    see cli/azd/docs/code-coverage-guide.md.

.PARAMETER ShowReport
    Prints a per-package coverage table sorted by coverage percentage.
    Format matches Get-CICoverageReport.ps1 output for easy comparison.

.PARAMETER UnitOnly
    Skip integration/functional tests. Much faster; produces unit-only
    coverage numbers (comparable to 'go test -short -cover').
    Mutually exclusive with -MergeWithCI.

.PARAMETER MergeWithCI
    Run unit tests locally and merge with integration coverage downloaded
    from the latest CI build. Requires Azure CLI authentication for Azure
    DevOps API access (run 'az login' first). Much faster than full local
    integration testing while still producing combined coverage numbers.
    Mutually exclusive with -UnitOnly.

.PARAMETER SkipBuild
    Skip building the azd binary with coverage instrumentation. Use when
    you already have a coverage-instrumented binary from a previous run.
    Only relevant for full combined (non-UnitOnly, non-MergeWithCI) mode.

.PARAMETER MinCoverage
    Minimum coverage threshold (0-100). When greater than 0, the script
    exits with a non-zero code if total statement coverage is below this
    value. Uses Test-CodeCoverageThreshold.ps1 when available.

.PARAMETER Html
    Generate an HTML coverage report and open it in the default browser.

.PARAMETER PullRequestId
    When used with -MergeWithCI, download integration coverage from the CI
    build for this GitHub pull request number. Defaults to 0 (latest
    successful main build).

.PARAMETER BuildId
    When used with -MergeWithCI, use this specific Azure DevOps build ID.
    Defaults to 0 (auto-detect latest successful build).

.EXAMPLE
    # Quick unit-only coverage with per-package report
    ./Get-LocalCoverageReport.ps1 -ShowReport -UnitOnly

.EXAMPLE
    # Hybrid: local unit tests + CI integration baseline
    ./Get-LocalCoverageReport.ps1 -ShowReport -MergeWithCI

.EXAMPLE
    # Hybrid with a specific PR's integration coverage
    ./Get-LocalCoverageReport.ps1 -ShowReport -MergeWithCI -PullRequestId 7350

.EXAMPLE
    # Full combined coverage (unit + integration) with threshold check
    ./Get-LocalCoverageReport.ps1 -ShowReport -MinCoverage 48

.EXAMPLE
    # Generate HTML coverage report for unit tests
    ./Get-LocalCoverageReport.ps1 -UnitOnly -Html

.EXAMPLE
    # Re-run report without rebuilding (reuse previous binary)
    ./Get-LocalCoverageReport.ps1 -SkipBuild -ShowReport
#>

param(
    [switch]$ShowReport,
    [switch]$UnitOnly,
    [switch]$MergeWithCI,
    [switch]$SkipBuild,
    [ValidateRange(0, 100)]
    [int]$MinCoverage = 0,
    [switch]$Html,
    [int]$PullRequestId = 0,
    [int]$BuildId = 0
)

$ErrorActionPreference = 'Stop'

# ---------------------------------------------------------------------------
# Parameter validation
# ---------------------------------------------------------------------------
if ($MergeWithCI -and $UnitOnly) {
    throw "-MergeWithCI and -UnitOnly are mutually exclusive. " +
          "Use -MergeWithCI for local unit + CI integration, or -UnitOnly for local unit only."
}

# ---------------------------------------------------------------------------
# Resolve paths
# ---------------------------------------------------------------------------
# Script lives at eng/scripts/; cli/azd is two levels up then into cli/azd.
$repoRoot = Resolve-Path (Join-Path $PSScriptRoot "../..")
$azdRoot  = Join-Path $repoRoot "cli/azd"

if (-not (Test-Path (Join-Path $azdRoot "go.mod"))) {
    throw "Cannot find cli/azd/go.mod relative to script location. Expected at: $azdRoot"
}

Push-Location $azdRoot

# Disable Go workspace mode (required for this monorepo layout).
$savedGoWork = $env:GOWORK
$env:GOWORK = "off"

$coverBase  = $null  # set below; used in finally for cleanup
$ciBuildLabel = ''   # set by MergeWithCI; used in report
# UnitOnly = 4 steps; MergeWithCI = 5 steps; combined = 6 steps.
$stepTotal  = if ($UnitOnly) { 4 } elseif ($MergeWithCI) { 5 } else { 6 }
$stepNum    = 0

function Write-Step {
    param([string]$Message)
    $script:stepNum++
    Write-Host ""
    Write-Host "[$script:stepNum/$stepTotal] $Message" -ForegroundColor Cyan
}

try {
    Write-Host ""
    Write-Host "Local Coverage Report" -ForegroundColor Cyan
    Write-Host "=====================" -ForegroundColor Cyan
    Write-Host "  Working directory : $azdRoot"
    $modeDisplay = if ($UnitOnly) { 'Unit Only' } `
        elseif ($MergeWithCI) { 'Hybrid (Local Unit + CI Integration)' } `
        else { 'Combined (Unit + Integration)' }
    Write-Host "  Mode              : $modeDisplay"
    Write-Host ""

    # ------------------------------------------------------------------
    # Create temp directories for binary coverage data
    # ------------------------------------------------------------------
    $coverBase = Join-Path ([System.IO.Path]::GetTempPath()) "azd-local-coverage-$PID"
    if (Test-Path $coverBase) {
        Remove-Item -Recurse -Force $coverBase
    }

    $unitDir   = (New-Item -ItemType Directory -Force -Path (Join-Path $coverBase "cover-unit")).FullName
    $intDir    = (New-Item -ItemType Directory -Force -Path (Join-Path $coverBase "cover-int")).FullName
    $mergedDir = (New-Item -ItemType Directory -Force -Path (Join-Path $coverBase "cover-merged")).FullName

    $outputFile = Join-Path $azdRoot "cover-local.out"

    # ==================================================================
    # Step 1: Build azd with coverage instrumentation
    # ==================================================================
    if (-not $UnitOnly -and -not $MergeWithCI) {
        if (-not $SkipBuild) {
            Write-Step "Building azd with coverage instrumentation..."
            $binaryName = if ($IsWindows) { "azd.exe" } else { "azd" }

            go build -cover -o $binaryName
            if ($LASTEXITCODE) {
                throw "go build -cover failed (exit code: $LASTEXITCODE)"
            }
            Write-Host "  Built $binaryName with -cover" -ForegroundColor Green
        } else {
            Write-Step "Skipping build (-SkipBuild)"
            $binaryName = if ($IsWindows) { "azd.exe" } else { "azd" }
            if (-not (Test-Path (Join-Path $azdRoot $binaryName))) {
                Write-Warning "No azd binary found at $azdRoot/$binaryName — integration coverage may be empty."
            }
        }
    } else {
        # Unit-only: no binary needed for coverage, but adjust step count
        # (step 1 is skipped entirely, numbering starts at step 2 below)
    }

    # ==================================================================
    # Step 2: Run unit tests with binary coverage
    # ==================================================================
    Write-Step "Running unit tests with coverage..."
    Write-Host "  Coverage dir: $unitDir"

    # Mirrors ci-test.ps1 unit phase:
    #   -short          → skip functional tests (TestMain exits) and slow tests
    #   -cover          → instrument test binaries for coverage
    #   --test.gocoverdir → write binary-format coverage to directory
    #   -count=1        → disable test caching for fresh results
    go test ./... -short -count=1 -cover -args "--test.gocoverdir=$unitDir"
    if ($LASTEXITCODE) {
        throw "Unit tests failed (exit code: $LASTEXITCODE). Fix test failures before measuring coverage."
    }

    $unitFileCount = @(Get-ChildItem $unitDir -File -ErrorAction SilentlyContinue).Count
    Write-Host "  Unit tests passed ($unitFileCount coverage files)" -ForegroundColor Green

    # ==================================================================
    # Step 3: Run integration/functional tests (optional)
    # ==================================================================
    $hasIntCoverage = $false

    if (-not $UnitOnly -and -not $MergeWithCI) {
        Write-Step "Running integration/functional tests..."
        Write-Host "  Coverage dir: $intDir"
        Write-Host "  GOCOVERDIR causes the coverage-instrumented azd binary to"
        Write-Host "  emit coverage data whenever functional tests spawn it."
        Write-Host ""
        Write-Host "  Note: Tests requiring Azure credentials run in playback mode" -ForegroundColor Yellow
        Write-Host "  if recordings exist, otherwise they may be skipped." -ForegroundColor Yellow

        $savedGoCoverDir = $env:GOCOVERDIR
        # GOCOVERDIR makes any binary built with 'go build -cover' write
        # coverage data on exit — this is how functional tests that spawn
        # the azd binary contribute to integration coverage.
        $env:GOCOVERDIR = $intDir

        try {
            # Mirrors ci-test.ps1 integration phase:
            #   no -short   → functional tests run (TestMain doesn't exit)
            #   no -cover   → in-process coverage not needed (unit phase has it)
            #   -timeout    → allow long-running functional tests
            #   -count=1    → disable caching
            go test ./... -count=1 -timeout 120m
            $intExitCode = $LASTEXITCODE
        } finally {
            if ($null -eq $savedGoCoverDir) {
                Remove-Item Env:\GOCOVERDIR -ErrorAction SilentlyContinue
            } else {
                $env:GOCOVERDIR = $savedGoCoverDir
            }
        }

        if ($intExitCode) {
            Write-Warning "Some integration tests failed (exit code: $intExitCode)."
            Write-Warning "Coverage from completed tests is still included."
        } else {
            Write-Host "  Integration tests passed" -ForegroundColor Green
        }

        # Check whether the azd binary produced any coverage files
        $intFileCount = @(Get-ChildItem $intDir -File -ErrorAction SilentlyContinue).Count
        if ($intFileCount -gt 0) {
            $hasIntCoverage = $true
            Write-Host "  Integration coverage files: $intFileCount"
        } else {
            Write-Host "  No integration coverage data produced." -ForegroundColor Yellow
            Write-Host "  (Functional tests may have been skipped or the binary was not coverage-instrumented.)" -ForegroundColor Yellow
        }
    }

    # ==================================================================
    # Step 3b: Download CI integration coverage (hybrid mode)
    # ==================================================================
    if ($MergeWithCI) {
        Write-Step "Downloading CI integration coverage..."

        # ADO settings (match Get-CICoverageReport.ps1 defaults)
        $adoOrg      = 'https://dev.azure.com/azure-sdk'
        $adoProject  = 'internal'
        $adoPipeline = '4643'

        # Authenticate with Azure DevOps
        $adoToken = $null
        try {
            $adoToken = az account get-access-token `
                --resource "499b84ac-1321-427f-aa17-267ca6975798" `
                --query accessToken -o tsv
            if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($adoToken)) {
                throw "token_failed"
            }
        } catch {
            Write-Host ""
            Write-Host "ERROR: Cannot authenticate with Azure DevOps." -ForegroundColor Red
            Write-Host "  Run 'az login' first, then retry -MergeWithCI." -ForegroundColor Red
            Write-Host "  Or use -UnitOnly for local-only coverage (no ADO access needed)." -ForegroundColor Red
            Write-Host ""
            throw "Azure DevOps authentication failed. Run 'az login' or use -UnitOnly instead."
        }

        $adoHeaders = @{ Authorization = "Bearer $adoToken" }

        # Find the CI build to pull integration coverage from
        $ciBuildId = $BuildId
        if ($ciBuildId -eq 0) {
            if ($PullRequestId -gt 0) {
                Write-Host "  Finding latest build for PR #$PullRequestId..."
                $prBranch  = "refs/pull/$PullRequestId/merge"
                $buildsUrl = "$adoOrg/$adoProject/_apis/build/builds?" +
                    "definitions=$adoPipeline&branchName=$prBranch&`$top=1&api-version=7.1"
                $buildsResp = Invoke-RestMethod -Uri $buildsUrl -Headers $adoHeaders -Method Get

                if ($buildsResp.count -eq 0) {
                    Write-Warning "No CI builds found for PR #$PullRequestId. Falling back to unit-only coverage."
                } else {
                    $ciBuild      = $buildsResp.value[0]
                    $ciBuildId    = $ciBuild.id
                    $ciBuildLabel = "PR #$PullRequestId build #$ciBuildId ($($ciBuild.buildNumber))"
                    Write-Host "  Using $ciBuildLabel"
                }
            } else {
                Write-Host "  Finding latest successful build from main..."
                $buildsUrl = "$adoOrg/$adoProject/_apis/build/builds?" +
                    "definitions=$adoPipeline&branchName=refs/heads/main" +
                    "&resultFilter=succeeded&`$top=1&api-version=7.1"
                $buildsResp = Invoke-RestMethod -Uri $buildsUrl -Headers $adoHeaders -Method Get

                if ($buildsResp.count -eq 0) {
                    Write-Warning "No successful CI builds found on main. Falling back to unit-only coverage."
                } else {
                    $ciBuild      = $buildsResp.value[0]
                    $ciBuildId    = $ciBuild.id
                    $ciBuildLabel = "main build #$ciBuildId ($($ciBuild.buildNumber))"
                    Write-Host "  Using $ciBuildLabel"
                }
            }
        } else {
            $ciBuildLabel = "build #$ciBuildId"
            Write-Host "  Using specified $ciBuildLabel"
        }

        # Download the cover-int artifact (integration coverage only)
        if ($ciBuildId -gt 0) {
            try {
                $artifactUrl = "$adoOrg/$adoProject/_apis/build/builds/$ciBuildId" +
                    "/artifacts?artifactName=cover-int&api-version=7.1"
                $artifactResp = Invoke-RestMethod -Uri $artifactUrl -Headers $adoHeaders -Method Get
                $downloadUrl  = $artifactResp.resource.downloadUrl

                $zipPath = Join-Path $coverBase "cover-int-ci.zip"
                Invoke-WebRequest -Uri $downloadUrl -Headers $adoHeaders -OutFile $zipPath

                $extractPath = Join-Path $coverBase "cover-int-ci"
                Expand-Archive -Path $zipPath -DestinationPath $extractPath -Force
                Remove-Item $zipPath

                # Pipeline artifacts nest under the artifact name
                $nested = Join-Path $extractPath "cover-int"
                $ciIntDir = if (Test-Path $nested) { $nested } else { $extractPath }

                # Copy CI integration files into intDir for merging
                $ciFiles = @(Get-ChildItem $ciIntDir -File -ErrorAction SilentlyContinue)
                if ($ciFiles.Count -gt 0) {
                    Copy-Item -Path (Join-Path $ciIntDir '*') -Destination $intDir -Force
                    $hasIntCoverage = $true
                    Write-Host "  CI integration coverage: $($ciFiles.Count) files from $ciBuildLabel" -ForegroundColor Green
                } else {
                    Write-Warning "CI integration artifact was empty. Using unit-only coverage."
                }
            } catch {
                Write-Warning "Failed to download CI integration coverage: $_"
                Write-Warning "Falling back to unit-only coverage."
            }
        }
    }

    # ==================================================================
    # Step 4: Merge coverage data
    # ==================================================================
    Write-Step "Merging coverage data..."

    if ($hasIntCoverage) {
        $mergeInputs = "$unitDir,$intDir"
        $sourceLabel = if ($MergeWithCI) { "local unit + CI integration" } else { "unit + integration" }
        Write-Host "  Sources: $sourceLabel"
    } else {
        $mergeInputs = $unitDir
        Write-Host "  Sources: unit only"
    }

    go tool covdata merge "-i=$mergeInputs" -o $mergedDir
    if ($LASTEXITCODE) {
        throw "go tool covdata merge failed (exit code: $LASTEXITCODE)"
    }

    # Convert binary coverage to text profile format
    go tool covdata textfmt "-i=$mergedDir" -o $outputFile
    if ($LASTEXITCODE) {
        throw "go tool covdata textfmt failed (exit code: $LASTEXITCODE)"
    }

    $lineCount = (Get-Content $outputFile).Count
    Write-Host "  Merged profile: $outputFile ($lineCount lines)"

    # ==================================================================
    # Step 5: Filter generated code
    # ==================================================================
    Write-Step "Filtering generated code..."

    $filterScript = Join-Path $PSScriptRoot "Filter-GeneratedCoverage.ps1"
    if (Test-Path $filterScript) {
        & $filterScript -CoverageFile $outputFile
        if ($LASTEXITCODE) {
            throw "Filter-GeneratedCoverage.ps1 failed (exit code: $LASTEXITCODE)"
        }
    } else {
        Write-Warning "Filter-GeneratedCoverage.ps1 not found at $filterScript — skipping."
    }

    # ==================================================================
    # Step 6: Report
    # ==================================================================
    Write-Step "Producing report..."

    # --- Total coverage via go tool cover -func ---
    $funcOutput = & go tool cover "-func=$outputFile"
    if ($LASTEXITCODE) {
        throw "go tool cover -func failed (exit code: $LASTEXITCODE)"
    }

    $totalLine = $funcOutput | Where-Object { $_ -match '^\s*total:' } | Select-Object -Last 1
    $totalPct = 0.0
    if ($totalLine -match '(\d+(?:\.\d+)?)%') {
        $totalPct = [double]::Parse($Matches[1], [System.Globalization.CultureInfo]::InvariantCulture)
    }

    $modeLabel = if ($UnitOnly) { "Unit Only" } `
        elseif ($MergeWithCI) { "Hybrid (Local Unit + CI Integration)" } `
        else { "Combined (Unit + Integration)" }

    Write-Host ""
    Write-Host "==========================================" -ForegroundColor Cyan
    Write-Host "  Local Coverage Summary" -ForegroundColor Cyan
    Write-Host "==========================================" -ForegroundColor Cyan
    Write-Host ""
    Write-Host "  Mode:        $modeLabel"
    if ($ciBuildLabel) {
        Write-Host "  CI baseline: $ciBuildLabel"
    }
    Write-Host "  Coverage:    $totalPct%"
    Write-Host "  Profile:     $outputFile"
    Write-Host ""

    # --- Per-package breakdown (matches Get-CICoverageReport.ps1 format) ---
    if ($ShowReport) {
        Write-Host "==========================================" -ForegroundColor Cyan
        Write-Host "  Per-Package Coverage Report" -ForegroundColor Cyan
        Write-Host "==========================================" -ForegroundColor Cyan
        Write-Host ""

        $percentOutput = go tool covdata percent "-i=$mergedDir" 2>&1
        $parsed = @($percentOutput | ForEach-Object {
            if ($_ -match '([\w/./-]+)\s+coverage:\s+([\d.]+)%') {
                $pkg = $Matches[1] -replace 'github\.com/azure/azure-dev/cli/azd/', ''
                $pct = [double]$Matches[2]
                [PSCustomObject]@{ Package = $pkg; Coverage = $pct }
            }
        })

        if ($parsed.Count -gt 0) {
            Write-Host "All packages (sorted by coverage):"
            Write-Host ""
            $parsed | Sort-Object Coverage | Format-Table -AutoSize

            $avg = ($parsed | Measure-Object -Property Coverage -Average).Average
            Write-Host "Packages: $($parsed.Count) | Average coverage: $([math]::Round($avg, 1))%"
        } else {
            Write-Host "  No per-package data available." -ForegroundColor Yellow
        }
        Write-Host ""
    }

    # --- Threshold check ---
    if ($MinCoverage -gt 0) {
        $thresholdScript = Join-Path $PSScriptRoot "Test-CodeCoverageThreshold.ps1"
        if (Test-Path $thresholdScript) {
            & $thresholdScript -CoverageFile $outputFile -MinimumCoveragePercent $MinCoverage
            if ($LASTEXITCODE) {
                exit $LASTEXITCODE
            }
        } else {
            # Inline fallback if threshold script is missing
            if ($totalPct -lt $MinCoverage) {
                Write-Host "Coverage $totalPct% is below minimum threshold of $MinCoverage%." -ForegroundColor Red
                exit 1
            }
            Write-Host "Coverage $totalPct% meets minimum threshold of $MinCoverage% `u{2713}" -ForegroundColor Green
        }
    }

    # --- HTML report ---
    if ($Html) {
        $htmlFile = Join-Path $azdRoot "coverage.html"
        Write-Host "Generating HTML coverage report..."
        go tool cover "-html=$outputFile" "-o=$htmlFile"
        if ($LASTEXITCODE) {
            throw "go tool cover -html failed (exit code: $LASTEXITCODE)"
        }
        Write-Host "  HTML report: $htmlFile" -ForegroundColor Green

        if ($IsWindows) {
            Start-Process $htmlFile
        } elseif ($IsMacOS) {
            & open $htmlFile
        } elseif ($IsLinux) {
            & xdg-open $htmlFile 2>$null
        }
    }

    Write-Host ""
    Write-Host "Done. Coverage profile: $outputFile" -ForegroundColor Green

} finally {
    # Restore environment
    if ($null -eq $savedGoWork) {
        Remove-Item Env:\GOWORK -ErrorAction SilentlyContinue
    } else {
        $env:GOWORK = $savedGoWork
    }

    # Clean up temp directories (the output file is outside temp)
    if ($coverBase -and (Test-Path $coverBase -ErrorAction SilentlyContinue)) {
        Remove-Item -Recurse -Force $coverBase -ErrorAction SilentlyContinue
    }

    Pop-Location
}
