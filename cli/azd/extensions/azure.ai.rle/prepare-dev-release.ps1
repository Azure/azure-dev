<#
.SYNOPSIS
Builds RLE extension artifacts and updates the internal development registry.

.PARAMETER VersionBump
Increments the current semantic version by major, minor, or patch and preserves
its prerelease suffix. Updates version.txt and extension.yaml automatically.

.EXAMPLE
.\prepare-dev-release.ps1 -VersionBump patch

.EXAMPLE
.\prepare-dev-release.ps1 -VersionBump minor -BreakingChanges
#>
param(
    [ValidateSet("major", "minor", "patch")]
    [string] $VersionBump = "patch",
    [switch] $BreakingChanges
)

$ErrorActionPreference = "Stop"

& "$PSScriptRoot/prepare-release.ps1" -Channel rle-dev @PSBoundParameters

Write-Host ""
Write-Host "Review the changes and raise a PR in the implementation repository."
