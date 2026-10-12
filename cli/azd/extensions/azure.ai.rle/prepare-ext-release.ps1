<#
.SYNOPSIS
Builds RLE extension artifacts and publishes an artifacts-only Azure/azure-dev PR.

.PARAMETER VersionBump
Increments the current semantic version by major, minor, or patch and preserves
its prerelease suffix. Updates version.txt and extension.yaml automatically.

.EXAMPLE
.\prepare-ext-release.ps1 -VersionBump patch

.EXAMPLE
.\prepare-ext-release.ps1 -VersionBump minor -BreakingChanges

.PARAMETER PublicRepositoryPath
Separate Azure/azure-dev distribution checkout. Created automatically if absent.
Defaults to .azd/rle-releases under the current user's home directory.
#>
param(
    [ValidateSet("major", "minor", "patch")]
    [string] $VersionBump = "patch",
    [switch] $BreakingChanges,
    [string] $PublicRepositoryPath = (Join-Path $HOME ".azd\rle-releases")
)

$ErrorActionPreference = "Stop"

$repository = "Azure/azure-dev"
$branch = "rle-releases"
$registryRelativePath = "cli/azd/extensions/registry.rle.json"
$artifactRelativeRoot = "cli/azd/extensions/azure.ai.rle/artifacts/rle"
$trailer = "Co-authored-by: Copilot <223556219+Copilot@users.noreply.github.com>"

function Invoke-ReleaseGit {
    param([string[]] $Arguments)
    $result = & git -C $PublicRepositoryPath @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "git $($Arguments -join ' ') failed in '$PublicRepositoryPath'."
    }
    return $result
}

function Invoke-ReleaseGh {
    param([string[]] $Arguments)
    $result = & gh @Arguments
    if ($LASTEXITCODE -ne 0) {
        throw "gh $($Arguments -join ' ') failed."
    }
    return $result
}

foreach ($tool in @("git", "gh", "go", "azd")) {
    if (-not (Get-Command $tool -ErrorAction SilentlyContinue)) {
        throw "$tool is required to prepare an external release."
    }
}
Invoke-ReleaseGh @("auth", "status") | Out-Host

$PublicRepositoryPath = [IO.Path]::GetFullPath($PublicRepositoryPath)
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

