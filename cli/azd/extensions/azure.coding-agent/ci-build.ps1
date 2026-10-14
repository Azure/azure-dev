param(
    [string] $Version = (Get-Content "$PSScriptRoot/version.txt" -ErrorAction Stop),
    [string] $SourceVersion = (git rev-parse HEAD),
    [switch] $CodeCoverageEnabled,
    [switch] $BuildRecordMode,
    [string] $MSYS2Shell, # retained for compatibility with existing CI arguments
    [string] $OutputFileName
)

$ErrorActionPreference = 'Stop'
# Keep the existing linker argument quoting mode.
$PSNativeCommandArgumentPassing = 'Legacy'

# Preserve the combined version string printed by existing releases.
& "$PSScriptRoot/../scripts/ci-build.ps1" `
    -VersionPackages @("azurecodingagent/internal/cmd") `
    -Version "$Version (commit $SourceVersion)" `
    -SourceVersion $SourceVersion `
    -CodeCoverageEnabled:$CodeCoverageEnabled `
    -BuildRecordMode:$BuildRecordMode `
    -OutputFileName $OutputFileName
if ($LASTEXITCODE) {
    exit $LASTEXITCODE
}
