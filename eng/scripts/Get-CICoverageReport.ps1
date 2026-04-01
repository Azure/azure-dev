#!/usr/bin/env pwsh

<#
.SYNOPSIS
    Downloads and analyzes combined (unit + integration) test coverage from CI.

.DESCRIPTION
    Fetches coverage artifacts from the Azure DevOps CI pipeline, merges unit
    and integration test coverage using 'go tool covdata', and produces a
    per-package coverage report sorted by coverage percentage.

    This gives the TRUE coverage picture — including coverage from functional
    tests that exercise the azd binary built with '-cover'. Running
    'go test -short -cover' locally only measures unit test coverage and can
    significantly underestimate actual coverage.

    For local combined coverage without Azure DevOps dependency, use
    Get-LocalCoverageReport.ps1 which mirrors this pipeline locally. For a
    hybrid approach (local unit + CI integration), use
    Get-LocalCoverageReport.ps1 -MergeWithCI.

    See cli/azd/docs/code-coverage-guide.md for a full overview of all
    coverage modes, prerequisites, and troubleshooting.

.PARAMETER BuildId
    Azure DevOps build ID to download coverage from. If not specified, uses
    the latest successful build from the main branch (or the PR branch if
    -PullRequestId is set).

.PARAMETER PullRequestId
    GitHub pull request number. Finds the latest CI build for this PR and
    downloads its coverage artifacts. This lets you check coverage from a PR
    without waiting for it to merge.

.PARAMETER Organization
    Azure DevOps organization URL. Defaults to 'https://dev.azure.com/azure-sdk'.

.PARAMETER Project
    Azure DevOps project name or ID. Defaults to 'internal'.

.PARAMETER OutputFile
    Path to write the combined cover.out file. Defaults to 'cover-ci-combined.out'.

.PARAMETER ShowReport
    If set, prints a per-package coverage report sorted by coverage.

.PARAMETER MinCoverage
    If set, filters the report to only show packages below this coverage threshold.

.EXAMPLE
    # Get latest main build coverage
    ./Get-CICoverageReport.ps1 -ShowReport

.EXAMPLE
    # Get coverage from a specific PR
    ./Get-CICoverageReport.ps1 -PullRequestId 7350 -ShowReport

.EXAMPLE
    # Show only packages below 10% coverage
    ./Get-CICoverageReport.ps1 -ShowReport -MinCoverage 10

.EXAMPLE
    # Use a specific build
    ./Get-CICoverageReport.ps1 -BuildId 6065857 -ShowReport
#>

param(
    [int]$BuildId = 0,
    [int]$PullRequestId = 0,
    [string]$Organization = 'https://dev.azure.com/azure-sdk',
    [string]$Project = 'internal',
    [string]$OutputFile = 'cover-ci-combined.out',
    [switch]$ShowReport,
    [double]$MinCoverage = -1,
    [string]$PipelineDefinitionId = '4643'
)

$ErrorActionPreference = 'Stop'

# Resolve organization to just the name for API calls
$orgName = $Organization -replace 'https://dev.azure.com/', ''

# Get Azure DevOps access token
Write-Host "Authenticating with Azure DevOps..."
$token = az account get-access-token --resource "499b84ac-1321-427f-aa17-267ca6975798" --query accessToken -o tsv
if ($LASTEXITCODE) {
    throw "Failed to get Azure DevOps access token. Run 'az login' first."
}
$headers = @{ Authorization = "Bearer $token" }

# Find the build to use
if ($BuildId -eq 0) {
    if ($PullRequestId -gt 0) {
        # Find the latest build for this PR by searching the merge ref branch
        Write-Host "Finding latest build for PR #$PullRequestId..."

        # Azure DevOps indexes PR builds under refs/pull/<id>/merge
        $prBranch = "refs/pull/$PullRequestId/merge"
        $buildsUrl = "$Organization/$Project/_apis/build/builds?definitions=$PipelineDefinitionId&branchName=$prBranch&`$top=1&api-version=7.1"
        $buildsResp = Invoke-RestMethod -Uri $buildsUrl -Headers $headers -Method Get

        if ($buildsResp.count -eq 0) {
            throw "No builds found for PR #$PullRequestId (pipeline $PipelineDefinitionId). Make sure the PR CI pipeline has run."
        }

        $build = $buildsResp.value[0]
        $BuildId = $build.id
        $buildNumber = $build.buildNumber
        $buildResult = $build.result
        $buildStatus = $build.status

        if ($buildStatus -ne 'completed') {
            Write-Warning "Build $BuildId is still '$buildStatus' — coverage artifacts may not be available yet."
        } elseif ($buildResult -ne 'succeeded') {
            Write-Warning "Build $BuildId result is '$buildResult' — coverage artifacts may be incomplete."
        }

        Write-Host "Using PR #$PullRequestId build $BuildId ($buildNumber) [$buildResult]"
    } else {
        Write-Host "Finding latest successful build from main..."
        $buildsUrl = "$Organization/$Project/_apis/build/builds?definitions=$PipelineDefinitionId&branchName=refs/heads/main&resultFilter=succeeded&`$top=1&api-version=7.1"
        $buildsResp = Invoke-RestMethod -Uri $buildsUrl -Headers $headers -Method Get
        if ($buildsResp.count -eq 0) {
            throw "No successful builds found for pipeline $PipelineDefinitionId on main"
        }
        $BuildId = $buildsResp.value[0].id
        $buildNumber = $buildsResp.value[0].buildNumber
        Write-Host "Using build $BuildId ($buildNumber)"
    }
}

