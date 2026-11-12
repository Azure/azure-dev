param(
    [string] $Version = (Get-Content "$PSScriptRoot/version.txt" -ErrorAction Stop),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode,
    [string] $OutputFileName
)

$ErrorActionPreference = 'Stop'
$PSNativeCommandArgumentPassing = 'Legacy'

& "$PSScriptRoot/../scripts/ci-build.ps1" `
    -VersionPackages @("azure.resources/internal/version") `
    -Version $Version `
    -SourceVersion $SourceVersion `
    -CodeCoverageEnabled:$CodeCoverageEnabled `
    -BuildRecordMode:$BuildRecordMode `
    -OutputFileName $OutputFileName
if ($LASTEXITCODE) {
    exit $LASTEXITCODE
}