if (-not (Test-Path -LiteralPath $PublicRepositoryPath)) {
    $remoteBranch = & git ls-remote --heads "https://github.com/$repository.git" $branch
    if ($LASTEXITCODE -ne 0) {
        throw "Could not query the public distribution branch."
    }
    $cloneBranch = if ($remoteBranch) { $branch } else { "main" }
    & git clone --filter=blob:none --no-checkout --single-branch --branch $cloneBranch `
        "https://github.com/$repository.git" $PublicRepositoryPath
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to create the public distribution checkout."
    }
    Invoke-ReleaseGit @("sparse-checkout", "set", "--no-cone", "/$registryRelativePath", "/$artifactRelativeRoot/") |
        Out-Host
    if ($cloneBranch -eq $branch) {
        Invoke-ReleaseGit @("switch", $branch) | Out-Host
    }
    else {
        Invoke-ReleaseGit @("switch", "-c", $branch, "origin/main") | Out-Host
    }
}

$remote = Invoke-ReleaseGit @("remote", "get-url", "--push", "origin")
if ($remote -notin @("https://github.com/$repository.git", "https://github.com/$repository",
        "git@github.com:$repository.git")) {
    throw "The distribution checkout origin must point directly to $repository, not a fork."
}
$currentBranch = Invoke-ReleaseGit @("branch", "--show-current")
$publicRoot = [IO.Path]::GetFullPath((Invoke-ReleaseGit @("rev-parse", "--show-toplevel"))).TrimEnd('\', '/')
if (-not $publicRoot.Equals($PublicRepositoryPath.TrimEnd('\', '/'), [StringComparison]::OrdinalIgnoreCase)) {
    throw "PublicRepositoryPath must be the root of the separate distribution checkout."
}
if ($currentBranch -ne $branch) {
    throw "The distribution checkout must be on '$branch', not '$currentBranch'."
}
if (Invoke-ReleaseGit @("status", "--porcelain")) {
    throw "The distribution checkout must be clean. Review and finish any previous release before retrying."
}
Invoke-ReleaseGit @("fetch", "origin", "main:refs/remotes/origin/main") | Out-Host
$remoteBranch = Invoke-ReleaseGit @("ls-remote", "--heads", "origin", $branch)
if ($remoteBranch) {
    Invoke-ReleaseGit @("fetch", "origin", "${branch}:refs/remotes/origin/$branch") | Out-Host
    Invoke-ReleaseGit @("merge", "--ff-only", "origin/$branch") | Out-Host
}
$base = Invoke-ReleaseGit @("merge-base", "HEAD", "origin/main")
$existingChanges = @(Invoke-ReleaseGit @("diff", "--name-only", "$base..HEAD"))
foreach ($path in $existingChanges) {
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
if (Test-Path -LiteralPath $artifactDirectory) {
    throw "Public artifacts for '$version' already exist; published versions must not be overwritten."
}
New-Item -ItemType Directory -Path $artifactDirectory -Force | Out-Null
Get-ChildItem -LiteralPath (Join-Path $PSScriptRoot "artifacts\rle-ext\$version") -File |
    Where-Object { $_.Name -match "\.(zip|tar\.gz)$" } |
    ForEach-Object { Copy-Item -LiteralPath $_.FullName -Destination $artifactDirectory }
foreach ($file in Get-ChildItem -LiteralPath $artifactDirectory -File) {
    if ($file.Length -ge 100MB) {
        throw "Artifact '$($file.Name)' exceeds GitHub's Git file-size limit; use release assets instead."
    }
}
foreach ($file in Get-ChildItem -LiteralPath $artifactDirectory -File) {
    Invoke-ReleaseGit @("add", "--force", "--", "$artifactRelativePath/$($file.Name)") | Out-Host
}
Invoke-ReleaseGit @("commit", "-m", "Publish RLE $version binaries", "-m", $trailer) | Out-Host
$artifactCommit = Invoke-ReleaseGit @("rev-parse", "HEAD")

$registryPath = Join-Path $PublicRepositoryPath $registryRelativePath
$registry = Get-Content -LiteralPath $registryPath -Raw | ConvertFrom-Json
$versionEntry = @($registry.extensions | Where-Object id -eq "azure.ai.rle").versions |
    Where-Object version -eq $version
foreach ($artifact in $versionEntry.artifacts.PSObject.Properties) {
    $name = Split-Path -Leaf $artifact.Value.url
    $artifact.Value.url = "https://raw.githubusercontent.com/$repository/$artifactCommit/$artifactRelativePath/$name"
}
foreach ($extension in $registry.extensions) {
    foreach ($entry in $extension.versions) {
        foreach ($artifact in $entry.artifacts.PSObject.Properties) {
            if (-not $artifact.Value.url.StartsWith("https://raw.githubusercontent.com/$repository/")) {
                throw "Public registry contains a non-$repository artifact URL: $($artifact.Value.url)"
            }
        }
    }
}
$encoding = New-Object System.Text.UTF8Encoding -ArgumentList $false
[IO.File]::WriteAllText($registryPath, ($registry | ConvertTo-Json -Depth 100) + [Environment]::NewLine, $encoding)
Invoke-ReleaseGit @("add", "--", $registryRelativePath) | Out-Host
Invoke-ReleaseGit @("commit", "-m", "Update external RLE registry to $version", "-m", $trailer) | Out-Host
Invoke-ReleaseGit @("push", "--set-upstream", "origin", $branch) | Out-Host

$pullRequests = Invoke-ReleaseGh @("pr", "list", "--repo", $repository, "--head", $branch,
    "--base", "main", "--state", "open", "--json", "url") | ConvertFrom-Json
if (@($pullRequests).Count -eq 0) {
    $body = @'
## Install the RLE extension (private preview)

Prerequisite: [Azure Developer CLI (azd)](https://learn.microsoft.com/azure/developer/azure-developer-cli/install-azd).

### Add the source

Run this once to register the RLE extension source:

```powershell
azd extension source add --name rle --type url --location "{registryURL}"
```

### Install

```powershell
azd extension install azure.ai.rle --source rle
```

### Verify and explore commands

```powershell
azd ai rle version
azd ai rle --help
```

### Update

When a new version is available, update from the same source:

```powershell
azd extension update azure.ai.rle --source rle
azd ai rle version
```
'@
    $body = $body.Replace("{registryURL}", "https://raw.githubusercontent.com/$repository/$branch/$registryRelativePath")
    Invoke-ReleaseGh @("pr", "create", "--repo", $repository, "--base", "main", "--head", $branch,
        "--draft", "--title", "RLE private preview: registry and packaged binaries", "--body", $body) | Out-Host
}
else {
    Write-Host "Updated PR: $($pullRequests[0].url)"
}
Write-Host "Registry: https://raw.githubusercontent.com/$repository/$branch/$registryRelativePath"
Write-Host "Source version changes remain local; commit them only in the implementation repository."
