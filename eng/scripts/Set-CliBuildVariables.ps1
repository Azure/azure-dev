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


# AZURE_RECORD_MODE is controlled by the AzureRecordMode pipeline parameter
# defined in release-cli.yml and passed through build-and-test.yml → build-cli.yml.