# Create temp directory
$tempDir = Join-Path ([System.IO.Path]::GetTempPath()) "azd-ci-coverage-$BuildId"
if (Test-Path $tempDir) {
    Remove-Item -Recurse -Force $tempDir
}
New-Item -ItemType Directory -Force -Path $tempDir | Out-Null

function Download-Artifact {
    param([string]$ArtifactName, [string]$DestDir)

    Write-Host "  Downloading $ArtifactName..."
    $url = "$Organization/$Project/_apis/build/builds/$BuildId/artifacts?artifactName=$ArtifactName&api-version=7.1"
    $resp = Invoke-RestMethod -Uri $url -Headers $headers -Method Get
    $downloadUrl = $resp.resource.downloadUrl

    $zipPath = Join-Path $tempDir "$ArtifactName.zip"
    Invoke-WebRequest -Uri $downloadUrl -Headers $headers -OutFile $zipPath

    $extractPath = Join-Path $tempDir $ArtifactName
    Expand-Archive -Path $zipPath -DestinationPath $extractPath -Force
    Remove-Item $zipPath

    # Pipeline artifacts nest under the artifact name
    $nested = Join-Path $extractPath $ArtifactName
    if (Test-Path $nested) {
        return $nested
    }
    return $extractPath
}

# Download artifacts
Write-Host "Downloading coverage artifacts from build $BuildId..."
$unitDir = Download-Artifact -ArtifactName "cover-unit" -DestDir $tempDir
$intDir = Download-Artifact -ArtifactName "cover-int" -DestDir $tempDir

# Merge coverage
$mergedDir = Join-Path $tempDir "cover-merged"
New-Item -ItemType Directory -Force -Path $mergedDir | Out-Null

Write-Host "Merging unit + integration coverage..."
go tool covdata merge -i="$unitDir,$intDir" -o "$mergedDir"
if ($LASTEXITCODE) {
    throw "go tool covdata merge failed"
}

# Convert to text format
Write-Host "Converting to text format..."
go tool covdata textfmt -i="$mergedDir" -o $OutputFile
if ($LASTEXITCODE) {
    throw "go tool covdata textfmt failed"
}

# Filter generated code (e.g. protobuf *.pb.go) so coverage reflects hand-written code only.
$filterScript = Join-Path $PSScriptRoot "Filter-GeneratedCoverage.ps1"
if (Test-Path $filterScript) {
    & $filterScript -CoverageFile $OutputFile
    if ($LASTEXITCODE) {
        throw "Filter-GeneratedCoverage failed"
    }
}

$lineCount = (Get-Content $OutputFile).Count
Write-Host "Combined coverage written to $OutputFile ($lineCount lines)"

# Show report if requested
if ($ShowReport) {
    Write-Host ""
    Write-Host "=========================================="
    Write-Host "  Combined Coverage Report (Build $BuildId)"
    Write-Host "=========================================="
    Write-Host ""

    $percentOutput = go tool covdata percent -i="$mergedDir" 2>&1
    $parsed = $percentOutput | ForEach-Object {
        if ($_ -match '([\w/./-]+)\s+coverage:\s+([\d.]+)%') {
            $pkg = $Matches[1] -replace 'github.com/azure/azure-dev/cli/azd/', ''
            $pct = [double]$Matches[2]
            [PSCustomObject]@{ Package = $pkg; Coverage = $pct }
        }
    }

    if ($MinCoverage -ge 0) {
        $parsed = $parsed | Where-Object { $_.Coverage -lt $MinCoverage }
        Write-Host "Packages below ${MinCoverage}% coverage:"
    } else {
        Write-Host "All packages (sorted by coverage):"
    }

    Write-Host ""
    $parsed | Sort-Object Coverage | Format-Table -AutoSize

    $avg = ($parsed | Measure-Object -Property Coverage -Average).Average
    $count = $parsed.Count
    Write-Host "Packages shown: $count | Average coverage: $([math]::Round($avg, 1))%"
}

# Cleanup temp files (keep the output file)
Remove-Item -Recurse -Force $tempDir

Write-Host ""
Write-Host "Done. Combined coverage profile: $OutputFile"
