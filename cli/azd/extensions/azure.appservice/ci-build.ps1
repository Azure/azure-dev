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

& "$PSScriptRoot/../scripts/ci-build.ps1" `
    -VersionPackages @("azureappservice/internal/version") `
    -VersionOnly `
    -Version $Version `
    -SourceVersion $SourceVersion `
    -CodeCoverageEnabled:$CodeCoverageEnabled `
    -BuildRecordMode:$BuildRecordMode `
    -OutputFileName $OutputFileName
if ($LASTEXITCODE) {
    exit $LASTEXITCODE
}
