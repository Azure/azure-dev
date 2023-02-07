param(
    [string] $Version,
    [string] $Tag = "azure-dev-cli_$Version",
    [string] $MsiSha256
)

$originalLocation = Get-Location
try {
    Set-Location "$PSScriptRoot../../../cli/installer/choco"

    # Set SHA256 in chocolateyinstall.ps1
    $CHOCO_INSTALL_SCRIPT_PATH = 'tools/chocolateyinstall.ps1'
    $chocolateyInstallContent = Get-Content -Raw $CHOCO_INSTALL_SCRIPT_PATH
    $chocolateyInstallContent = $chocolateyInstallContent.Replace('%SHA256%', $MsiSha256)
    Set-Content -Path $CHOCO_INSTALL_SCRIPT_PATH -Value $chocolateyInstallContent

    # Copy NOTICE.txt to tools directory
    Copy-Item -Path "$PSScriptRoot/../../NOTICE.txt" -Destination 'tools/NOTICE.txt'

    choco pack .\azd.nuspec VERSION=$Version TAG=$Tag
} finally {
    Set-Location $originalLocation
}
