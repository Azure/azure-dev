$cliVersion = Get-Content $PSScriptRoot/../../cli/version.txt
Write-Host "CLI Version: $cliVersion"
Write-Host "##vso[task.setvariable variable=CLI_VERSION;]$cliVersion"