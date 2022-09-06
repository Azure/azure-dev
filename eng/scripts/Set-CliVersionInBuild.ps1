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
    # This intentionally covers all scheduled/CI trigger definitions: IndividualCI, BatchedCI, Schedule.
    # For all other types such as ResourceTrigger, we default back to 'daily' as a sensible default.
    # See https://docs.microsoft.com/en-us/azure/devops/pipelines/build/variables?view=azure-devops&tabs=yaml#build-variables-devops-services for full list.
    $prereleaseCategory = "daily"
}

$prereleaseTag = "-$prereleaseCategory.$BuildId"
$version = "$(Get-Content cli/version.txt)$prereleaseTag"

Set-Content cli/version.txt -Value $version
Write-Host "Set version.txt contents to: $version"
