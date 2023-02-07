param(
    [string] $Version,
    [string] $Tag = "azure-dev-cli_$Version"
)

$originalLocation = Get-Location
try {
    Set-Location "$PSScriptRoot../../../cli/installer/choco"

    # Copy NOTICE.txt to tools directory
    Copy-Item -Path "$PSScriptRoot/../../NOTICE.txt" -Destination 'tools/NOTICE.txt'

    choco pack .\azd.nuspec VERSION=$Version TAG=$Tag
} finally {
    Set-Location $originalLocation
}
