<#
    .SYNOPSIS
    Set-CliVersionInBuild sets the CLI version defined in version.txt.

    .PARAMETER BuildReason
    The build reason supplied from the CI provider. In Azure Pipelines, this would be "Build.Reason".

    .PARAMETER BuildId
    A unique build ID supplied from the CI provider. In Azure Pipelines, this would be "Build.BuildId".
 #>
param(
    [string]$BuildReason,
    [string]$BuildId
)

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

$prereleaseTag = "-$prereleaseCategory.$BuildId"
$version = "$(Get-Content cli/version.txt)$prereleaseTag"

Set-Content cli/version.txt -Value $version
Write-Host "Set version.txt contents to: $version"
