param(
    [string] $Version = (Get-Content "$PSScriptRoot/version.txt" -ErrorAction Stop).Trim(),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode,
    [string] $MSYS2Shell, # retained for compatibility with existing CI arguments
    [string] $OutputFileName
)

$ErrorActionPreference = 'Stop'

& "$PSScriptRoot/../scripts/ci-build.ps1" `
    -VersionPackages @("azurespringboot/internal/version") `
    -Version $Version `
    -SourceVersion $SourceVersion `
    -CodeCoverageEnabled:$CodeCoverageEnabled `
    -BuildRecordMode:$BuildRecordMode `
    -OutputFileName $OutputFileName
if ($LASTEXITCODE) {
    exit $LASTEXITCODE
}
