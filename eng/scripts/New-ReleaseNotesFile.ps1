param(
    [string] $ChangeLogPath,
    [string] $Version,
    [string] $OutputPath = (New-TemporaryFile),
    [switch] $DevOpsOutputFormat
)

. "$PSScriptRoot../../common/scripts/common.ps1"

Set-StrictMode -Version 4

$entry = Get-ChangeLogEntry `
    -ChangeLogLocation $ChangeLogPath `
    -VersionString $Version
$entryString = ChangeLogEntryAsString -changeLogEntry $entry

Set-Content -Path $OutputPath -Value $entryString

if ($DevOpsOutputFormat) {
    Write-Host "##vso[task.setvariable variable=ReleaseChangeLogPath;]$OutputPath"
}
