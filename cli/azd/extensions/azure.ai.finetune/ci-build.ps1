param(
    [string] $Version = (Get-Content "$PSScriptRoot/version.txt" -ErrorAction Stop),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode,
    [string] $MSYS2Shell, # retained for compatibility with existing CI arguments
    [string] $OutputFileName
)

$ErrorActionPreference = 'Stop'
# Keep the caller's native argument mode.

& "$PSScriptRoot/../scripts/ci-build.ps1" `
    -VersionPackages @("azure.ai.finetune/internal/version") `
    -Version $Version `
    -SourceVersion $SourceVersion `
    -CodeCoverageEnabled:$CodeCoverageEnabled `
    -BuildRecordMode:$BuildRecordMode `
    -OutputFileName $OutputFileName
if ($LASTEXITCODE) {
    exit $LASTEXITCODE
}
