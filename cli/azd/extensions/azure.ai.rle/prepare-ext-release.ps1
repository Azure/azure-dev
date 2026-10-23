<#
.SYNOPSIS
Prepares dev and customer packages for one version, with customer files in a supplied public checkout.

.PARAMETER AzureDevRepositoryPath
Root of an existing, separate Azure/azure-dev checkout.
The script fetches and switches to the existing remote rle-releases branch.

.EXAMPLE
.\prepare-ext-release.ps1 -AzureDevRepositoryPath "C:\repos\azure-dev" -VersionBump patch

.EXAMPLE
.\prepare-ext-release.ps1 -AzureDevRepositoryPath "C:\repos\azure-dev" -BreakingChanges
#>
param(
    [string] $AzureDevRepositoryPath,
    [ValidateSet("major", "minor", "patch")]
    [string] $VersionBump = "patch",
    [switch] $BreakingChanges
)

$ErrorActionPreference = "Stop"
$repository = "Azure/azure-dev"
$branch = "rle-releases"
$PublicRepositoryPath = $AzureDevRepositoryPath
if ([string]::IsNullOrWhiteSpace($PublicRepositoryPath)) {
    throw "AzureDevRepositoryPath is required. Supply -AzureDevRepositoryPath with an existing Azure/azure-dev checkout."
}

function Invoke-ReleaseGit {
    param([string[]] $Arguments)
    $result = & git -C $PublicRepositoryPath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "git $($Arguments -join ' ') failed in '$PublicRepositoryPath'."
    }
    return $result
}

foreach ($tool in @("git", "go", "azd")) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "$tool is required to prepare an external release."
    }
}

$PublicRepositoryPath = [IO.Path]::GetFullPath($PublicRepositoryPath).TrimEnd('\', '/')
if (-not (Test-Path -LiteralPath $PublicRepositoryPath -PathType Container)) {
    throw "AzureDevRepositoryPath must be an existing Azure/azure-dev checkout."
}
$sourceRoot = & git -C $PSScriptRoot rev-parse --show-toplevel
if ($LASTEXITCODE -ne 0) {
    throw "Could not locate the source repository."
}
$sourceRoot = [IO.Path]::GetFullPath($sourceRoot).TrimEnd('\', '/')
if ($PublicRepositoryPath.Equals($sourceRoot, [StringComparison]::OrdinalIgnoreCase) -or
    $PublicRepositoryPath.StartsWith("$sourceRoot$([IO.Path]::DirectorySeparatorChar)",
        [StringComparison]::OrdinalIgnoreCase)) {
    throw "The public distribution checkout must be outside the source repository."
}
$publicRoot = [IO.Path]::GetFullPath((Invoke-ReleaseGit @("rev-parse", "--show-toplevel"))).TrimEnd('\', '/')
if (-not $publicRoot.Equals($PublicRepositoryPath, [StringComparison]::OrdinalIgnoreCase)) {
    throw "AzureDevRepositoryPath must be the root of the separate distribution checkout."
}
foreach ($arguments in @(
    @("remote", "get-url", "origin"),
    @("remote", "get-url", "--push", "--all", "origin")
)) {
    $remotes = @(Invoke-ReleaseGit $arguments)
    foreach ($remote in $remotes) {
        if ($remote -notin @("https://github.com/$repository.git", "https://github.com/$repository",
                "git@github.com:$repository.git")) {
            throw "The distribution checkout origin must point directly to $repository, not a fork."
        }
    }
}
if (Invoke-ReleaseGit @("status", "--porcelain")) {
    throw "The distribution checkout must be clean. Commit or discard previous changes before preparing a release."
}
$gitDirectory = Invoke-ReleaseGit @("rev-parse", "--absolute-git-dir")
foreach ($state in @("MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "rebase-merge", "rebase-apply", "sequencer")) {
    if (Test-Path -LiteralPath (Join-Path $gitDirectory $state)) {
        throw "Finish the current Git operation before preparing a release."
    }
}

Invoke-ReleaseGit @("fetch", "origin", "${branch}:refs/remotes/origin/$branch") | Out-Host
$localBranch = Invoke-ReleaseGit @("branch", "--list", $branch)
if ($localBranch) {
    $unpublished = Invoke-ReleaseGit @("rev-list", "--count", "origin/$branch..refs/heads/$branch")
    if ([int]$unpublished -ne 0) {
        throw "The distribution checkout has unpublished or diverged commits. Synchronize it before preparing a release."
    }
    if ((Invoke-ReleaseGit @("branch", "--show-current")) -ne $branch) {
        Invoke-ReleaseGit @("switch", $branch) | Out-Host
    }
}
else {
    # Single-branch clones need this refspec before Git can configure tracking.
    Invoke-ReleaseGit @("remote", "set-branches", "--add", "origin", $branch) | Out-Host
    Invoke-ReleaseGit @("switch", "--track", "-c", $branch, "origin/$branch") | Out-Host
}
Invoke-ReleaseGit @("merge", "--ff-only", "origin/$branch") | Out-Host
if (Invoke-ReleaseGit @("status", "--porcelain")) {
    throw "The distribution checkout must be clean after switching and synchronizing '$branch'. Commit or discard previous changes before preparing a release."
}

& "$PSScriptRoot/prepare-release.ps1" -Channel rle-ext -VersionBump $VersionBump `
    -BreakingChanges:$BreakingChanges -PublicRepositoryPath $PublicRepositoryPath

$version = (Get-Content -LiteralPath (Join-Path $PSScriptRoot "version.txt") -Raw).Trim()
Remove-Item -LiteralPath (Join-Path $PSScriptRoot "bin") -Recurse -Force

Write-Host ""
Write-Host "Release $version prepared locally. No files were staged, committed, or pushed."
Write-Host "Review the changes and raise or update PRs in:"
Write-Host "  Implementation: $sourceRoot"
Write-Host "  Distribution:   $PublicRepositoryPath"
