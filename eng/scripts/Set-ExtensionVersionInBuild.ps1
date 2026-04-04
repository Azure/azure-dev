<#
    .SYNOPSIS
    Appends a prerelease suffix to the extension's version.txt for CI/PR builds.
    Skips for Manual (release) builds.

    .PARAMETER ExtensionDirectory
    Path to the extension directory containing version.txt.

    .PARAMETER BuildReason
    The build reason from CI (e.g. Build.Reason).

    .PARAMETER BuildId
    A unique build ID from CI (e.g. Build.BuildId).
 #>
param(
    [Parameter(Mandatory)] [string] $ExtensionDirectory,
    [Parameter(Mandatory)] [string] $BuildReason,
    [Parameter(Mandatory)] [string] $BuildId
)

Write-Host "Build reason: $BuildReason"

$prereleaseCategory = ""

if ($BuildReason -eq "Manual") {
    Write-Host "Skipping prerelease tagging for release build."
    exit 0
}
elseif ($BuildReason -eq "PullRequest") {
    $prereleaseCategory = "pr"
}
else {
    $prereleaseCategory = "daily"
}

$versionFile = Join-Path $ExtensionDirectory "version.txt"
$version = (Get-Content $versionFile).Trim()
if ([string]::IsNullOrWhiteSpace($version)) {
    Write-Error "version.txt is empty at $versionFile"
    exit 1
}
$newVersion = "$version-$prereleaseCategory.$BuildId"

Set-Content $versionFile -Value $newVersion
Write-Host "Set version.txt contents to: $newVersion"
