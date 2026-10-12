<#
.SYNOPSIS
Prepares dev and customer packages for one version, with customer files in a supplied public checkout.

.PARAMETER AzureDevRepositoryPath
Root of an existing, separate Azure/azure-dev checkout on rle-releases.

.EXAMPLE
.\prepare-ext-release.ps1 -AzureDevRepositoryPath "C:\repos\azure-dev" -VersionBump patch

.EXAMPLE
.\prepare-ext-release.ps1 -AzureDevRepositoryPath "C:\repos\azure-dev" -BreakingChanges
#>
param(
    [Parameter(Mandatory)]
    [ValidateNotNullOrEmpty()]
    [string] $AzureDevRepositoryPath,
    [ValidateSet("major", "minor", "patch")]
    [string] $VersionBump = "patch",
    [switch] $BreakingChanges
)

$ErrorActionPreference = "Stop"
$repository = "Azure/azure-dev"
$branch = "rle-releases"
$registryRelativePath = "cli/azd/extensions/registry.rle.json"
$artifactRelativeRoot = "cli/azd/extensions/azure.ai.rle/artifacts"
$PublicRepositoryPath = $AzureDevRepositoryPath
if ([string]::IsNullOrWhiteSpace($PublicRepositoryPath)) {
    throw "AzureDevRepositoryPath must not be blank."
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
$currentBranch = Invoke-ReleaseGit @("branch", "--show-current")
if ($currentBranch -ne $branch) {
    throw "The distribution checkout must be on '$branch', not '$currentBranch'."
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

Invoke-ReleaseGit @("fetch", "origin", "main:refs/remotes/origin/main",
    "${branch}:refs/remotes/origin/$branch") | Out-Host
$unpublished = Invoke-ReleaseGit @("rev-list", "--count", "origin/$branch..HEAD")
if ([int]$unpublished -ne 0) {
    throw "The distribution checkout has unpublished or diverged commits. Synchronize it before preparing a release."
}
Invoke-ReleaseGit @("merge", "--ff-only", "origin/$branch") | Out-Host
$base = Invoke-ReleaseGit @("merge-base", "HEAD", "origin/main")
foreach ($path in @(Invoke-ReleaseGit @("diff", "--name-only", "$base..HEAD"))) {
    if ($path -ne $registryRelativePath -and
        $path -notmatch "^$([regex]::Escape($artifactRelativeRoot))/[^/]+/azure-ai-rle-(windows|darwin|linux)-(amd64|arm64)\.(zip|tar\.gz)$") {
        throw "Distribution branch contains a non-release change: $path"
    }
}

& "$PSScriptRoot/prepare-release.ps1" -Channel rle-ext -VersionBump $VersionBump `
    -BreakingChanges:$BreakingChanges -PublicRepositoryPath $PublicRepositoryPath

$version = (Get-Content -LiteralPath (Join-Path $PSScriptRoot "version.txt") -Raw).Trim()
$artifactRelativePath = "$artifactRelativeRoot/$version"
$artifactDirectory = Join-Path $PublicRepositoryPath $artifactRelativePath
$packageDirectory = Join-Path $PSScriptRoot "artifacts\rle-dev\$version"
$artifacts = @(Get-ChildItem -LiteralPath $packageDirectory -File |
    Where-Object { $_.Name -match "\.(zip|tar\.gz)$" })
foreach ($file in $artifacts) {
    if ($file.Length -ge 100MB) {
        throw "Artifact '$($file.Name)' exceeds GitHub's Git file-size limit."
    }
}
New-Item -ItemType Directory -Path $artifactDirectory -Force | Out-Null
foreach ($file in $artifacts) {
    Copy-Item -LiteralPath $file.FullName -Destination $artifactDirectory
}
foreach ($file in $artifacts) {
    $copiedFile = Join-Path $artifactDirectory $file.Name
    if ((Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash -ne
        (Get-FileHash -LiteralPath $copiedFile -Algorithm SHA256).Hash) {
        throw "Copied artifact '$($file.Name)' failed checksum verification. Local build output was retained."
    }
}
Remove-Item -LiteralPath (Join-Path $PSScriptRoot "bin") -Recurse -Force

Write-Host ""
Write-Host "Release $version prepared locally. No files were staged, committed, or pushed."
Write-Host "Review the registry and all six archives, then run:"
Write-Host "  git -C `"$PublicRepositoryPath`" add --force -- `"$artifactRelativePath`""
Write-Host "  git -C `"$PublicRepositoryPath`" add -- `"$registryRelativePath`""
Write-Host "  git -C `"$PublicRepositoryPath`" diff --cached --stat"
Write-Host "  git -C `"$PublicRepositoryPath`" commit -m `"Publish RLE $version`""
Write-Host "  git -C `"$PublicRepositoryPath`" push origin $branch"
Write-Host "The push updates the existing distribution PR. Versioned archive URLs use the $branch branch."
Write-Host "Commit version.txt, extension.yaml, registry.rle-dev.json and dev packages together in the implementation repository."
