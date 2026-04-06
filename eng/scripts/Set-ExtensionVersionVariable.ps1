param(
    [string] $ExtensionDirectory 
)

$extVersion = (Get-Content "$ExtensionDirectory/version.txt").Trim()
Write-Host "Extension Version: $extVersion"
Write-Host "##vso[task.setvariable variable=EXT_VERSION;]$extVersion"
