<#
    .SYNOPSIS
    Set-CliBuildVariables sets variables for the CLI build run in Azure Pipelines.
 #>
 param(
    # Build.Reason in Azure Pipelines
    [string]$BuildReason
 )

 function Set-BuildVariable() {
    param(
        [string]$Key,
        [string]$Value
    )

    # Set the env variable in the current scope
    Set-Item env:$Key -Value $Value

    # Set the variable as a build variable on the pipeline
    Write-Host "##vso[task.setvariable variable=$Key;]$Value"

    # Print out always in the build logs
    Write-Host "Set $Key=$Value"
 }


# AZURE_RECORD_MODE is used for running tests in a specific recording mode.
if (-not $env:AZURE_RECORD_MODE) {
    $recordMode = "live"

    if ($BuildReason -eq "PullRequest") {
        $recordMode = "playback"
    }

    Set-BuildVariable -Key "AZURE_RECORD_MODE" -Value $recordMode
}
