param(
    [string] $Version = (Get-Content "$PSScriptRoot/../version.txt"),
    [string] $SourceVersion = (git rev-parse HEAD)
)

# On Windows, use the goversioninfo tool to embed the version information into the executable.
if ($IsWindows) {
    Write-Host "Windows build, set verison info and run 'go generate'"
    if (! (Get-Command "goversioninfo" -ErrorAction SilentlyContinue)) {
        Write-Host "goversioninfo not found, installing"
        go install github.com/josephspurrier/goversioninfo/cmd/goversioninfo@v1.4.0
        Get-Command "goversioninfo" -ErrorAction Stop
    }

    $VERSION_INFO_PATH = "$PSScriptRoot/versioninfo.json"

    $exeFileVersion = ."$PSScriptRoot/../../eng/scripts/Get-MsiVersion.ps1" -CliVersion $Version
    $splitExeFileVersion = $exeFileVersion -split '\.'
    $versionInfo = Get-Content $VERSION_INFO_PATH | ConvertFrom-Json

    $versionInfo.FixedFileInfo.FileVersion.Major = [int]$splitExeFileVersion[0]
    $versionInfo.FixedFileInfo.FileVersion.Minor = [int]$splitExeFileVersion[1]
    $versionInfo.FixedFileInfo.FileVersion.Patch = [int]$splitExeFileVersion[2]
    $versionInfo.FixedFileInfo.FileVersion.Build = 0

    # Product verison is the same as the file version
    $versioninfo.FixedFileInfo.ProductVersion = $versionInfo.FixedFileInfo.FileVersion

    $versionInfo.StringFileInfo.ProductVersion = $Version

    $versionInfoJson = ConvertTo-Json $versionInfo -Depth 10
    Set-Content $VERSION_INFO_PATH $versionInfoJson
    Write-Host "go generate"
    go generate
    if ($LASTEXITCODE) {
        Write-Host "Error running go generate"
        exit $LASTEXITCODE
    }
    Write-Host "go generate succeeded"
}

# Force DNS resolution to not use cgo
$env:GODEBUG=netdns=go+1

Write-Host "go build"
go build -ldflags="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=$Version (commit $SourceVersion)'"

if ($LASTEXITCODE) {
    Write-Host "Error running go build"
    exit $LASTEXITCODE
}
Write-Host "go build succeeded"

if ($IsWindows) {
    Write-Host "Windows exe file verison info"
    $azdExe = Get-Item azd.exe
    Write-Host "File Version: $($azdExe.VersionInfo.FileVersionRaw)"
    Write-Host "Product Version: $($azdExe.VersionInfo.ProductVersionRaw)"
}